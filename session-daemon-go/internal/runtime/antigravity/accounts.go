package antigravity

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"astrorder.dev/contracts/agycredential"
)

const agyTokenName = "antigravity-oauth-token"

var agyAccountFiles sync.Mutex

func credentialField(field any) ([]byte, error) {
	switch value := field.(type) {
	case nil:
		return nil, nil
	case []byte:
		return value, nil
	case string:
		if len(value) <= base64.StdEncoding.EncodedLen(agycredential.MaxBytes) {
			if raw, err := base64.StdEncoding.DecodeString(value); err == nil {
				return raw, nil
			}
		}
	}
	return nil, errors.New("AGY 凭证格式无效")
}

func readAgyAccountFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > agycredential.MaxBytes {
		return nil, errors.New("invalid AGY account file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, agycredential.MaxBytes+1))
	if err != nil || len(data) > agycredential.MaxBytes {
		return nil, errors.New("无法读取 AGY 账号凭证")
	}
	return data, nil
}

func writeAgyAccountFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".agy-account-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (a *Adapter) queryAgyAccounts(ctx context.Context, method, id string, token []byte) (map[string]any, error) {
	var result map[string]any
	var err error
	if a.sshStarter != nil {
		if a.remoteHelper == nil {
			return nil, errors.New("远端 AGY 账号管理组件不可用")
		}
		result, err = a.remoteHelper(ctx, map[string]any{
			"domain": "antigravity", "action": method, "account_id": id, "credential": token, "executable": a.config.Executable,
		})
		if err == nil && method == "account.vault.capture" {
			result["credential"], err = credentialField(result["credential"])
		}
	} else {
		result, err = executeAgyAccount(ctx, a.config.Home, a.config.Executable, method, id, token)
	}
	if err == nil && method == "account.vault.apply" {
		a.quotaMu.Lock()
		a.quotaCache = nil
		a.tierInfo = nil
		a.quotaCachedAt = time.Time{}
		a.quotaFailedAt = time.Time{}
		a.quotaMu.Unlock()
	}
	return result, err
}

func executeAgyAccount(ctx context.Context, home, executable, method, id string, token []byte) (map[string]any, error) {
	if !filepath.IsAbs(home) {
		return nil, errors.New("无法确定 AGY 用户目录")
	}
	result, quotaToken, err := agyAccountOperation(ctx, home, method, id, token)
	if err != nil || method != "account.vault.quota" {
		return result, err
	}
	return readIsolatedAgyQuota(ctx, home, executable, quotaToken)
}

func agyAccountOperation(ctx context.Context, home, method, id string, token []byte) (map[string]any, []byte, error) {
	agyAccountFiles.Lock()
	defer agyAccountFiles.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	livePath := filepath.Join(home, ".gemini", "antigravity-cli", agyTokenName)
	// A saved account's quota does not depend on the target's login state.
	// If this is the active account, its live copy may have been refreshed.
	live, err := readAgyAccountFile(livePath)
	if err != nil && !os.IsNotExist(err) && !(method == "account.vault.quota" && id != "current") {
		return nil, nil, errors.New("无法读取当前 AGY 凭证")
	}
	current, loggedIn := agycredential.Identity(live)

	switch method {
	case "account.vault.state":
		return map[string]any{"identity": current, "quota_supported": runtime.GOOS == "linux"}, nil, nil
	case "account.vault.capture":
		if !loggedIn {
			return nil, nil, errors.New("目标环境没有有效的 AGY 登录凭证")
		}
		return map[string]any{"credential": live}, nil, nil
	case "account.vault.quota":
		if id == "current" {
			if !loggedIn {
				return nil, nil, errors.New("目标环境没有有效的 AGY 登录凭证")
			}
			return nil, live, nil
		}
		identity, valid := agycredential.Identity(token)
		if !valid {
			return nil, nil, errors.New("AGY 凭证格式无效")
		}
		if loggedIn && identity == current {
			return nil, live, nil
		}
		return nil, token, nil
	case "account.vault.check", "account.vault.apply":
		if _, valid := agycredential.Identity(token); !valid {
			return nil, nil, errors.New("AGY 凭证格式无效")
		}
		if method == "account.vault.check" {
			return map[string]any{"ready": true}, nil, nil
		}
		if err := os.MkdirAll(filepath.Dir(livePath), 0700); err != nil {
			return nil, nil, errors.New("无法创建目标 AGY 配置目录")
		}
		if err := writeAgyAccountFile(livePath, token); err != nil {
			return nil, nil, errors.New("无法替换目标 AGY 凭证")
		}
		return map[string]any{"account_id": id}, nil, nil
	default:
		return nil, nil, errors.New("不支持的 AGY 账号操作")
	}
}

func readIsolatedAgyQuota(ctx context.Context, home, executable string, token []byte) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("此系统暂不支持隔离查询 AGY 额度")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, errors.New("目标环境需要安装 bubblewrap 才能查询 AGY 账号额度")
	}
	agy, err := exec.LookPath(executable)
	if err != nil || !filepath.IsAbs(agy) {
		return nil, errors.New("未找到目标环境的 AGY 命令")
	}
	profile, err := os.MkdirTemp("/tmp", "astrorder-agy-profile-")
	if err != nil {
		return nil, errors.New("无法创建 AGY 隔离目录")
	}
	defer os.RemoveAll(profile)
	tokenDir := filepath.Join(profile, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		return nil, errors.New("无法创建 AGY 隔离目录")
	}
	if err := os.WriteFile(filepath.Join(tokenDir, agyTokenName), token, 0600); err != nil {
		return nil, errors.New("无法写入隔离凭证")
	}

	ctx, cancel := context.WithTimeout(ctx, quotaCommandTimeout)
	defer cancel()
	// Keep absolute executable paths intact. The host home is read-only and its
	// AGY configuration is hidden even if the CLI ignores the HOME override.
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, errors.New("无法定位 AGY 默认配置目录")
	}
	const sandboxHome = "/run/astrorder-profile"
	args := []string{"--die-with-parent", "--unshare-pid", "--ro-bind", "/", "/",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/run",
		"--bind", profile, sandboxHome}
	configPath := filepath.Join(home, ".gemini")
	if _, err := os.Stat(configPath); err == nil {
		args = append(args, "--bind", filepath.Join(profile, ".gemini"), configPath)
	} else if !os.IsNotExist(err) {
		return nil, errors.New("无法检查 AGY 默认配置目录")
	}
	args = append(args,
		"--tmpfs", "/tmp", "--clearenv", "--setenv", "HOME", sandboxHome, "--setenv", "PATH", "/usr/bin:/bin",
		"--chdir", sandboxHome, "--", agy)
	cmd := exec.CommandContext(ctx, bwrap, append(slices.Clone(args), "-p", "/usage", "--output-format", "json")...)
	cmd.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("隔离额度查询失败，请检查 AGY 登录状态和 bubblewrap")
	}
	groups, _, err := parseUsageCLIOutput(output.Bytes())
	if err != nil {
		return nil, errors.New("AGY 未返回可用的额度数据")
	}
	groups = slices.DeleteFunc(groups, func(g map[string]any) bool { return len(g["buckets"].([]map[string]any)) == 0 })
	if len(groups) == 0 {
		return nil, errors.New("AGY 未返回可用的额度数据，请检查该账号登录状态")
	}
	out := map[string]any{"groups": groups, "as_of": time.Now().UTC().Format(time.RFC3339)}
	// /usage has quota buckets. The subscription belongs to the ACP session
	// opened with this same isolated credential, including for inactive accounts.
	tier, err := isolatedSubscriptionTier(ctx, bwrap, args, sandboxHome)
	if err == nil {
		applyTierMetadata(out, tier)
	}
	return out, nil
}

func isolatedSubscriptionTier(ctx context.Context, executable string, args []string, cwd string) (map[string]any, error) {
	adapter := New(Config{Executable: executable, Arguments: args})
	client, err := adapter.startACPClient(ctx, "")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.forceClose()
		<-client.done
	}()
	session, err := client.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	if err != nil {
		return nil, err
	}
	meta, _ := session["_meta"].(map[string]any)
	agy, _ := meta["antigravity"].(map[string]any)
	tier, _ := agy["tier"].(map[string]any)
	return tier, nil
}
