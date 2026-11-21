package backups

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

// ValidationError carries an explicit HTTP status and localized user message.
type ValidationError struct {
	Status int
	Detail string
}

func (e *ValidationError) Error() string { return e.Detail }

func policyError(status int, detail string) error {
	return &ValidationError{Status: status, Detail: detail}
}

var blockedDirSegments = map[string]bool{
	".gemini": true,
	".ssh":    true,
	".aws":    true,
	".gnupg":  true,
	".kube":   true,
}

var blockedFilenames = map[string]bool{
	"antigravity-oauth-token": true,
	"id_rsa":                  true,
	"id_ed25519":              true,
	"id_ecdsa":                true,
	"id_dsa":                  true,
	"authorized_keys":         true,
	"authorized_keys2":        true,
	"known_hosts":             true,
	".netrc":                  true,
	".git-credentials":        true,
	".npmrc":                  true,
	".pypirc":                 true,
	"credentials.json":        true,
	"shadow":                  true,
	"gshadow":                 true,
	"sudoers":                 true,
	"login.keychain":          true,
	"login.keychain-db":       true,
}

var blockedSequences = [][]string{
	{".config", "gcloud"},
	{".docker", "config.json"},
	{"appdata", "local", "microsoft", "credentials"},
	{"appdata", "roaming", "microsoft", "credentials"},
	{"appdata", "roaming", "microsoft", "protect"},
	{"windows", "system32", "config"},
}

func hasInvalidControlOrTraversal(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return true
	}
	normalized := strings.ReplaceAll(raw, `\`, "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isWindowsDriveAbs(p string) bool {
	return len(p) >= 3 && isASCIIAlpha(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func isWindowsUNCAbs(p string) bool {
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`)
}

func isWindowsStylePath(p string) bool {
	return isWindowsDriveAbs(p) || isWindowsUNCAbs(p)
}

func cleanPOSIXAbs(raw string) (string, error) {
	if strings.ContainsRune(raw, '\\') {
		return "", policyError(422, "远程路径必须使用正斜杠 / 开头的绝对路径。")
	}
	if !strings.HasPrefix(raw, "/") {
		return "", policyError(422, "远程路径必须以 / 开头。")
	}
	cleaned := path.Clean(raw)
	return cleaned, nil
}

func cleanLocalPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if isWindowsDriveAbs(trimmed) {
		drive := strings.ToUpper(trimmed[:2])
		rest := strings.ReplaceAll(trimmed[2:], `\`, "/")
		cleaned := path.Clean("/" + rest)
		if runtime.GOOS == "windows" {
			return filepath.Clean(drive + filepath.FromSlash(cleaned)), nil
		}
		if cleaned == "/" {
			return drive + `\`, nil
		}
		return drive + strings.ReplaceAll(cleaned, "/", `\`), nil
	}
	if isWindowsUNCAbs(trimmed) {
		rest := strings.TrimLeft(strings.ReplaceAll(trimmed, `\`, "/"), "/")
		parts := splitNonEmpty(rest)
		if len(parts) < 2 {
			return "", policyError(422, "UNC 路径格式不完整。")
		}
		cleaned := path.Clean("/" + strings.Join(parts, "/"))
		return `\\` + strings.ReplaceAll(strings.TrimPrefix(cleaned, "/"), "/", `\`), nil
	}
	if strings.HasPrefix(trimmed, "/") {
		cleaned := path.Clean(trimmed)
		if runtime.GOOS == "windows" {
			return "", policyError(422, "本机 Windows 路径请使用盘符开头的绝对路径，例如 D:\\Backups。")
		}
		return cleaned, nil
	}
	return "", policyError(422, "路径必须为绝对路径。")
}

func splitNonEmpty(slashPath string) []string {
	parts := make([]string, 0)
	for _, part := range strings.Split(strings.ReplaceAll(slashPath, `\`, "/"), "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}

func lowerNormalizedSegments(anyPath string) (segments []string, hasDrive bool, isUNC bool) {
	trimmed := strings.TrimSpace(anyPath)
	if isWindowsDriveAbs(trimmed) {
		hasDrive = true
		trimmed = trimmed[2:]
	} else if isWindowsUNCAbs(trimmed) {
		isUNC = true
	}
	parts := splitNonEmpty(strings.ToLower(trimmed))
	return parts, hasDrive, isUNC
}

func hasSegmentSequence(segments, seq []string) bool {
	if len(seq) == 0 || len(segments) < len(seq) {
		return false
	}
	for i := 0; i <= len(segments)-len(seq); i++ {
		matched := true
		for j := range seq {
			if segments[i+j] != seq[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// isRestrictedDestinationPath protects credential and system locations from backup writes.
func isRestrictedDestinationPath(p string) bool {
	segments, _, _ := lowerNormalizedSegments(p)
	if len(segments) == 0 {
		return false
	}
	for _, seg := range segments {
		if blockedDirSegments[seg] {
			return true
		}
		if blockedFilenames[seg] {
			return true
		}
		if strings.Contains(seg, "antigravity-oauth-token") || strings.Contains(seg, "oauth-token") || strings.Contains(seg, "oauth_token") {
			return true
		}
	}
	for _, seq := range blockedSequences {
		if hasSegmentSequence(segments, seq) {
			return true
		}
	}
	return false
}

func isTooBroadRoot(cleaned string) bool {
	segments, hasDrive, isUNC := lowerNormalizedSegments(cleaned)
	if len(segments) == 0 {
		return true
	}
	if isUNC {
		return len(segments) <= 2
	}
	if hasDrive {
		if len(segments) == 1 && (segments[0] == "windows" || segments[0] == "users") {
			return true
		}
		if len(segments) == 2 && segments[0] == "users" {
			return true
		}
		if len(segments) == 2 && segments[0] == "windows" && segments[1] == "system32" {
			return true
		}
		return false
	}
	if len(segments) == 1 {
		switch segments[0] {
		case "etc", "proc", "sys", "dev", "boot", "root", "home", "users", "usr", "var", "bin", "sbin", "lib", "lib64":
			return true
		}
	}
	if len(segments) >= 1 && (segments[0] == "proc" || segments[0] == "sys" || segments[0] == "dev" || segments[0] == "boot") {
		return true
	}
	if len(segments) == 2 && (segments[0] == "home" || segments[0] == "users") {
		return true
	}
	return false
}

// ValidateSourcePath validates and normalizes a backup source directory or file path.
func ValidateSourcePath(connectionID, rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", policyError(422, "请填写需要备份的源目录或文件路径。")
	}
	if hasInvalidControlOrTraversal(trimmed) {
		return "", policyError(422, "源路径包含非法字符或不允许的 .. 穿越段。")
	}
	isRemote := strings.TrimSpace(connectionID) != "" && strings.TrimSpace(connectionID) != targetfs.LocalConnectionID
	var cleaned string
	var err error
	if isRemote {
		cleaned, err = cleanPOSIXAbs(trimmed)
	} else {
		cleaned, err = cleanLocalPath(trimmed)
	}
	if err != nil {
		return "", err
	}
	if isTooBroadRoot(cleaned) {
		return "", policyError(422, fmt.Sprintf("路径 %q 范围过宽或属于系统根目录，请指定更具体的子目录或文件。", cleaned))
	}
	return cleaned, nil
}

// ValidateDestinationDir validates the local host destination directory where versioned backups are stored.
func ValidateDestinationDir(rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", policyError(422, "请填写本机备份保存目录。")
	}
	if hasInvalidControlOrTraversal(trimmed) {
		return "", policyError(422, "目标目录包含非法字符或不允许的 .. 穿越段。")
	}
	cleaned, err := cleanLocalPath(trimmed)
	if err != nil {
		return "", err
	}
	if isRestrictedDestinationPath(cleaned) {
		return "", policyError(403, "请选择其他保存目录，认证凭据、密钥和系统敏感目录不能用作备份保存位置。")
	}
	if isTooBroadRoot(cleaned) {
		return "", policyError(422, fmt.Sprintf("本机保存目录 %q 范围过宽，请指定专用的备份子目录。", cleaned))
	}
	return cleaned, nil
}

// ValidateLocalPair ensures local-to-local backups cannot create self-recursive trees.
func ValidateLocalPair(sourcePath, destinationDir string) error {
	srcNorm := canonicalCompareSlash(sourcePath)
	dstNorm := canonicalCompareSlash(destinationDir)
	if srcNorm == "" || dstNorm == "" {
		return nil
	}
	if dstNorm == srcNorm || strings.HasPrefix(dstNorm, srcNorm+"/") {
		return policyError(422, "本机备份目标目录不能与源路径相同或位于源目录内部，以免产生递归备份。")
	}
	return nil
}

func canonicalCompareSlash(p string) string {
	s := strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	s = strings.TrimRight(s, "/")
	if runtime.GOOS == "windows" || isWindowsStylePath(p) {
		s = strings.ToLower(s)
	}
	return s
}
