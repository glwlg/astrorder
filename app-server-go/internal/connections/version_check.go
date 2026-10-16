package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type VersionCheck struct {
	Current   string
	Latest    string
	HasUpdate bool
	Error     string
}

type UpgradeVersionInspector interface {
	CheckUpgrade(context.Context, string, string, map[string]any) VersionCheck
}

func (e *NativeVersionExecutor) CheckUpgrade(ctx context.Context, kind, connID string, conn map[string]any) VersionCheck {
	if kind == "grok" {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var cmd *exec.Cmd
		if connID != "" && connID != "local" {
			target, args, err := sshProbeTarget(conn)
			if err != nil {
				return VersionCheck{Error: err.Error()}
			}
			binary := e.SSHBinary
			if binary == "" {
				binary = "ssh"
			}
			cmd = exec.CommandContext(ctx, binary, append(args, target, remoteAgentSetup(kind)+`exec "$p" update --check --json`)...)
		} else {
			binary, err := localAgentExecutable(ctx, kind)
			if err != nil {
				return VersionCheck{Error: err.Error()}
			}
			cmd = exec.CommandContext(ctx, binary, "update", "--check", "--json")
		}
		configureChildProcess(cmd)
		out, err := cmd.Output()
		if err != nil {
			return VersionCheck{Error: err.Error()}
		}
		return parseGrokVersionCheck(out)
	}
	current, upToDate, err := e.GetVersion(ctx, kind, connID, conn)
	if err != nil {
		return VersionCheck{Error: err.Error()}
	}
	result := VersionCheck{Current: current}
	if kind == "codex" {
		latest, err := fetchCodexLatest(ctx, http.DefaultClient, "https://registry.npmjs.org/@openai/codex/latest")
		if err != nil {
			result.Error = err.Error()
			return result
		}
		return compareVersionCheck(current, latest)
	}
	if upToDate {
		result.Latest = current
	} else {
		result.Error = "原生版本输出未确认更新状态"
	}
	return result
}

func fetchCodexLatest(ctx context.Context, client *http.Client, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询 Codex 发布版本: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询 Codex 发布版本: HTTP %d", response.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if _, _, ok := splitVersion(body.Version); !ok {
		return "", fmt.Errorf("无效的 Codex 发布版本 %q", body.Version)
	}
	return body.Version, nil
}

func parseGrokVersionCheck(out []byte) VersionCheck {
	var body struct {
		Current   string `json:"currentVersion"`
		Latest    string `json:"latestVersion"`
		Available *bool  `json:"updateAvailable"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return VersionCheck{Error: "无法解析 Grok 原生更新检查: " + err.Error()}
	}
	result := compareVersionCheck(body.Current, body.Latest)
	if body.Error != "" {
		result.Error = body.Error
		result.HasUpdate = false
	}
	if body.Available == nil {
		result.Error = "Grok 未返回更新检查结果"
		result.HasUpdate = false
	}
	if body.Available != nil && !*body.Available {
		result.HasUpdate = false
	}
	return result
}

func splitVersion(value string) ([]int, string, bool) {
	value = strings.TrimPrefix(value, "v")
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(value, "-", 2)
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return nil, "", false
	}
	core := make([]int, 3)
	for i, n := range numbers {
		v, err := strconv.Atoi(n)
		if err != nil || v < 0 {
			return nil, "", false
		}
		core[i] = v
	}
	pre := ""
	if len(parts) == 2 {
		pre = parts[1]
		if pre == "" {
			return nil, "", false
		}
	}
	return core, pre, true
}

func compareVersionCheck(current, latest string) VersionCheck {
	result := VersionCheck{Current: current, Latest: latest}
	a, ap, ok := splitVersion(current)
	b, bp, bok := splitVersion(latest)
	if !ok || !bok {
		result.Error = "无法比较实际版本号"
		return result
	}
	for i := range a {
		if a[i] != b[i] {
			result.HasUpdate = a[i] < b[i]
			return result
		}
	}
	if ap == bp || bp != "" && ap == "" {
		return result
	}
	if bp == "" {
		result.HasUpdate = true
		return result
	}
	aa, bb := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] == bb[i] {
			continue
		}
		an, ae := strconv.Atoi(aa[i])
		bn, be := strconv.Atoi(bb[i])
		if ae == nil && be == nil {
			result.HasUpdate = an < bn
		} else if ae == nil {
			result.HasUpdate = true
		} else if be != nil {
			result.HasUpdate = aa[i] < bb[i]
		}
		return result
	}
	result.HasUpdate = len(aa) < len(bb)
	return result
}
