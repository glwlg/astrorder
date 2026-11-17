package botgroups

import (
	"context"
	"encoding/json"
	"errors"
)

// Resolve immutable metadata on the server; never accept client URLs or paths.
func (s *Service) messageAttachments(ctx context.Context, raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil || len(ids) > 32 {
		return nil, errors.New("群聊最多可发送 32 张图片")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 256 || seen[id] {
			return nil, errors.New("群聊图片标识无效或重复")
		}
		seen[id] = true
	}
	rows, err := s.options.Store.AttachmentRows(ctx, ids)
	if err != nil {
		return nil, errors.New("群聊图片不存在，请重新上传")
	}
	for _, row := range rows {
		switch row["media_type"] {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return nil, errors.New("群聊支持 PNG、JPEG、GIF 和 WebP 图片")
		}
	}
	return rows, nil
}

func deliveryAttachments(messages []Message) []map[string]any {
	var result []map[string]any
	seen := map[string]bool{}
	for _, message := range messages {
		for _, attachment := range message.Attachments {
			id, _ := attachment["id"].(string)
			if !seen[id] {
				seen[id] = true
				result = append(result, attachment)
			}
		}
	}
	return result
}
