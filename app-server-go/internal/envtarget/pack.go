package envtarget

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxPackBytes bounds a packed directory (a skill is a few files).
const maxPackBytes = 32 << 20

func tempSuffix() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func validDirName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`+"\x00")
}

// ---- local ------------------------------------------------------------

func (e *localEnv) PackDir(_ context.Context, dir string) ([]byte, error) {
	root := filepath.FromSlash(dir)
	name := filepath.Base(root)
	if !validDirName(name) {
		return nil, Errorf(400, "目录名无效：%s", dir)
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	total := 0
	visited := map[string]bool{}
	var add func(source, rel string) error
	add = func(source, rel string) error {
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if info.IsDir() {
			real, err := filepath.EvalSymlinks(source)
			if err != nil || visited[real] {
				return nil
			}
			visited[real] = true
			defer delete(visited, real)
			if err := archive.WriteHeader(&tar.Header{Name: rel + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				return err
			}
			entries, err := os.ReadDir(source)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Name() == ".git" {
					continue
				}
				if err := add(filepath.Join(source, entry.Name()), rel+"/"+entry.Name()); err != nil {
					return err
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if total += len(data); total > maxPackBytes {
			return Errorf(413, "目录超过 %d MiB，无法打包", maxPackBytes>>20)
		}
		mode := int64(0o644)
		if bytes.HasPrefix(data, []byte("#!")) {
			mode = 0o755
		}
		if err := archive.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = archive.Write(data)
		return err
	}
	if err := add(root, name); err != nil {
		var failure *Error
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, Errorf(500, "打包 %s 失败：%v", dir, err)
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (e *localEnv) UnpackDir(_ context.Context, parent, name string, data []byte) error {
	if !validDirName(name) {
		return Errorf(400, "目录名无效：%s", name)
	}
	parentDir := filepath.FromSlash(parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return Errorf(500, "创建目录 %s 失败：%v", parent, err)
	}
	staging, err := os.MkdirTemp(parentDir, "."+name+".astrorder-")
	if err != nil {
		return Errorf(500, "创建临时目录失败：%v", err)
	}
	defer os.RemoveAll(staging)
	if err := extractTarGz(data, staging); err != nil {
		return err
	}
	staged := filepath.Join(staging, name)
	if info, err := os.Stat(staged); err != nil || !info.IsDir() {
		return Errorf(422, "归档中没有目录 %s", name)
	}
	final := filepath.Join(parentDir, name)
	if err := os.RemoveAll(final); err != nil {
		return Errorf(500, "替换旧目录失败：%v", err)
	}
	if err := os.Rename(staged, final); err != nil {
		return Errorf(500, "放置目录失败：%v", err)
	}
	return nil
}

// extractTarGz unpacks regular files and directories only; links, devices and
// any path that would leave dest are rejected.
func extractTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return Errorf(422, "归档格式无效")
	}
	reader := tar.NewReader(gz)
	total := int64(0)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return Errorf(422, "归档损坏")
		}
		clean := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(clean, "\x00:\\") {
			return Errorf(422, "归档包含非法路径：%s", header.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return Errorf(500, "创建目录失败：%v", err)
			}
		case tar.TypeReg:
			if total += header.Size; total > maxPackBytes {
				return Errorf(413, "归档超过 %d MiB", maxPackBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return Errorf(500, "创建目录失败：%v", err)
			}
			content, err := io.ReadAll(io.LimitReader(reader, header.Size))
			if err != nil {
				return Errorf(422, "归档损坏")
			}
			if err := os.WriteFile(target, content, fs.FileMode(0o644)); err != nil {
				return Errorf(500, "写入 %s 失败：%v", clean, err)
			}
		default:
			// symlinks, hard links, devices: skipped on purpose
		}
	}
}

// ---- remote -----------------------------------------------------------

const packScript = `parent=$(dirname -- "$1"); name=$(basename -- "$1"); [ -d "$1" ] || exit 3; tar -czhf - --exclude=.git -C "$parent" -- "$name"`

func (e *remoteEnv) PackDir(ctx context.Context, dir string) ([]byte, error) {
	result, err := e.Shell(ctx, packScript, nil, dir)
	if err != nil {
		return nil, err
	}
	switch result.Code {
	case 0:
		if len(result.Stdout) > maxPackBytes {
			return nil, Errorf(413, "目录超过 %d MiB，无法打包", maxPackBytes>>20)
		}
		return []byte(result.Stdout), nil
	case 3:
		return nil, ErrNotExist
	}
	return nil, Errorf(502, "%s：打包 %s 失败", e.target.Name, dir)
}

const unpackScript = `set -e
parent="$1"; name="$2"
mkdir -p -- "$parent"
tmp="$parent/.$name.astrorder-$$"
rm -rf -- "$tmp"; mkdir -- "$tmp"
trap 'rm -rf -- "$tmp"' EXIT
tar -xzf - -C "$tmp" --no-same-owner --no-same-permissions
[ -d "$tmp/$name" ] || exit 4
rm -rf -- "$parent/$name"
mv -- "$tmp/$name" "$parent/$name"`

func (e *remoteEnv) UnpackDir(ctx context.Context, parent, name string, data []byte) error {
	if !validDirName(name) {
		return Errorf(400, "目录名无效：%s", name)
	}
	result, err := e.Shell(ctx, unpackScript, data, parent, name)
	if err != nil {
		return err
	}
	switch result.Code {
	case 0:
		return nil
	case 4:
		return Errorf(422, "归档中没有目录 %s", name)
	}
	return Errorf(502, "%s：解包到 %s 失败", e.target.Name, parent)
}
