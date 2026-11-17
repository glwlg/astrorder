package blackboard

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/targetfs"
)

// FileResolver connects artifact reads to the application's target filesystem.
type FileResolver interface {
	Resolve(context.Context, targetfs.ResolveRequest) (targetfs.Resolved, error)
}

func (s *Service) WithFileResolver(resolver FileResolver) *Service {
	s.resolver = resolver
	return s
}

func (s *Service) artifactContent(ctx context.Context, namespace, key string, payload map[string]any) (map[string]any, error) {
	var directory artifacts.Directory
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		directory, err = artifacts.ReadTx(ctx, tx, strings.TrimPrefix(namespace, "group:"))
		return err
	})
	if err != nil {
		return nil, err
	}
	var file *artifacts.File
	for _, item := range directory.Items {
		if item.ContentKey == key {
			copy := item
			file = &copy
			break
		}
	}
	if file == nil {
		return map[string]any{"namespace": namespace, "key": key, "exists": false, "value": nil}, nil
	}
	// Validate range inputs before contacting the source server.
	offset, err := readNumber(payload, "offset", 0, 1<<53-1)
	if err != nil {
		return nil, err
	}
	limit, err := readNumber(payload, "limit", 256*1024, 4*1024*1024)
	if err != nil || limit == 0 {
		return nil, errors.New("limit 必须是 1 到 4194304 之间的字节数")
	}
	if s.resolver == nil {
		return nil, errors.New("产物文件读取服务不可用")
	}
	// Match file viewers: resolve the output file's parent, including outputs
	// outside the session workspace, while keeping configured connection roots.
	lastSlash := strings.LastIndex(file.Path, "/")
	if lastSlash < 0 {
		return nil, errors.New("产物路径无效")
	}
	readRoot := file.Path[:lastSlash]
	if readRoot == "" {
		readRoot = "/"
	} else if len(readRoot) == 2 && readRoot[1] == ':' {
		readRoot += "/"
	}
	target, err := s.resolver.Resolve(ctx, targetfs.ResolveRequest{Workspace: readRoot, ConnectionID: file.ConnectionID})
	if err != nil {
		return nil, fmt.Errorf("读取产物「%s」（%s，%s）失败：%w", file.Name, file.MemberName, file.ConnectionID, err)
	}
	result, err := target.FileSystem.Read(ctx, file.Path)
	if err != nil {
		return nil, fmt.Errorf("读取产物「%s」（%s，%s）失败：%w", file.Name, file.MemberName, file.ConnectionID, err)
	}
	defer result.Reader.Close()
	// Content is fetched only on demand and bounded to avoid flooding Agent context.
	if offset > 0 {
		if _, err = io.CopyN(io.Discard, result.Reader, offset); err != nil {
			return nil, fmt.Errorf("产物读取偏移超出文件范围：%w", err)
		}
	}
	data, err := io.ReadAll(io.LimitReader(result.Reader, limit+1))
	if err != nil {
		return nil, err
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	media := result.ContentType
	if media == "" {
		media = http.DetectContentType(data)
	}
	encoding, content := "utf-8", string(data)
	if strings.HasPrefix(media, "image/") || !utf8.Valid(data) || strings.ContainsRune(content, '\x00') {
		encoding, content = "base64", base64.StdEncoding.EncodeToString(data)
	}
	value := map[string]any{"artifact": file, "content": content, "encoding": encoding, "content_type": media, "size": result.Info.Size, "offset": offset, "bytes_read": len(data), "truncated": truncated}
	if truncated {
		value["next_offset"] = offset + int64(len(data))
	}
	return map[string]any{"namespace": namespace, "key": key, "exists": true, "value": value}, nil
}

func readNumber(payload map[string]any, key string, defaultValue, max int64) (int64, error) {
	raw, ok := payload[key]
	if !ok {
		return defaultValue, nil
	}
	var n int64
	switch value := raw.(type) {
	case json.Number:
		var err error
		n, err = value.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s 必须为整数字节数", key)
		}
	case float64:
		if value < 0 || value > float64(max) || value != float64(int64(value)) {
			return 0, fmt.Errorf("%s 必须为有效的非负整数字节数", key)
		}
		n = int64(value)
	case int:
		n = int64(value)
	case int64:
		n = value
	default:
		return 0, fmt.Errorf("%s 必须为整数字节数", key)
	}
	if n < 0 || n > max {
		return 0, fmt.Errorf("%s 超出允许范围", key)
	}
	return n, nil
}
