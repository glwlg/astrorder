package browser

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var uvInstallMu sync.Mutex

type uvArtifact struct {
	name string
	sum  string
}

var uvArtifacts = map[string]uvArtifact{
	"windows/amd64": {"uv-x86_64-pc-windows-msvc.zip", "8fcb0cb46e1229065e344758980924e569bef5882ef45f46fada8fb24e06b74a"},
	"windows/arm64": {"uv-aarch64-pc-windows-msvc.zip", "9bc7c18e616230fa2dc6fb24bc3afde18a95c2b5c9433de747e9502c66041568"},
	"linux/amd64":   {"uv-x86_64-unknown-linux-gnu.tar.gz", "90b2f223fb69d19db49e117da601f64978593417988530aa733d456141b4bcbb"},
	"linux/arm64":   {"uv-aarch64-unknown-linux-gnu.tar.gz", "769d373e146692c639b5fbaae33b331c297a32e03d30448772051902df52bbf4"},
	"darwin/amd64":  {"uv-x86_64-apple-darwin.tar.gz", "69d9f9a00337f25a50dcb13882052da08b8469bac11091c98c5694c3c6721467"},
	"darwin/arm64":  {"uv-aarch64-apple-darwin.tar.gz", "77d2906988e8074fd43f2f329ec452ebbf9b0c257ba1c66451c71de70a6baf42"},
}

func ensureUV(ctx context.Context) (string, error) {
	if path, err := exec.LookPath("uv"); err == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		name := "uv"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(home, ".local", "bin", name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	artifact, ok := uvArtifacts[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("浏览器运行时不支持 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := "uv"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	directory := filepath.Join(cache, "Astrorder", "browser-runtime", "uv-0.12.1")
	path := filepath.Join(directory, name)
	uvInstallMu.Lock()
	defer uvInstallMu.Unlock()
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/astral-sh/uv/releases/download/0.12.1/"+artifact.name, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载浏览器运行时失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载浏览器运行时失败: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20+1))
	if err != nil || len(raw) > 64<<20 {
		return "", errors.New("浏览器运行时下载失败或文件过大")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != artifact.sum {
		return "", errors.New("浏览器运行时校验失败")
	}
	binary, err := extractUV(raw, artifact.name, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, "uv-*")
	if err != nil {
		return "", err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err := file.Write(binary); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Chmod(0700); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func extractUV(raw []byte, archiveName, name string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			if filepath.Base(file.Name) != name || file.FileInfo().IsDir() {
				continue
			}
			r, err := file.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(io.LimitReader(r, 128<<20+1))
			r.Close()
			if err != nil || len(data) > 128<<20 {
				return nil, errors.New("浏览器运行时解压失败")
			}
			return data, nil
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if filepath.Base(header.Name) == name && header.Typeflag == tar.TypeReg {
				data, err := io.ReadAll(io.LimitReader(reader, 128<<20+1))
				if err != nil || len(data) > 128<<20 {
					return nil, errors.New("浏览器运行时解压失败")
				}
				return data, nil
			}
		}
	}
	return nil, errors.New("浏览器运行时压缩包缺少执行文件")
}
