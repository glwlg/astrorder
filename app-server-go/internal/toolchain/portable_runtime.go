package toolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultNodeVersion = "v22.14.0"
	DefaultUVVersion   = "0.6.14"

	RecipeIDNodePortable = "node-portable"
	RecipeIDUVPortable   = "uv-portable"
)

func isPortableBuiltinRecipe(recipe InstallRecipe) bool {
	return recipe.ID == RecipeIDNodePortable || recipe.ID == RecipeIDUVPortable || recipe.Shell == "builtin"
}

func configuredNodeVersion() string {
	if v := strings.TrimSpace(os.Getenv("ASTRORDER_NODE_VERSION")); v != "" {
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		return v
	}
	return DefaultNodeVersion
}

func configuredUVVersion() string {
	if v := strings.TrimSpace(os.Getenv("ASTRORDER_UV_VERSION")); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	return DefaultUVVersion
}

// PortableDownloadURLs returns ordered candidate download URLs for portable node
// or uv runtimes, prioritizing China mirrors with official upstream fallback.
func PortableDownloadURLs(toolID, targetOS, targetArch, mirrorProfile string) ([]string, string, error) {
	id := strings.ToLower(strings.TrimSpace(toolID))
	osName := strings.ToLower(strings.TrimSpace(targetOS))
	arch := strings.ToLower(strings.TrimSpace(targetArch))
	mirror := strings.ToLower(strings.TrimSpace(mirrorProfile))
	preferOfficial := mirror == "official" || mirror == "global" || mirror == "direct"

	switch id {
	case "node":
		ver := configuredNodeVersion()
		asset, err := nodePortableAsset(ver, osName, arch)
		if err != nil {
			return nil, "", err
		}
		var bases []string
		if custom := strings.TrimRight(strings.TrimSpace(os.Getenv("ASTRORDER_NODE_MIRROR")), "/"); custom != "" {
			bases = append(bases, custom)
		}
		if preferOfficial {
			bases = append(bases,
				"https://nodejs.org/dist",
				"https://npmmirror.com/mirrors/node",
				"https://mirrors.tuna.tsinghua.edu.cn/nodejs-release",
			)
		} else {
			bases = append(bases,
				"https://npmmirror.com/mirrors/node",
				"https://mirrors.tuna.tsinghua.edu.cn/nodejs-release",
				"https://nodejs.org/dist",
			)
		}
		urls := make([]string, 0, len(bases))
		seen := map[string]bool{}
		for _, base := range bases {
			u := fmt.Sprintf("%s/%s/%s", strings.TrimRight(base, "/"), ver, asset)
			if !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
		return urls, asset, nil

	case "uv":
		ver := configuredUVVersion()
		asset, err := uvPortableAsset(osName, arch)
		if err != nil {
			return nil, "", err
		}
		official := fmt.Sprintf("https://github.com/astral-sh/uv/releases/download/%s/%s", ver, asset)
		var urls []string
		add := func(u string) {
			u = strings.TrimSpace(u)
			if u == "" {
				return
			}
			for _, existing := range urls {
				if existing == u {
					return
				}
			}
			urls = append(urls, u)
		}
		if custom := strings.TrimRight(strings.TrimSpace(os.Getenv("ASTRORDER_UV_MIRROR")), "/"); custom != "" {
			add(fmt.Sprintf("%s/%s/%s", custom, ver, asset))
		}
		if ghMirror := strings.TrimRight(strings.TrimSpace(os.Getenv("ASTRORDER_GITHUB_MIRROR")), "/"); ghMirror != "" {
			add(ghMirror + "/" + official)
		} else if ghMirror := strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_MIRROR")), "/"); ghMirror != "" {
			add(ghMirror + "/" + official)
		}
		cnMirrors := []string{
			fmt.Sprintf("https://mirrors.nju.edu.cn/github-release/astral-sh/uv/%s/%s", ver, asset),
			"https://ghfast.top/" + official,
		}
		if preferOfficial {
			add(official)
			for _, m := range cnMirrors {
				add(m)
			}
		} else {
			for _, m := range cnMirrors {
				add(m)
			}
			add(official)
		}
		return urls, asset, nil
	default:
		return nil, "", fmt.Errorf("不支持便携版自动下载的工具：%s", toolID)
	}
}

func nodePortableAsset(ver, targetOS, targetArch string) (string, error) {
	var archTag string
	switch targetArch {
	case "amd64", "x86_64", "x64":
		archTag = "x64"
	case "arm64", "aarch64":
		archTag = "arm64"
	default:
		return "", fmt.Errorf("不支持的 Node.js 便携版架构：%s", targetArch)
	}
	switch targetOS {
	case "windows":
		return fmt.Sprintf("node-%s-win-%s.zip", ver, archTag), nil
	case "linux":
		return fmt.Sprintf("node-%s-linux-%s.tar.gz", ver, archTag), nil
	case "darwin", "macos":
		return fmt.Sprintf("node-%s-darwin-%s.tar.gz", ver, archTag), nil
	default:
		return "", fmt.Errorf("不支持的 Node.js 便携版平台：%s", targetOS)
	}
}

func uvPortableAsset(targetOS, targetArch string) (string, error) {
	var archTag string
	switch targetArch {
	case "amd64", "x86_64", "x64":
		archTag = "x86_64"
	case "arm64", "aarch64":
		archTag = "aarch64"
	default:
		return "", fmt.Errorf("不支持的 uv 便携版架构：%s", targetArch)
	}
	switch targetOS {
	case "windows":
		return fmt.Sprintf("uv-%s-pc-windows-msvc.zip", archTag), nil
	case "linux":
		return fmt.Sprintf("uv-%s-unknown-linux-gnu.tar.gz", archTag), nil
	case "darwin", "macos":
		return fmt.Sprintf("uv-%s-apple-darwin.tar.gz", archTag), nil
	default:
		return "", fmt.Errorf("不支持的 uv 便携版平台：%s", targetOS)
	}
}

func buildPortableHTTPClient(proxyURL string) (*http.Client, error) {
	proxyFunc := http.ProxyFromEnvironment
	if trimmed := strings.TrimSpace(proxyURL); trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errors.New("代理地址格式无效，例如 http://127.0.0.1:7890")
		}
		proxyFunc = http.ProxyURL(parsed)
	}
	transport := &http.Transport{
		Proxy: proxyFunc,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport}, nil
}

// InstallPortableRuntime downloads and extracts a managed portable runtime
// ("node" or "uv") into <runtimesRoot>/<toolID>, trying CN mirrors first and
// falling back to official upstream while respecting unified proxy settings.
func (s *Service) InstallPortableRuntime(ctx context.Context, toolID string, netProfile NetworkProfile, output io.Writer) (string, error) {
	id := strings.ToLower(strings.TrimSpace(toolID))
	root := s.runtimesRoot()
	if root == "" {
		return "", errors.New("无法确定星序托管运行时目录")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建托管运行时目录失败：%w", err)
	}

	urls, assetName, err := PortableDownloadURLs(id, runtime.GOOS, runtime.GOARCH, netProfile.Mirror)
	if err != nil {
		return "", err
	}
	client := s.httpClient
	if client == nil {
		client, err = buildPortableHTTPClient(netProfile.ProxyURL)
		if err != nil {
			return "", err
		}
	}

	if output != nil {
		fmt.Fprintf(output, "[PORTABLE] 目标托管目录：%s\n", filepath.Join(root, id))
		if strings.TrimSpace(netProfile.ProxyURL) != "" {
			fmt.Fprintf(output, "[PORTABLE] 已启用统一代理：%s\n", strings.TrimSpace(netProfile.ProxyURL))
		}
	}

	tmpArchive, err := os.CreateTemp(root, fmt.Sprintf(".download-%s-*-%s", id, assetName))
	if err != nil {
		return "", fmt.Errorf("创建下载临时文件失败：%w", err)
	}
	tmpArchivePath := tmpArchive.Name()
	_ = tmpArchive.Close()
	defer os.Remove(tmpArchivePath)

	var lastErr error
	downloadedFrom := ""
	for idx, rawURL := range urls {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if output != nil {
			fmt.Fprintf(output, "[DOWNLOAD] (%d/%d) 尝试下载：%s\n", idx+1, len(urls), rawURL)
		}
		dlErr := downloadToFile(ctx, client, rawURL, tmpArchivePath, output)
		if dlErr == nil {
			downloadedFrom = rawURL
			break
		}
		lastErr = dlErr
		if output != nil {
			fmt.Fprintf(output, "[WARN] 源不可用（%v），尝试回退下一个源…\n", dlErr)
		}
	}
	if downloadedFrom == "" {
		return "", fmt.Errorf("所有镜像与官方源均下载失败：%v", lastErr)
	}

	stagingDir := filepath.Join(root, fmt.Sprintf(".staging-%s-%s", id, rand.Text()))
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return "", fmt.Errorf("创建解压暂存目录失败：%w", err)
	}
	defer os.RemoveAll(stagingDir)

	if output != nil {
		fmt.Fprintf(output, "[EXTRACT] 正在解压 %s…\n", assetName)
	}
	if strings.HasSuffix(strings.ToLower(assetName), ".zip") {
		if err := extractZipPortable(tmpArchivePath, stagingDir); err != nil {
			return "", fmt.Errorf("解压 zip 失败：%w", err)
		}
	} else if strings.HasSuffix(strings.ToLower(assetName), ".tar.gz") || strings.HasSuffix(strings.ToLower(assetName), ".tgz") {
		if err := extractTarGzPortable(tmpArchivePath, stagingDir); err != nil {
			return "", fmt.Errorf("解压 tar.gz 失败：%w", err)
		}
	} else {
		return "", fmt.Errorf("不支持的归档格式：%s", assetName)
	}

	// Flatten single wrapper top-level directory (e.g. node-v22.14.0-win-x64/).
	payloadDir := unwrapSingleTopDir(stagingDir)
	expectedExe := expectedPortableBinary(payloadDir, id)
	if expectedExe == "" {
		return "", fmt.Errorf("归档校验失败：未在解压结果中找到 %s 可执行文件", id)
	}

	targetDir := filepath.Join(root, id)
	if err := swapDirectoryAtomic(payloadDir, targetDir); err != nil {
		return "", fmt.Errorf("安装便携运行时到 %s 失败：%w", targetDir, err)
	}

	// Ensure managed npm-global dir exists when Node.js is installed.
	if id == "node" {
		_ = os.MkdirAll(filepath.Join(root, "npm-global"), 0o755)
	}

	finalExe := expectedPortableBinary(targetDir, id)
	if finalExe == "" {
		return "", fmt.Errorf("便携运行时安装后未找到可执行文件：%s", targetDir)
	}

	// Verify immediately with refreshed PATH (no OS/app restart needed).
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(verifyCtx, finalExe, "--version")
	configureHiddenCmd(cmd)
	cmd.Env = s.localChildEnv(nil)
	if verOut, verErr := cmd.CombinedOutput(); verErr == nil {
		if output != nil {
			fmt.Fprintf(output, "[VERIFY] %s 就绪：%s (%s)\n", id, finalExe, strings.TrimSpace(string(verOut)))
		}
	} else if output != nil {
		fmt.Fprintf(output, "[INFO] 已解压 %s 至 %s\n", id, finalExe)
	}
	return finalExe, nil
}

func downloadToFile(ctx context.Context, client *http.Client, rawURL, dstPath string, output io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Astrorder-Toolchain/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n == 0 {
		return errors.New("下载文件为空")
	}
	if output != nil {
		fmt.Fprintf(output, "[DOWNLOAD] 下载完成（%.2f MB）\n", float64(n)/(1024*1024))
	}
	return nil
}

func extractZipPortable(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	cleanDest := filepath.Clean(destDir)
	for _, f := range zr.File {
		targetPath, err := safeJoinUnder(cleanDest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(outFile, rc)
		closeOutErr := outFile.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	return nil
}

func extractTarGzPortable(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(destDir)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		targetPath, err := safeJoinUnder(cleanDest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(outFile, tr)
			closeOutErr := outFile.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeOutErr != nil {
				return closeOutErr
			}
		case tar.TypeSymlink:
			// Only allow relative symlinks that stay inside destDir.
			if filepath.IsAbs(hdr.Linkname) {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(targetPath), hdr.Linkname))
			if !pathWithinDir(cleanDest, resolved) {
				return fmt.Errorf("拒绝越界符号链接：%s -> %s", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			_ = os.Remove(targetPath)
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeJoinUnder(rootDir, memberName string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimLeft(memberName, `/\`)))
	if cleaned == "." || cleaned == "" {
		return rootDir, nil
	}
	if filepath.IsAbs(memberName) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("归档包含非法路径：%s", memberName)
	}
	target := filepath.Join(rootDir, cleaned)
	if !pathWithinDir(rootDir, target) {
		return "", fmt.Errorf("归档路径越界：%s", memberName)
	}
	return target, nil
}

func pathWithinDir(rootDir, target string) bool {
	rel, err := filepath.Rel(rootDir, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func unwrapSingleTopDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dir
	}
	var nonHidden []os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		nonHidden = append(nonHidden, e)
	}
	if len(nonHidden) == 1 && nonHidden[0].IsDir() {
		return filepath.Join(dir, nonHidden[0].Name())
	}
	return dir
}

func expectedPortableBinary(dir, toolID string) string {
	var candidates []string
	switch toolID {
	case "node":
		if runtime.GOOS == "windows" {
			candidates = []string{
				filepath.Join(dir, "node.exe"),
				filepath.Join(dir, "bin", "node.exe"),
			}
		} else {
			candidates = []string{
				filepath.Join(dir, "bin", "node"),
				filepath.Join(dir, "node"),
			}
		}
	case "uv":
		if runtime.GOOS == "windows" {
			candidates = []string{
				filepath.Join(dir, "uv.exe"),
				filepath.Join(dir, "bin", "uv.exe"),
			}
		} else {
			candidates = []string{
				filepath.Join(dir, "uv"),
				filepath.Join(dir, "bin", "uv"),
			}
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			if runtime.GOOS != "windows" {
				_ = os.Chmod(c, info.Mode().Perm()|0o111)
			}
			return c
		}
	}
	return ""
}

func swapDirectoryAtomic(srcDir, dstDir string) error {
	backupDir := ""
	if _, err := os.Stat(dstDir); err == nil {
		backupDir = dstDir + ".bak-" + rand.Text()
		if err := os.Rename(dstDir, backupDir); err != nil {
			return fmt.Errorf("备份旧运行时目录失败：%w", err)
		}
	}
	if err := os.Rename(srcDir, dstDir); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, dstDir)
		}
		return err
	}
	if backupDir != "" {
		_ = os.RemoveAll(backupDir)
	}
	return nil
}
