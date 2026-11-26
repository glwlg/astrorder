package agentaccounts

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

// QuotaBucket mirrors the frontend AgyQuotaBucket schema so all platforms share
// identical quota tile, hover card, and mobile rendering.
type QuotaBucket struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Window            string  `json:"window"`
	RemainingFraction float64 `json:"remaining_fraction"`
	ResetAt           int64   `json:"reset_at,omitempty"`
}

// QuotaGroup groups related quota buckets under a label (e.g. "Codex" or "Grok").
type QuotaGroup struct {
	Name    string        `json:"name"`
	Buckets []QuotaBucket `json:"buckets"`
}

// QuotaResult is the unified quota/subscription payload returned by all
// platform account quota endpoints.
type QuotaResult struct {
	Plan         string       `json:"plan,omitempty"`
	TierID       string       `json:"tier_id,omitempty"`
	AccountLabel string       `json:"account_label,omitempty"`
	Detail       string       `json:"detail,omitempty"`
	Groups       []QuotaGroup `json:"groups"`
	AsOf         string       `json:"as_of"`
}

type rpcEnvelope struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type isolatedRPCRequest struct {
	Prefix      string
	BinaryName  string
	Executable  string
	Subcommand  []string
	HomeEnvVar  string
	AuthFile    string
	AuthJSON    []byte
	InitRequest string
	FollowUps   []string
	RequiredIDs []int
	Timeout     time.Duration
}

const remoteStdioRPCScript = `exe="$1"
home_var="$2"
auth_file="$3"
subcmd="$4"
init_req="$5"
expected_ids="$6"
shift 6
d=$(mktemp -d) || exit 1
pid=""
wd=""
cleanup() {
  exec 3>&- 2>/dev/null || true
  [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  [ -n "$wd" ] && kill "$wd" 2>/dev/null || true
  rm -rf -- "$d"
}
trap cleanup EXIT INT TERM
umask 077
cat > "$d/$auth_file" || exit 1
chmod 600 "$d/$auth_file" 2>/dev/null || true
mkfifo "$d/in" "$d/out" || exit 1
PATH="$HOME/.local/bin:$HOME/.antigravity/bin:$HOME/.vite-plus/bin:$HOME/.cargo/bin:$HOME/bin:$HOME/.grok/bin:/usr/local/bin:$PATH"
export PATH
export "$home_var=$d"
"$exe" $subcmd < "$d/in" > "$d/out" 2>"$d/err" &
pid=$!
( sleep 25; kill "$pid" 2>/dev/null || true ) >/dev/null 2>&1 &
wd=$!
exec 3>"$d/in"
printf '%s\n' "$init_req" >&3
has_id() {
  case "$1" in
    *'"id":'"$2"[,}]*|*'"id": '"$2"[,}]*|*'"id":'"$2"' '[},]*|*'"id": '"$2"' '[},]*) return 0 ;;
  esac
  return 1
}
sent=0
seen=" "
while IFS= read -r line; do
  if [ "$sent" -eq 0 ] && has_id "$line" 1; then
    printf '%s\n' "$line"
    sent=1
    for req in "$@"; do
      printf '%s\n' "$req" >&3
    done
    continue
  fi
  for rid in $expected_ids; do
    if has_id "$line" "$rid"; then
      printf '%s\n' "$line"
      case "$seen" in
        *" $rid "*) ;;
        *) seen="${seen}${rid} " ;;
      esac
      break
    fi
  done
  all_done=1
  for rid in $expected_ids; do
    case "$seen" in
      *" $rid "*) ;;
      *) all_done=0; break ;;
    esac
  done
  if [ "$all_done" -eq 1 ]; then
    break
  fi
done < "$d/out"
if [ -s "$d/err" ]; then
  cat "$d/err" >&2
fi`

func runIsolatedStdioRPC(ctx context.Context, env envtarget.Env, req isolatedRPCRequest) (map[int]rpcEnvelope, string, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if env.IsLocal() {
		return runLocalIsolatedStdioRPC(rpcCtx, env, req)
	}
	return runRemoteIsolatedStdioRPC(rpcCtx, env, req)
}

func runLocalIsolatedStdioRPC(ctx context.Context, env envtarget.Env, req isolatedRPCRequest) (map[int]rpcEnvelope, string, error) {
	exe := strings.TrimSpace(req.Executable)
	if exe == "" {
		found, ok := env.Which(ctx, req.BinaryName)
		if !ok || strings.TrimSpace(found) == "" {
			return nil, "", fmt.Errorf("本机未找到 %s 命令", req.BinaryName)
		}
		exe = found
	}
	exe = filepath.FromSlash(exe)

	tmpDir, err := os.MkdirTemp("", "astrorder-"+req.Prefix+"-quota-*")
	if err != nil {
		return nil, "", fmt.Errorf("无法创建隔离目录: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	authPath := filepath.Join(tmpDir, req.AuthFile)
	if err := os.WriteFile(authPath, req.AuthJSON, 0o600); err != nil {
		return nil, "", fmt.Errorf("无法写入隔离凭证: %w", err)
	}

	cmd := hiddenCommand(ctx, exe, req.Subcommand...)
	cmd.Env = replaceEnv(os.Environ(), req.HomeEnvVar, tmpDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	var stderrBuf boundedBuffer
	stderrBuf.limit = 16 * 1024
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("无法启动 %s: %w", req.BinaryName, err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	defer func() {
		_ = stdin.Close()
		select {
		case <-waitDone:
		case <-time.After(400 * time.Millisecond):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-waitDone
		}
	}()

	if _, err := io.WriteString(stdin, req.InitRequest+"\n"); err != nil {
		return nil, stderrBuf.String(), fmt.Errorf("向 %s 发送初始化请求失败: %w", req.BinaryName, err)
	}

	required := make(map[int]bool, len(req.RequiredIDs))
	for _, id := range req.RequiredIDs {
		required[id] = true
	}

	responses := make(map[int]rpcEnvelope, len(req.RequiredIDs)+1)
	sentFollowUps := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var envMsg rpcEnvelope
		if json.Unmarshal(line, &envMsg) != nil || envMsg.ID == nil {
			continue
		}
		id := *envMsg.ID
		responses[id] = envMsg
		if id == 1 && !sentFollowUps {
			sentFollowUps = true
			for _, nextReq := range req.FollowUps {
				if _, err := io.WriteString(stdin, nextReq+"\n"); err != nil {
					return responses, stderrBuf.String(), err
				}
			}
		}
		if hasAllRequired(responses, required) {
			break
		}
	}

	if err := ctx.Err(); err != nil && !hasAllRequired(responses, required) {
		return responses, stderrBuf.String(), fmt.Errorf("查询 %s 额度超时", req.BinaryName)
	}
	return responses, strings.TrimSpace(stderrBuf.String()), nil
}

func runRemoteIsolatedStdioRPC(ctx context.Context, env envtarget.Env, req isolatedRPCRequest) (map[int]rpcEnvelope, string, error) {
	exe := strings.TrimSpace(req.Executable)
	if exe == "" {
		if found, ok := env.Which(ctx, req.BinaryName); ok && strings.TrimSpace(found) != "" {
			exe = found
		} else {
			exe = req.BinaryName
		}
	}
	idStrings := make([]string, 0, len(req.RequiredIDs))
	for _, id := range req.RequiredIDs {
		idStrings = append(idStrings, strconv.Itoa(id))
	}
	args := []string{
		exe,
		req.HomeEnvVar,
		req.AuthFile,
		strings.Join(req.Subcommand, " "),
		req.InitRequest,
		strings.Join(idStrings, " "),
	}
	args = append(args, req.FollowUps...)

	res, err := env.Shell(ctx, remoteStdioRPCScript, req.AuthJSON, args...)
	if err != nil {
		return nil, "", err
	}

	responses := make(map[int]rpcEnvelope, len(req.RequiredIDs)+1)
	scanner := bufio.NewScanner(strings.NewReader(res.Stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var envMsg rpcEnvelope
		if json.Unmarshal(line, &envMsg) != nil || envMsg.ID == nil {
			continue
		}
		responses[*envMsg.ID] = envMsg
	}
	return responses, strings.TrimSpace(res.Stderr), nil
}

func hasAllRequired(responses map[int]rpcEnvelope, required map[int]bool) bool {
	if len(required) == 0 {
		return true
	}
	for id := range required {
		if _, ok := responses[id]; !ok {
			return false
		}
	}
	return true
}

func replaceEnv(base []string, key, value string) []string {
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(k, key) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, key+"="+value)
}

type boundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:remaining])
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}

func sha256Hex(parts ...string) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

func clampFraction(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return math.Round(v*1000) / 1000
}

func unixToMillis(ts float64) int64 {
	if math.IsNaN(ts) || math.IsInf(ts, 0) || ts <= 0 {
		return 0
	}
	if ts < 1e11 {
		return int64(ts * 1000)
	}
	return int64(ts)
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := m[key]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

func floatValue(m map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		raw, ok := m[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return v, true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			if f, err := v.Float64(); err == nil {
				return f, true
			}
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func boolValue(m map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		raw, ok := m[key]
		if !ok || raw == nil {
			continue
		}
		if b, ok := raw.(bool); ok {
			return b, true
		}
	}
	return false, false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
