package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type preparedFile struct {
	name     string
	path     string
	tempPath string
	current  []byte
	newData  []byte
	existed  bool
	mode     os.FileMode
}

type replacedFile struct {
	path       string
	current    []byte
	existed    bool
	mode       os.FileMode
	backupPath string
}

func (s *Service) apply(ctx context.Context, request map[string]any) (map[string]any, error) {
	if strict, ok := request["strict_concurrency"].(bool); ok && strict {
		if !s.mu.TryLock() {
			return nil, newProtocolErrorf("concurrency conflict: model_config operation in progress")
		}
		defer s.mu.Unlock()
	} else {
		s.mu.Lock()
		defer s.mu.Unlock()
	}

	if target, ok := request["target"].(map[string]any); ok && target != nil {
		if kind, _ := target["kind"].(string); kind != "" && kind != "local" {
			rawFiles, ok := request["files"].(map[string]any)
			if !ok || rawFiles == nil {
				return nil, newProtocolErrorf("模型配置写入内容无效")
			}
			apiKey := ""
			if raw, exists := request["api_key"]; exists {
				var ok bool
				apiKey, ok = raw.(string)
				if !ok {
					return nil, newProtocolErrorf("模型配置写入内容无效")
				}
			}
			payload := map[string]any{
				"action":  "apply",
				"files":   rawFiles,
				"api_key": apiKey,
			}
			if raw, exists := request["api_keys"]; exists {
				payload["api_keys"] = raw
			}
			return executeRemoteRequest(ctx, target, payload)
		}
	}

	root, err := s.resolveTargetRoot(request)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, err
	}

	rawFiles, ok := request["files"].(map[string]any)
	if !ok || rawFiles == nil {
		return nil, newProtocolErrorf("模型配置写入内容无效")
	}

	credValues, err := credentialValues(request)
	if err != nil {
		return nil, err
	}

	prepared := make([]*preparedFile, 0, len(rawFiles))
	defer func() { s.cleanTempFiles(prepared) }()
	for name, val := range rawFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text, ok := val.(string)
		if !ok {
			return nil, newProtocolErrorf("模型配置写入内容无效")
		}
		rel, ok := ManagedFiles[name]
		if !ok {
			return nil, newProtocolErrorf("模型配置包含未授权文件")
		}

		fullPath, err := resolveWithinAllowed(s.allowedRoots, root, rel)
		if err != nil {
			return nil, err
		}
		if name == FileHermesConfig {
			if _, statErr := os.Stat(fullPath); statErr != nil {
				continue
			}
		}

		data := []byte(text)
		if err := validateManagedContent(name, data); err != nil {
			return nil, err
		}

		var current []byte
		existed := false
		mode := os.FileMode(0600)
		if fi, err := os.Stat(fullPath); err == nil && fi.Mode().IsRegular() {
			existed = true
			mode = fi.Mode().Perm()
			if c, err := os.ReadFile(fullPath); err == nil {
				current = c
			}
		}

		currSHA := EmptySHA256
		if existed {
			currSHA = sha256Hex(current)
		}
		if err := s.checkExpectedDigest(request, name, currSHA); err != nil {
			return nil, err
		}

		if existed && string(current) == text {
			continue
		}

		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, newProtocolErrorf("create parent dir: %v", err)
		}

		tempPath, stageErr := stagePrivateFile(dir, "."+filepath.Base(fullPath)+".astrorder-*", data)
		if stageErr != nil {
			s.cleanTempFiles(prepared)
			return nil, newProtocolErrorf("write temp file: %v", stageErr)
		}

		writtenData, err := os.ReadFile(tempPath)
		if err != nil || validateManagedContent(name, writtenData) != nil {
			_ = os.Remove(tempPath)
			s.cleanTempFiles(prepared)
			return nil, newProtocolErrorf("validation of staged file failed: %s", name)
		}

		prepared = append(prepared, &preparedFile{
			name:     name,
			path:     fullPath,
			tempPath: tempPath,
			current:  current,
			newData:  data,
			existed:  existed,
			mode:     mode,
		})
	}

	envPrepared := []*preparedFile{}
	defer func() { s.cleanTempFiles(envPrepared) }()
	writeEnvironmentFile := s.config.Credentials == nil
	if fileBackend, ok := s.config.Credentials.(interface{ EnvironmentFile() bool }); ok {
		writeEnvironmentFile = fileBackend.EnvironmentFile()
	}
	if writeEnvironmentFile {
		for _, name := range credentialNames {
			apiKey := credValues[name]
			if apiKey == "" {
				continue
			}
			envData, err := formatEnvData(name, apiKey)
			if err != nil {
				s.cleanTempFiles(prepared)
				return nil, err
			}
			envFullPath, err := resolveWithinAllowed(s.allowedRoots, root, CredentialFiles[name])
			if err != nil {
				s.cleanTempFiles(prepared)
				return nil, err
			}
			var current []byte
			existed := false
			if fi, err := os.Stat(envFullPath); err == nil && fi.Mode().IsRegular() {
				existed = true
				if c, err := os.ReadFile(envFullPath); err == nil {
					current = c
				}
			}
			if existed && string(current) == string(envData) {
				continue
			}
			dir := filepath.Dir(envFullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				s.cleanTempFiles(prepared)
				return nil, newProtocolErrorf("create env dir: %v", err)
			}
			tempPath, stageErr := stagePrivateFile(dir, "."+filepath.Base(envFullPath)+".astrorder-*", envData)
			if stageErr != nil {
				s.cleanTempFiles(prepared)
				return nil, newProtocolErrorf("write env temp file: %v", stageErr)
			}
			envPrepared = append(envPrepared, &preparedFile{
				name:     "env_config",
				path:     envFullPath,
				tempPath: tempPath,
				current:  current,
				newData:  envData,
				existed:  existed,
				mode:     0600,
			})
		}
	}

	allToReplace := prepared
	if len(envPrepared) > 0 {
		allToReplace = append(allToReplace, envPrepared...)
	}

	replaced := make([]*replacedFile, 0, len(allToReplace))
	var applyErr error

	for _, pf := range allToReplace {
		if err := ctx.Err(); err != nil {
			applyErr = err
			break
		}
		var backupPath string
		if pf.existed {
			dir := filepath.Dir(pf.path)
			base := filepath.Base(pf.path)
			backupPath = filepath.Join(dir, fmt.Sprintf("%s.astrorder-backup-%d", base, time.Now().UnixNano()))
			if err := os.WriteFile(backupPath, pf.current, pf.mode); err != nil {
				applyErr = newProtocolErrorf("write backup %s: %v", pf.name, err)
				break
			}
		}

		if err := os.Rename(pf.tempPath, pf.path); err != nil {
			applyErr = newProtocolErrorf("replace %s: %v", pf.name, err)
			break
		}

		_ = os.Chmod(pf.path, pf.mode)

		replaced = append(replaced, &replacedFile{
			path:       pf.path,
			current:    pf.current,
			existed:    pf.existed,
			mode:       pf.mode,
			backupPath: backupPath,
		})
	}

	var restoreCredentials func() error
	if applyErr == nil && len(credValues) > 0 && s.config.Credentials != nil {
		if multi, ok := s.config.Credentials.(interface {
			SetMany(context.Context, map[string]string) (func() error, error)
		}); ok {
			restoreCredentials, applyErr = multi.SetMany(ctx, credValues)
		} else if key := credValues[EnvTokenKey]; key != "" {
			restoreCredentials, applyErr = s.config.Credentials.Set(ctx, key)
		}
	}
	if applyErr != nil {
		if restoreCredentials != nil {
			applyErr = errors.Join(applyErr, restoreCredentials())
		}
		for i := len(replaced) - 1; i >= 0; i-- {
			rf := replaced[i]
			if rollbackErr := s.rollbackFile(rf); rollbackErr != nil {
				applyErr = errors.Join(applyErr, fmt.Errorf("rollback %s: %w", rf.path, rollbackErr))
			}
		}
		s.cleanTempFiles(allToReplace)
		return nil, applyErr
	}

	for _, pf := range allToReplace {
		s.pruneBackups(pf.path)
	}

	changedNames := make([]string, 0, len(prepared))
	digests := make(map[string]string)
	for _, pf := range prepared {
		changedNames = append(changedNames, pf.name)
		digests[pf.name] = sha256Hex(pf.newData)
	}
	sort.Strings(changedNames)

	inspectOut, err := s.plan(ctx, map[string]any{"target": map[string]any{"root": root}})
	if err != nil {
		return nil, err
	}

	filesMap := make(map[string]any)
	for key := range ManagedFiles {
		if val, ok := inspectOut[key]; ok {
			filesMap[key] = val
		}
	}
	filesMap["_home"] = root

	return map[string]any{
		"changed": changedNames,
		"files":   filesMap,
		"receipt": map[string]any{
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"status":    "validated",
			"changed":   changedNames,
			"digests":   digests,
		},
	}, nil
}

func (s *Service) cleanTempFiles(files []*preparedFile) {
	for _, pf := range files {
		if pf.tempPath != "" {
			_ = os.Remove(pf.tempPath)
		}
	}
}

func (s *Service) rollbackFile(rf *replacedFile) error {
	if !rf.existed {
		err := os.Remove(rf.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	dir := filepath.Dir(rf.path)
	base := filepath.Base(rf.path)
	rollbackTmp, err := stagePrivateFile(dir, "."+base+".astrorder-rollback-*", rf.current)
	if err != nil {
		return err
	}
	defer os.Remove(rollbackTmp)
	if err := os.Rename(rollbackTmp, rf.path); err != nil {
		return err
	}
	return os.Chmod(rf.path, rf.mode)
}

func (s *Service) pruneBackups(targetFile string) {
	dir := filepath.Dir(targetFile)
	base := filepath.Base(targetFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	prefix := base + ".astrorder-backup-"
	type backupInfo struct {
		path    string
		modTime time.Time
	}
	var backups []backupInfo

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && !e.IsDir() {
			full := filepath.Join(dir, e.Name())
			if fi, err := os.Stat(full); err == nil {
				backups = append(backups, backupInfo{path: full, modTime: fi.ModTime()})
			}
		}
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	if len(backups) > 3 {
		for _, b := range backups[3:] {
			_ = os.Remove(b.path)
		}
	}
}
