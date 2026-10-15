package projection

import (
	"crypto/sha256"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"
)

// CodexUserAttachments preserves native image blocks for both live messages and
// history. File URLs use the existing authenticated, target-aware file endpoint.
// No native image bytes or transcript copies are persisted here.
func CodexUserAttachments(content any, sessionID, itemID string) []any {
	attachments := []any{}
	parts, _ := content.([]any)
	for index, part := range parts {
		block, ok := part.(map[string]any)
		if !ok {
			continue
		}
		var href, name, media string
		switch block["type"] {
		case "image":
			href, _ = block["url"].(string)
			if href == "" {
				continue
			}
			media = "image/png"
			name = fmt.Sprintf("image-%d.png", index+1)
			if strings.HasPrefix(href, "data:") {
				header, _, found := strings.Cut(strings.TrimPrefix(href, "data:"), ";base64,")
				if !found || !rasterImageType(header) {
					continue
				}
				media = header
				name = fmt.Sprintf("image-%d.%s", index+1, strings.TrimPrefix(media, "image/"))
			} else {
				parsed, err := url.Parse(href)
				if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
					continue
				}
				if extension := path.Ext(parsed.Path); rasterImageType(mime.TypeByExtension(extension)) {
					media = mime.TypeByExtension(extension)
					name = path.Base(parsed.Path)
				}
			}
		case "localImage":
			nativePath, _ := block["path"].(string)
			if nativePath == "" || strings.ContainsAny(nativePath, "\x00\r\n") {
				continue
			}
			name = path.Base(strings.ReplaceAll(nativePath, `\`, "/"))
			media = mime.TypeByExtension(path.Ext(name))
			if !rasterImageType(media) {
				media = "image/png"
			}
			query := url.Values{"path": {nativePath}, "session_id": {sessionID}}
			href = "/api/v1/files/raw?" + query.Encode()
		default:
			continue
		}
		identity := sha256.Sum256([]byte(itemID + "\x00" + fmt.Sprint(index) + "\x00" + href))
		attachments = append(attachments, map[string]any{"id": fmt.Sprintf("codex-image-%x", identity[:16]), "name": name, "media_type": media, "url": href})
	}
	return attachments
}

func rasterImageType(media string) bool {
	switch media {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}
