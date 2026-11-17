package botgroups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const artifactIndexVersion = 6

type ArtifactFileResolver interface {
	Resolve(context.Context, targetfs.ResolveRequest) (targetfs.Resolved, error)
}

type fileIndex struct {
	Version    int              `json:"version"`
	Generation string           `json:"generation"`
	Cursor     string           `json:"cursor"`
	References []artifacts.File `json:"references"`
	Claims     []artifacts.File `json:"claims,omitempty"`
	Errors     []string         `json:"errors,omitempty"`
}

type groupFiles struct {
	Items      []artifacts.File `json:"items"`
	References []artifacts.File `json:"references"`
	Checking   bool             `json:"checking"`
	Error      string           `json:"error,omitempty"`
}

func fileIndexKey(id string) string { return "bot_group_files:" + id }

func readFileIndexTx(ctx context.Context, tx *sql.Tx, id string) (fileIndex, error) {
	index := fileIndex{References: []artifacts.File{}}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, fileIndexKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return index, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &index)
	}
	return index, err
}

func artifactEventTx(ctx context.Context, tx *sql.Tx, groupID string) (store.Event, error) {
	directory, err := artifacts.ReadTx(ctx, tx, groupID)
	if err != nil {
		return store.Event{}, err
	}
	return appendEventTx(ctx, tx, "blackboard.change", map[string]any{"namespace": "group:" + groupID, "key": artifacts.Key, "action": "set", "value": directory})
}

// Invalidate the guess-based catalog before reading remote history. Missing
// evidence must not leave the old false positives visible.
func (s *Service) initializeFileIndex(ctx context.Context, id string) error {
	var events []store.Event
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := readGroupTx(ctx, tx, id); err != nil {
			return err
		}
		index, err := readFileIndexTx(ctx, tx, id)
		if err != nil || index.Version == artifactIndexVersion {
			return err
		}
		generation, err := randomID("files-")
		if err != nil {
			return err
		}
		index = fileIndex{Version: artifactIndexVersion, Generation: generation, References: []artifacts.File{}}
		if err := putTx(ctx, tx, fileIndexKey(id), index); err != nil {
			return err
		}
		if err := artifacts.WriteTx(ctx, tx, id, artifacts.Directory{Items: []artifacts.File{}}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=?`, "bot_group_artifacts_cursor:"+id); err != nil {
			return err
		}
		event, err := artifactEventTx(ctx, tx, id)
		if err == nil {
			events = append(events, event)
		}
		return err
	})
	if err == nil && len(events) > 0 {
		s.options.Publisher.PublishCommitted(events)
	}
	return err
}

// One worker per group, independent of submissionMu. Reads, model routing and
// member completion never wait for remote file validation.
func (s *Service) scheduleArtifacts(id string) {
	if s.ctx == nil || s.options.FileResolver == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.artifactWorkers == nil {
		s.artifactWorkers = make(map[string]bool)
		s.artifactRequested = make(map[string]bool)
	}
	s.artifactRequested[id] = true
	if s.artifactWorkers[id] {
		return
	}
	s.artifactWorkers[id] = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			s.mu.Lock()
			delete(s.artifactRequested, id)
			s.mu.Unlock()
			if err := s.indexGroupArtifacts(s.ctx, id); err != nil && !errors.Is(err, errMissing) && s.ctx.Err() == nil {
				slog.Warn("group file validation failed", "group_id", id, "error", err)
			}
			s.mu.Lock()
			if s.closed || !s.artifactRequested[id] {
				delete(s.artifactWorkers, id)
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
		}
	}()
}

func (s *Service) indexGroupArtifacts(ctx context.Context, id string) error {
	if err := s.initializeFileIndex(ctx, id); err != nil {
		return err
	}
	messages, err := s.storedMessages(ctx, id)
	if err != nil {
		return err
	}
	var index fileIndex
	var directory artifacts.Directory
	err = s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		index, err = readFileIndexTx(ctx, tx, id)
		if err == nil {
			directory, err = artifacts.ReadTx(ctx, tx, id)
		}
		return err
	})
	if err != nil {
		return err
	}
	start := 0
	if index.Cursor != "" {
		found := false
		for i, message := range messages {
			if message.ID == index.Cursor {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return errors.New("群聊文件核验游标失效")
		}
	}
	request := ""
	for i, message := range messages {
		if message.SenderType == "user" {
			request = message.Text
		}
		if i < start {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		before := append([]artifacts.File{}, index.References...)
		oldItems := append([]artifacts.File{}, directory.Items...)
		if message.SenderType == "agent" && message.SessionID != "" && len(message.NativeIDs) > 0 {
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			checkErr := s.inspectMessageFiles(checkCtx, message, request, &index, &directory)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if checkErr != nil {
				index.Errors = append(index.Errors, fmt.Sprintf("「%s」：%s", message.SenderName, checkErr.Error()))
			}
		}
		previousCursor := index.Cursor
		index.Cursor = message.ID
		var events []store.Event
		err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := readGroupTx(ctx, tx, id); err != nil {
				return err
			}
			current, err := readFileIndexTx(ctx, tx, id)
			if err != nil {
				return err
			}
			// A reset/deletion during validation must not resurrect data.
			if current.Version != artifactIndexVersion || current.Generation != index.Generation || current.Cursor != previousCursor {
				return errors.New("群聊文件核验已重置")
			}
			if err := putTx(ctx, tx, fileIndexKey(id), index); err != nil {
				return err
			}
			if err := artifacts.WriteTx(ctx, tx, id, directory); err != nil {
				return err
			}
			if !reflect.DeepEqual(before, index.References) || !reflect.DeepEqual(oldItems, directory.Items) || len(index.Errors) != len(current.Errors) {
				event, err := artifactEventTx(ctx, tx, id)
				if err != nil {
					return err
				}
				events = append(events, event)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if len(events) > 0 {
			s.options.Publisher.PublishCommitted(events)
		}
	}
	return nil
}

func (s *Service) inspectMessageFiles(ctx context.Context, message Message, request string, index *fileIndex, directory *artifacts.Directory) error {
	if s.options.FileResolver == nil {
		return errors.New("文件核验服务不可用")
	}
	reader, ok := s.options.Controller.(replyReader)
	if !ok {
		return errors.New("原生群会话历史不可用")
	}
	text, err := reader.ReadReply(ctx, message.SenderID, message.SessionID, message.NativeIDs)
	if err != nil {
		return err
	}
	paths := artifacts.Candidates(text)
	if len(paths) == 0 {
		return nil
	}
	session, err := s.options.Store.GetSession(ctx, message.SenderID, message.SessionID)
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("群会话的文件来源记录不存在")
	}
	workspace, _ := session["workspace"].(string)
	connection, _ := session["connection_id"].(string)
	if !artifacts.Absolute(artifacts.Normalize(workspace)) {
		return errors.New("群会话未记录有效的工作空间")
	}
	agent, err := s.options.Store.GetAgent(ctx, message.SenderID)
	if err != nil {
		return err
	}
	if agent == nil {
		return errors.New("群会话的 Agent 来源记录不存在")
	}
	agentConnection, _ := agent["connection_id"].(string)
	if connectionID(agentConnection) != connectionID(connection) {
		return errors.New("群会话与 Agent 的来源连接不一致")
	}
	source := artifacts.File{GroupID: message.GroupID, MemberID: message.SenderMemberID, MemberName: message.SenderName,
		AgentID: message.SenderID, SessionID: message.SessionID, Workspace: workspace, ConnectionID: connectionID(connection), CreatedAt: message.CreatedAt}
	candidates := []ArtifactCandidate{}
	seen := map[string]bool{}
	var failures []error
	for _, raw := range paths {
		file := artifacts.Reference(source, raw)
		existing := artifacts.Find(artifacts.Directory{Items: index.References}, raw)
		if existing == nil {
			existing = artifacts.Find(artifacts.Directory{Items: index.References}, file.Path)
		}
		if existing == nil {
			existing = artifacts.Find(artifacts.Directory{Items: index.Claims}, raw)
		}
		if existing == nil {
			existing = artifacts.Find(artifacts.Directory{Items: index.Claims}, file.Path)
		}
		if existing != nil {
			// Forwarded paths keep the first source, even for equal home paths.
			if existing.AgentID != source.AgentID || existing.SessionID != source.SessionID {
				continue
			}
			file = *existing
		}
		if seen[file.ID] {
			continue
		}
		seen[file.ID] = true
		target, err := s.options.FileResolver.Resolve(ctx, targetfs.ResolveRequest{Workspace: parentPath(file.Path), ConnectionID: file.ConnectionID})
		if err != nil {
			if targetfs.IsNotFound(err) || errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if existing == nil {
				index.Claims = append(index.Claims, file)
			}
			failures = append(failures, fmt.Errorf("核验 %s 失败：%w", file.Name, err))
			continue
		}
		info, err := target.FileSystem.Stat(ctx, file.Path)
		if err != nil {
			if !targetfs.IsNotFound(err) && !errors.Is(err, fs.ErrNotExist) {
				if existing == nil {
					index.Claims = append(index.Claims, file)
				}
				failures = append(failures, fmt.Errorf("核验 %s 失败：%w", file.Name, err))
			}
			continue
		}
		if info.IsDir || !info.Mode.IsRegular() {
			continue
		}
		if artifacts.Find(artifacts.Directory{Items: index.References}, file.Path) == nil {
			index.References = append(index.References, file)
		}
		if artifacts.Find(*directory, file.Path) == nil {
			candidates = append(candidates, ArtifactCandidate{File: file})
		}
	}
	if len(candidates) == 0 {
		return errors.Join(failures...)
	}
	evidenceReader, ok := s.options.Controller.(artifactEvidenceReader)
	if !ok {
		return errors.Join(append(failures, errors.New("原生文件变更记录不可用，已保留文件引用"))...)
	}
	operations, err := evidenceReader.ReadArtifactEvidence(ctx, message.SenderID, message.SessionID, message.NativeIDs)
	if err != nil {
		return errors.Join(append(failures, fmt.Errorf("原生文件变更核验失败：%w", err))...)
	}
	eligible := []ArtifactCandidate{}
	for _, candidate := range candidates {
		candidate.Operations = evidenceForFile(candidate.File, operations)
		if len(candidate.Operations) > 0 {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		return errors.Join(failures...)
	}
	if s.options.ArtifactDecider == nil {
		return errors.New("System One 产物核验不可用")
	}
	decisions, err := s.options.ArtifactDecider.ClassifyArtifacts(ctx, ArtifactInput{Request: request, Reply: text, Candidates: eligible})
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	// Validate every selection before publishing any of this batch.
	for id, evidenceID := range decisions {
		valid := false
		for _, candidate := range eligible {
			if candidate.File.ID == id {
				for _, op := range candidate.Operations {
					valid = valid || op.ID == evidenceID
				}
			}
		}
		if !valid {
			return errors.New("System One 返回了无效的产物变更依据")
		}
	}
	for _, candidate := range eligible {
		evidenceID, deliver := decisions[candidate.File.ID]
		if !deliver {
			continue
		}
		file := candidate.File
		file.EvidenceID, file.ContentKey = evidenceID, artifacts.ContentPrefix+file.ID
		directory.Items = append(directory.Items, file)
		for i := range index.References {
			if index.References[i].ID == file.ID {
				index.References[i] = file
			}
		}
	}
	return errors.Join(failures...)
}

func parentPath(p string) string {
	at := strings.LastIndex(p, "/")
	if at <= 0 {
		return "/"
	}
	parent := p[:at]
	if len(parent) == 2 && parent[1] == ':' {
		parent += "/"
	}
	return parent
}

func (s *Service) artifactDirectory(ctx context.Context, id string) (groupFiles, error) {
	if err := s.initializeFileIndex(ctx, id); err != nil {
		return groupFiles{}, err
	}
	result := groupFiles{Items: []artifacts.File{}, References: []artifacts.File{}}
	cursor := ""
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		index, err := readFileIndexTx(ctx, tx, id)
		if err != nil {
			return err
		}
		directory, err := artifacts.ReadTx(ctx, tx, id)
		cursor = index.Cursor
		result.Items, result.References, result.Error = directory.Items, index.References, strings.Join(index.Errors, "\n")
		return err
	})
	if err != nil {
		return result, err
	}
	messages, err := s.storedMessages(ctx, id)
	if err != nil {
		return result, err
	}
	if len(messages) > 0 && messages[len(messages)-1].ID != cursor {
		s.scheduleArtifacts(id)
	}
	s.mu.Lock()
	result.Checking = s.artifactWorkers[id]
	s.mu.Unlock()
	return result, err
}

func (s *Service) recheckArtifacts(ctx context.Context, id string) (groupFiles, error) {
	if err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := readGroupTx(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=?`, fileIndexKey(id))
		return err
	}); err != nil {
		return groupFiles{}, err
	}
	return s.artifactDirectory(ctx, id)
}
