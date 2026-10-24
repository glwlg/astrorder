package capabilities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/agentskill"
	"astrorder.dev/app-server/internal/capabilities/officialskills"
	"astrorder.dev/app-server/internal/envtarget"
)

// SkillAgent describes an implemented destination adapter, not an installation claim.
type SkillAgent struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func registeredSkillAgents() []SkillAgent {
	return []SkillAgent{{"codex", "OpenAI Codex"}, {"hermes", "Hermes Agent"}, {"grok", "xAI Grok"}}
}
func skillHome(p envtarget.Paths, agent string) string {
	switch agent {
	case "codex":
		return p.Codex
	case "hermes":
		return p.Hermes
	case "grok":
		return p.Grok
	}
	return ""
}
func payloadHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

const officialManifestFile = "astrorder-skill-manifest.json"

const (
	officialManifestOwner   = "astrorder-capabilities"
	officialManifestVersion = 1
)

// errOfficialCLIStale marks a hash-valid CLI that predates the commands this
// catalog installs. It is recoverable: another installed module or the legacy
// install may still provide a current binary. Tampered binaries and unreadable
// files are NOT stale and must abort the delivery.
var errOfficialCLIStale = errors.New("目标 CLI 功能陈旧")

type officialManifest struct {
	CallerHash string            `json:"caller_hash,omitempty"`
	Owner      string            `json:"owner"`
	Version    int               `json:"version"`
	Skill      string            `json:"skill"`
	Agent      string            `json:"agent"`
	Files      map[string]string `json:"files"`
}

func (s *Service) officialSkillDelivery(name string) (func(context.Context, envtarget.Env, string) (string, string, error), error) {
	skill, ok := officialskills.GetOfficialSkill(name)
	if !ok {
		return nil, envtarget.Errorf(404, "星序官方目录没有技能 %s", name)
	}
	return func(ctx context.Context, dest envtarget.Env, agent string) (string, string, error) {
		if err := s.prepareDispatchedCLI(ctx, dest, agent); err != nil {
			return "", "", err
		}
		p, err := dest.Paths(ctx)
		if err != nil {
			return "", "", err
		}
		home := skillHome(p, agent)
		if home == "" {
			return "", "", envtarget.Errorf(422, "尚未接入此 Agent 的技能目录：%s", agent)
		}
		folder := path.Join(home, "skills", skill.Name)
		defer s.lock(dest.Target().ID + "|skills-dispatch|" + home + "/skills")()
		if err := checkOfficialTarget(ctx, dest, home, folder); err != nil {
			return "", "", err
		}
		files := map[string][]byte{"SKILL.md": officialskills.RenderOfficialSkill(*skill, home)}
		if skill.RequiresCLI {
			binName, binary, err := verifiedDestinationCLI(ctx, dest, home, agent)
			if err != nil {
				if errors.Is(err, errOfficialCLIStale) {
					return "", "", envtarget.Errorf(422, "%s", err.Error())
				}
				return "", "", err
			}
			files["scripts/"+binName] = binary
		}
		if err := writeOfficialFiles(ctx, dest, folder, agent, skill.Name, files); err != nil {
			return "", "", err
		}
		message := "技能文件已分发并回读核验；请在目标 Agent 中加载"
		if skill.RequiresCLI {
			message = "技能文件与目标 CLI 已分发并回读核验；请在目标 Agent 中加载"
		}
		return folder, message, nil
	}, nil
}

// Reuse only a CLI and caller binding already provisioned on the destination.
// No executable, caller, token or native session id is copied from the source.
func verifiedDestinationCLI(ctx context.Context, dest envtarget.Env, home, agent string) (string, []byte, error) {
	callerPath := path.Join(home, agentskill.CallerFile)
	if err := checkOfficialTarget(ctx, dest, home, callerPath); err != nil {
		return "", nil, err
	}
	raw, err := dest.Read(ctx, callerPath)
	if err != nil {
		return "", nil, envtarget.Errorf(422, "目标 %s 尚未配置会话 CLI 身份绑定，请先在该 Agent 配置星序会话 CLI", agent)
	}
	var caller map[string]any
	if json.Unmarshal(raw, &caller) != nil || len(caller) != 4 {
		return "", nil, envtarget.Errorf(422, "目标身份绑定必须仅含四个静态字段")
	}
	for _, key := range []string{"agent_id", "connection_id", "profile_name", "runtime"} {
		v, ok := caller[key].(string)
		if !ok || v == "" || strings.ContainsAny(v, " \t\r\n") {
			return "", nil, envtarget.Errorf(422, "目标身份绑定字段无效：%s", key)
		}
	}
	if caller["runtime"] != agent {
		return "", nil, envtarget.Errorf(422, "目标身份绑定运行时与所选 Agent 不一致")
	}
	conn := dest.Target().ConnectionID
	if conn == "" {
		conn = "local"
	}
	if caller["connection_id"] != conn {
		return "", nil, envtarget.Errorf(422, "目标身份绑定与所选机器不一致")
	}
	var staleErr error
	// Reuse a CLI already installed and verified for any official module on this
	// destination. Every module is self-contained, so a destination that deleted
	// the legacy combined directory (or only installed one module) still works.
	for _, installed := range officialskills.ListOfficialSkills() {
		module := path.Join(home, "skills", installed.Name)
		manifestPath := path.Join(module, officialManifestFile)
		if err := checkOfficialTarget(ctx, dest, home, manifestPath); err != nil {
			return "", nil, err
		}
		ownRaw, readErr := dest.Read(ctx, manifestPath)
		if readErr != nil {
			if errors.Is(readErr, envtarget.ErrNotExist) {
				continue
			}
			return "", nil, readErr
		}
		var own officialManifest
		if json.Unmarshal(ownRaw, &own) != nil || own.Owner != officialManifestOwner || own.Version != officialManifestVersion || own.Skill != installed.Name || own.Agent != agent || own.CallerHash != payloadHash(raw) {
			// An unmanaged, foreign or caller-mismatched module is not a CLI source.
			continue
		}
		for _, name := range []string{"astrorder-agent.exe", "astrorder-agent"} {
			rel := "scripts/" + name
			if expected := own.Files[rel]; expected != "" {
				binName, binary, err := readVerifiedCLI(ctx, dest, home, path.Join(module, rel), name, expected)
				if err == nil {
					return binName, binary, nil
				}
				if errors.Is(err, errOfficialCLIStale) {
					// Hash was valid and caller matched, but the binary is older.
					// Keep searching other official modules and the legacy install
					// so a user who upgraded astrorderctl can still refresh.
					if staleErr == nil {
						staleErr = err
					}
					continue
				}
				// Tampering, read failures and OS exec errors abort immediately.
				return "", nil, err
			}
		}
	}
	legacy := path.Join(home, "skills/astrorder")
	if err := checkOfficialTarget(ctx, dest, home, path.Join(legacy, agentskill.ManifestFile)); err != nil {
		return "", nil, err
	}
	manifestRaw, err := dest.Read(ctx, path.Join(legacy, agentskill.ManifestFile))
	if err != nil {
		if errors.Is(err, envtarget.ErrNotExist) && staleErr != nil {
			return "", nil, staleErr
		}
		return "", nil, envtarget.Errorf(422, "目标尚无已验证的星序会话 CLI 安装")
	}
	var manifest agentskill.Manifest
	if json.Unmarshal(manifestRaw, &manifest) != nil || manifest.Owner != agentskill.ManifestOwner || manifest.FormatVersion != agentskill.ManifestVersion || manifest.Runtime != agent || manifest.AgentID != caller["agent_id"] || manifest.ConnectionID != caller["connection_id"] || manifest.ProfileName != caller["profile_name"] {
		return "", nil, envtarget.Errorf(422, "目标 CLI 安装清单与身份绑定不匹配")
	}
	if manifest.FileHashes[agentskill.CallerFile] != payloadHash(raw) {
		return "", nil, envtarget.Errorf(422, "目标身份绑定与安装清单哈希不匹配")
	}
	for _, name := range []string{"astrorder-agent.exe", "astrorder-agent"} {
		rel := "scripts/" + name
		if expected := manifest.FileHashes[rel]; expected != "" {
			binName, binary, err := readVerifiedCLI(ctx, dest, home, path.Join(legacy, rel), name, expected)
			if err == nil {
				return binName, binary, nil
			}
			return "", nil, err
		}
	}
	if staleErr != nil {
		return "", nil, staleErr
	}
	return "", nil, envtarget.Errorf(422, "目标安装清单缺少 CLI")
}

func readVerifiedCLI(ctx context.Context, dest envtarget.Env, home, binaryPath, binName, expected string) (string, []byte, error) {
	if err := checkOfficialTarget(ctx, dest, home, binaryPath); err != nil {
		return "", nil, err
	}
	binary, err := dest.Read(ctx, binaryPath)
	if err != nil || payloadHash(binary) != expected {
		return "", nil, envtarget.Errorf(422, "目标 CLI 不存在或哈希校验失败")
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := dest.Run(bounded, envtarget.Command{Name: binaryPath, Args: []string{"--help"}})
	if err != nil || result.Code != 0 {
		return "", nil, envtarget.Errorf(422, "目标 CLI 无法在此机器执行")
	}
	// Answering --help is not proof that the binary implements the surfaces this
	// catalog installs. Probe the exact subcommands so an older CLI is rejected
	// instead of being distributed with documentation it cannot serve.
	for _, probe := range []struct {
		args  []string
		want  string
		label string
	}{
		{[]string{"invoke", "--help"}, "--input-file", "invoke --input-file"},
		{[]string{"browser", "eval", "--help"}, "--script-file", "browser eval --script-file"},
	} {
		probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
		probeResult, probeErr := dest.Run(probeCtx, envtarget.Command{Name: binaryPath, Args: probe.args})
		probeCancel()
		if probeErr != nil || probeResult.Code != 0 || !strings.Contains(probeResult.Output(), probe.want) {
			// The binary is hash-valid and runnable but lacks a required surface.
			// This is recoverable by another module or the legacy install.
			return "", nil, fmt.Errorf("%w：不支持 %s，请先升级星序会话 CLI", errOfficialCLIStale, probe.label)
		}
	}
	return binName, binary, nil
}

// Refuse symlink/junction destinations. Remote checks receive paths as arguments.
func checkOfficialTarget(ctx context.Context, dest envtarget.Env, home, target string) error {
	if dest.IsLocal() {
		base := filepath.Clean(filepath.FromSlash(home))
		current := filepath.Clean(filepath.FromSlash(target))
		rel, err := filepath.Rel(base, current)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return envtarget.Errorf(422, "技能路径超出目标 Agent 目录")
		}
		for {
			info, err := os.Lstat(current)
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return envtarget.Errorf(422, "目标技能路径不允许符号链接")
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if current == base {
				break
			}
			next := filepath.Dir(current)
			if next == current {
				return envtarget.Errorf(422, "无法核验目标路径")
			}
			current = next
		}
		return nil
	}
	result, err := dest.Shell(ctx, `base=$1; p=$2; case "$p" in "$base"/*) ;; *) exit 4;; esac; while :; do [ ! -L "$p" ] || exit 5; [ "$p" != "$base" ] || break; p=$(dirname -- "$p"); [ "$p" != / ] || exit 4; done`, nil, home, target)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return envtarget.Errorf(422, "目标技能路径不可验证或包含符号链接")
	}
	return nil
}

func writeOfficialFiles(ctx context.Context, dest envtarget.Env, folder, agent, name string, files map[string][]byte, verify ...func() error) (resultErr error) {
	manifestPath := path.Join(folder, officialManifestFile)
	if err := checkOfficialTarget(ctx, dest, path.Dir(path.Dir(folder)), manifestPath); err != nil {
		return err
	}
	oldRaw, err := dest.Read(ctx, manifestPath)
	hasManifest := err == nil
	if err != nil && !errors.Is(err, envtarget.ErrNotExist) {
		return err
	}
	var old officialManifest
	if hasManifest {
		if json.Unmarshal(oldRaw, &old) != nil || old.Owner != officialManifestOwner || old.Version != officialManifestVersion || old.Skill != name || old.Agent != agent {
			return envtarget.Errorf(409, "目标技能清单无效或绑定冲突，已保留原文件")
		}
	}
	type backup struct {
		data   []byte
		exists bool
	}
	backups := map[string]backup{}
	keys := make([]string, 0, len(files))
	for rel := range files {
		keys = append(keys, rel)
	}
	sort.Strings(keys)
	for _, rel := range keys {
		full := path.Join(folder, rel)
		if err := checkOfficialTarget(ctx, dest, path.Dir(path.Dir(folder)), full); err != nil {
			return err
		}
		before, readErr := dest.Read(ctx, full)
		if readErr != nil && !errors.Is(readErr, envtarget.ErrNotExist) {
			return readErr
		}
		exists := readErr == nil
		if exists {
			// A file that exists but is untracked (or no longer matches its
			// recorded hash) belongs to the user or was edited: never overwrite.
			if !hasManifest || old.Files[rel] != payloadHash(before) {
				return envtarget.Errorf(409, "目标技能为用户文件或已修改，拒绝覆盖：%s", rel)
			}
		} else if hasManifest {
			// A tracked file with no recorded hash is damaged state, not a
			// legitimate upgrade. Untracked new files are allowed so a valid
			// document-only install can gain its CLI and manifest safely.
			if tracked, ok := old.Files[rel]; ok && tracked == "" {
				return envtarget.Errorf(409, "目标技能安装不完整，已保留原文件")
			}
		}
		backups[rel] = backup{before, exists}
	}
	if !hasManifest && dest.IsDir(ctx, folder) {
		entries, err := dest.ListDir(ctx, folder)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return envtarget.Errorf(409, "目标目录含用户文件，拒绝覆盖")
		}
	}
	backups[officialManifestFile] = backup{oldRaw, hasManifest}
	managed := officialManifest{Owner: officialManifestOwner, Version: officialManifestVersion, Skill: name, Agent: agent, Files: map[string]string{}}
	// Every official module is bound to the destination caller, not only the
	// session module. The binding was verified before any file was written.
	caller, err := dest.Read(ctx, path.Join(path.Dir(path.Dir(folder)), agentskill.CallerFile))
	if err != nil {
		return envtarget.Errorf(422, "目标身份绑定在分发期间不可读")
	}
	managed.CallerHash = payloadHash(caller)
	for rel, data := range files {
		managed.Files[rel] = payloadHash(data)
	}
	raw, err := json.MarshalIndent(managed, "", "  ")
	if err != nil {
		return err
	}
	payload := make(map[string][]byte, len(files)+1)
	for rel, data := range files {
		payload[rel] = data
	}
	payload[officialManifestFile] = append(raw, '\n')
	keys = append(keys, officialManifestFile)
	written := []string{}
	defer func() {
		if resultErr == nil {
			return
		}
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		for i := len(written) - 1; i >= 0; i-- {
			rel := written[i]
			b := backups[rel]
			full := path.Join(folder, rel)
			var err error
			if b.exists {
				mode := fs.FileMode(0644)
				if strings.HasPrefix(rel, "scripts/") {
					mode = 0755
				}
				err = dest.Write(restoreCtx, full, b.data, mode)
			} else {
				err = dest.Remove(restoreCtx, full)
			}
			if err != nil {
				resultErr = fmt.Errorf("%w；回滚失败：%s", resultErr, rel)
			}
		}
	}()
	for _, rel := range keys {
		full := path.Join(folder, rel)
		if err := checkOfficialTarget(ctx, dest, path.Dir(path.Dir(folder)), full); err != nil {
			return err
		}
		written = append(written, rel)
		mode := fs.FileMode(0644)
		if strings.HasPrefix(rel, "scripts/") {
			mode = 0755
		}
		if err := dest.Write(ctx, full, payload[rel], mode); err != nil {
			return err
		}
		stored, err := dest.Read(ctx, full)
		if err != nil || !bytes.Equal(stored, payload[rel]) {
			return envtarget.Errorf(502, "目标文件回读不一致，已尝试还原：%s", rel)
		}
	}
	for _, check := range verify {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}
