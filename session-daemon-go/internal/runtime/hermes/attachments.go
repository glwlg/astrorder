package hermes

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode"
)

func validateAttachments(values []any) error {
	if len(values) > 32 {
		return fmt.Errorf("too many native attachments")
	}
	allowed := map[string]bool{"application/json": true, "application/octet-stream": true, "application/pdf": true, "audio/aac": true, "audio/mp4": true, "audio/mpeg": true, "audio/ogg": true, "audio/wav": true, "audio/webm": true, "audio/x-wav": true, "image/gif": true, "image/jpeg": true, "image/png": true, "image/webp": true, "text/csv": true, "text/plain": true, "text/markdown": true}
	total := 0
	for _, value := range values {
		attachment, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid native attachment")
		}
		name, _ := attachment["name"].(string)
		media, _ := attachment["media_type"].(string)
		encoded, ok := attachment["content_base64"].(string)
		if name == "" || len(name) > 256 || name == "." || name == ".." || strings.ContainsAny(name, "/\\:") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return fmt.Errorf("invalid native attachment name")
		}
		if !allowed[media] {
			return fmt.Errorf("unsupported native attachment media type")
		}
		if !ok || len(encoded) > 13981016 {
			return fmt.Errorf("invalid native attachment encoding")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("invalid native attachment encoding")
		}
		total += len(data)
		if total > 10*1024*1024 {
			return fmt.Errorf("total native attachment size exceeds limit")
		}
	}
	return nil
}

func (a *Adapter) rollbackImages(ctx context.Context, handle string, paths []string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var failures []string
	for _, path := range paths {
		if _, err := a.rpc(cleanup, "image.detach", map[string]any{"session_id": handle, "path": path}); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("native attachment rollback failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (a *Adapter) attachInputs(ctx context.Context, handle, text string, values []any) (string, error) {
	images := []string{}
	refs := []string{}
	for _, value := range values {
		attachment := value.(map[string]any)
		name, media, encoded := attachment["name"].(string), attachment["media_type"].(string), attachment["content_base64"].(string)
		var result map[string]any
		var err error
		if strings.HasPrefix(media, "image/") {
			result, err = a.rpc(ctx, "image.attach_bytes", map[string]any{"session_id": handle, "filename": name, "content_base64": encoded})
		} else {
			result, err = a.rpc(ctx, "file.attach", map[string]any{"session_id": handle, "name": name, "data_url": "data:" + media + ";base64," + encoded})
		}
		if err == nil && result["attached"] != true {
			err = fmt.Errorf("native attachment was not confirmed")
		}
		if err != nil {
			if rollbackErr := a.rollbackImages(ctx, handle, images); rollbackErr != nil {
				return "", fmt.Errorf("attachment failed: %w; %v", err, rollbackErr)
			}
			return "", fmt.Errorf("native attachment failed before prompt submission: %w", err)
		}
		if strings.HasPrefix(media, "image/") {
			if path, ok := result["path"].(string); ok && path != "" {
				images = append(images, path)
			}
		} else if ref, ok := result["ref_text"].(string); ok && ref != "" {
			refs = append(refs, ref)
		}
	}
	if len(refs) > 0 {
		text = strings.Join(append(refs, text), "\n")
	}
	return text, nil
}
