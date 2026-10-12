package observe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"time"
)

var (
	hookKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9@._:/-]+$`)
	hookHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// TrustResult reports the outcome of TrustHooks.
type TrustResult struct {
	Hooks   int `json:"hooks"`
	Trusted int `json:"trusted"`
	Changed int `json:"changed"`
}

// TrustHooks asks Codex itself to record trust for Astrorder's hooks. It talks
// to a short-lived `codex app-server`, reads each hook's current hash from
// hooks/list, and writes only the trusted_hash of hooks that are Astrorder's
// (plugin id and exact command match). Anything else stays untrusted, so this
// cannot be used to approve a hook Astrorder did not write.
func TrustHooks(ctx context.Context, codex, home, wantCommand string) (TrustResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, codex, "app-server", "--listen", "stdio://")
	if home != "" {
		command.Env = append(os.Environ(), "CODEX_HOME="+home)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return TrustResult{}, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return TrustResult{}, err
	}
	if err := command.Start(); err != nil {
		return TrustResult{}, fmt.Errorf("start codex app-server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	return trustOverRPC(stdout, stdin, wantCommand)
}

type rpcConn struct {
	reader *bufio.Reader
	writer io.Writer
	next   int
}

func (c *rpcConn) send(message map[string]any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = c.writer.Write(append(encoded, '\n'))
	return err
}

func (c *rpcConn) call(method string, params any) (json.RawMessage, error) {
	c.next++
	id := c.next
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("codex app-server closed during %s: %w", method, err)
		}
		var message struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &message) != nil || message.ID == nil || message.Method != "" || *message.ID != id {
			continue
		}
		if len(message.Error) > 0 && string(message.Error) != "null" {
			return nil, fmt.Errorf("codex %s was rejected", method)
		}
		return message.Result, nil
	}
}

type listedHook struct {
	Key         string `json:"key"`
	PluginID    string `json:"pluginId"`
	Command     string `json:"command"`
	TrustStatus string `json:"trustStatus"`
	CurrentHash string `json:"currentHash"`
}

func (c *rpcConn) ownedHooks(wantCommand string) ([]listedHook, error) {
	raw, err := c.call("hooks/list", map[string]any{"cwds": []string{}})
	if err != nil {
		return nil, err
	}
	var listed struct {
		Data []struct {
			Hooks []listedHook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, errors.New("codex hooks/list returned an invalid result")
	}
	var owned []listedHook
	for _, entry := range listed.Data {
		for _, hook := range entry.Hooks {
			if hook.PluginID == PluginID && hook.Command == wantCommand {
				owned = append(owned, hook)
			}
		}
	}
	return owned, nil
}

func isTrusted(status string) bool { return status == "trusted" || status == "managed" }

func trustOverRPC(reader io.Reader, writer io.Writer, wantCommand string) (TrustResult, error) {
	conn := &rpcConn{reader: bufio.NewReaderSize(reader, 1<<20), writer: writer}
	if _, err := conn.call("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "astrorder-helper", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return TrustResult{}, err
	}
	if err := conn.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return TrustResult{}, err
	}
	hooks, err := conn.ownedHooks(wantCommand)
	if err != nil {
		return TrustResult{}, err
	}
	result := TrustResult{Hooks: len(hooks)}
	if len(hooks) == 0 {
		return result, errors.New("Codex does not list Astrorder hooks with the expected command; restart Codex and retry")
	}
	var edits []map[string]any
	for _, hook := range hooks {
		if isTrusted(hook.TrustStatus) {
			continue
		}
		if !hookKeyPattern.MatchString(hook.Key) || !hookHashPattern.MatchString(hook.CurrentHash) {
			return result, errors.New("Codex returned a hook identity that cannot be trusted safely")
		}
		edits = append(edits, map[string]any{
			"keyPath":       `hooks.state."` + hook.Key + `".trusted_hash`,
			"mergeStrategy": "upsert",
			"value":         hook.CurrentHash,
		})
	}
	if len(edits) > 0 {
		if _, err := conn.call("config/batchWrite", map[string]any{"edits": edits}); err != nil {
			return result, err
		}
		result.Changed = len(edits)
		if hooks, err = conn.ownedHooks(wantCommand); err != nil {
			return result, err
		}
	}
	for _, hook := range hooks {
		if isTrusted(hook.TrustStatus) {
			result.Trusted++
		}
	}
	if result.Trusted != result.Hooks {
		return result, fmt.Errorf("%d of %d Astrorder hooks are still untrusted", result.Hooks-result.Trusted, result.Hooks)
	}
	return result, nil
}
