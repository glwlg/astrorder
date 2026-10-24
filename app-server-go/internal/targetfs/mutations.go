package targetfs

import (
 "context"
 "io"
 "io/fs"
 "os"
 "path/filepath"
 "strings"
)

type RenameRequest struct { Path string `json:"path"`; Name string `json:"name"` }

type DeleteRequest struct { Path string `json:"path"` }

func validateEntryName(name string) error {
 if strings.TrimSpace(name) == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") { return errorf(400, "invalid_name", "文件名不合法。", nil) }
 return nil
}

func (l *localFileSystem) Rename(ctx context.Context, request RenameRequest) (Info, error) {
 if err := ctx.Err(); err != nil { return Info{}, err }
 if err := validateEntryName(request.Name); err != nil { return Info{}, err }
 source, err := l.existingPath(request.Path); if err != nil { return Info{}, err }
 info, err := os.Lstat(source); if err != nil { return Info{}, errorf(404, "path_not_found", "Path does not exist", err) }
 if !info.Mode().IsRegular() { return Info{}, errorf(400, "invalid_file", "只能重命名文件。", nil) }
 target, err := l.writablePath(filepath.Join(filepath.Dir(source), request.Name)); if err != nil { return Info{}, err }
 if _, err := os.Lstat(target); err == nil { return Info{}, errorf(409, "path_exists", "目标文件已存在。", nil) } else if !os.IsNotExist(err) { return Info{}, errorf(400, "invalid_path", "目标路径不合法。", err) }
 if err := os.Rename(source, target); err != nil { return Info{}, errorf(400, "rename_failed", "重命名文件失败。", err) }
 return l.Stat(ctx, target)
}

func (l *localFileSystem) Delete(ctx context.Context, request DeleteRequest) error {
 if err := ctx.Err(); err != nil { return err }
 path, err := l.existingPath(request.Path); if err != nil { return err }
 info, err := os.Lstat(path); if err != nil { return errorf(404, "path_not_found", "Path does not exist", err) }
 if !info.Mode().IsRegular() { return errorf(400, "invalid_file", "只能删除文件。", nil) }
 if err := os.Remove(path); err != nil { return errorf(400, "delete_failed", "删除文件失败。", err) }
 return nil
}

var _ FileSystem = (*localFileSystem)(nil)
var _ = io.Reader(nil)
var _ = fs.FileMode(0)
