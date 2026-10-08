package modelconfig

import (
	"context"
	"os"
	"time"
)

func (s *Service) plan(ctx context.Context, request map[string]any) (map[string]any, error) {
	if target, ok := request["target"].(map[string]any); ok && target != nil {
		if kind, _ := target["kind"].(string); kind != "" && kind != "local" {
			payload := map[string]any{"action": "plan"}
			return executeRemoteRequest(ctx, target, payload)
		}
	}

	root, err := s.resolveTargetRoot(request)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"_home":  root,
		"status": "validated",
	}

	allValid := true
	for key, rel := range ManagedFiles {
		fullPath, err := resolveWithinAllowed(s.allowedRoots, root, rel)
		if err != nil {
			return nil, err
		}

		info := s.inspectFile(fullPath, key)
		out[key] = info
		if key == FileHermesConfig && info["exists"] != true {
			continue
		}
		if info["status"] != "validated" {
			allValid = false
		}
	}

	if !allValid {
		out["status"] = "unavailable"
	}

	if rawFiles, ok := request["files"].(map[string]any); ok && rawFiles != nil {
		dryrunChanges := make([]map[string]any, 0, len(rawFiles))
		for name, val := range rawFiles {
			text, ok := val.(string)
			if !ok {
				return nil, newProtocolErrorf("模型配置包含未授权文件")
			}
			rel, ok := ManagedFiles[name]
			if !ok {
				return nil, newProtocolErrorf("模型配置包含未授权文件")
			}
			data := []byte(text)
			if err := validateManagedContent(name, data); err != nil {
				return nil, err
			}
			fullPath, err := resolveWithinAllowed(s.allowedRoots, root, rel)
			if err != nil {
				return nil, err
			}
			currInfo := s.inspectFile(fullPath, name)
			currSHA, _ := currInfo["sha256"].(string)
			newSHA := sha256Hex(data)

			if err := s.checkExpectedDigest(request, name, currSHA); err != nil {
				return nil, err
			}

			dryrunChanges = append(dryrunChanges, map[string]any{
				"file":            name,
				"changed":         currSHA != newSHA,
				"current_sha256":  currSHA,
				"expected_sha256": newSHA,
				"status":          "validated",
			})
		}
		out["changes"] = dryrunChanges
		out["dryrun"] = true
		out["receipt"] = map[string]any{
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"status":    "validated",
			"dryrun":    true,
		}
	}

	return out, nil
}

func (s *Service) inspectFile(fullPath, name string) map[string]any {
	fi, err := os.Stat(fullPath)
	if err != nil || !fi.Mode().IsRegular() {
		return map[string]any{
			"exists":  false,
			"sha256":  EmptySHA256,
			"content": "",
			"status":  "unavailable",
			"valid":   false,
		}
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return map[string]any{
			"exists":  true,
			"sha256":  EmptySHA256,
			"content": "",
			"status":  "unavailable",
			"valid":   false,
		}
	}

	valid := validateManagedContent(name, data) == nil
	status := "validated"
	if !valid {
		status = "unavailable"
	}

	return map[string]any{
		"exists":  true,
		"sha256":  sha256Hex(data),
		"content": string(data),
		"status":  status,
		"valid":   valid,
	}
}

func (s *Service) checkExpectedDigest(request map[string]any, name string, currentSHA string) error {
	if exp, ok := request["expected_digest"].(string); ok && exp != "" {
		if exp != currentSHA {
			return newProtocolErrorf("hash mismatch for %s: expected %s, got %s", name, exp, currentSHA)
		}
	}
	if digests, ok := request["expected_digests"].(map[string]any); ok && digests != nil {
		if exp, ok := digests[name].(string); ok && exp != "" {
			if exp != currentSHA {
				return newProtocolErrorf("hash mismatch for %s: expected %s, got %s", name, exp, currentSHA)
			}
		}
	}
	if digests, ok := request["expected_digests"].(map[string]string); ok && digests != nil {
		if exp, ok := digests[name]; ok && exp != "" {
			if exp != currentSHA {
				return newProtocolErrorf("hash mismatch for %s: expected %s, got %s", name, exp, currentSHA)
			}
		}
	}
	return nil
}
