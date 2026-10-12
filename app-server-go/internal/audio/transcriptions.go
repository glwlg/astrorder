package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const (
	MaxAudioBytes = 25 * 1024 * 1024 // 25 MB
)

var allowedAudioTypes = map[string]bool{
	"audio/wav":   true,
	"audio/x-wav": true,
	"audio/webm":  true,
	"audio/ogg":   true,
	"audio/mp4":   true,
	"audio/mpeg":  true,
	"audio/aac":   true,
	"audio/flac":  true,
	"audio/x-m4a": true,
}

// OpenCodexGatewayProfile represents the profile resolved from analytics:llm_gateway
type OpenCodexGatewayProfile struct {
	InferenceURL string
	APIKey       string
}

func ResolveOpenCodexProfile(ctx context.Context, st *store.Store) OpenCodexGatewayProfile {
	var profile OpenCodexGatewayProfile
	envToken := os.Getenv("OPENCODEX_API_AUTH_TOKEN")

	if st != nil {
		pref, err := st.GetPreference(ctx, "analytics:llm_gateway")
		if err == nil && pref.Exists && pref.Value != nil {
			if m, ok := pref.Value.(map[string]any); ok {
				gwType, _ := m["gateway_type"].(string)
				// If gateway_type is opencodex, check root fields
				if strings.ToLower(gwType) == "opencodex" {
					if inf, ok := m["inference_url"].(string); ok && inf != "" {
						profile.InferenceURL = inf
					}
					if key, ok := m["api_key"].(string); ok && key != "" {
						profile.APIKey = key
					}
				}
				// Also check gateway_profiles.opencodex
				if profiles, ok := m["gateway_profiles"].(map[string]any); ok {
					if ocx, ok := profiles["opencodex"].(map[string]any); ok {
						if profile.InferenceURL == "" {
							if inf, ok := ocx["inference_url"].(string); ok && inf != "" {
								profile.InferenceURL = inf
							}
						}
						if profile.APIKey == "" {
							if key, ok := ocx["api_key"].(string); ok && key != "" {
								profile.APIKey = key
							}
						}
					}
				}
			}
		}
	}

	// Fallback to environment variable if key is empty
	if profile.APIKey == "" {
		profile.APIKey = envToken
	}

	// If inference URL is still empty and default is needed
	if profile.InferenceURL == "" {
		profile.InferenceURL = "https://llm.651971564.xyz/v1"
	}

	profile.InferenceURL = strings.TrimRight(profile.InferenceURL, "/")
	return profile
}

func (s *Service) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}

	// Read multipart form. Max size bounded by MaxAudioBytes + overhead (10MB for headers/form).
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid multipart form")
		return
	}

	var fileData []byte
	var fileName string
	var fileMime string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Error reading multipart form")
			return
		}
		if part.FormName() == "file" {
			fileName = part.FileName()
			rawMime := part.Header.Get("Content-Type")
			fileMime = strings.ToLower(strings.TrimSpace(strings.Split(rawMime, ";")[0]))
			if !allowedAudioTypes[fileMime] {
				writeJSONError(w, http.StatusUnsupportedMediaType, "不支持此音频格式。")
				return
			}

			// Read up to MaxAudioBytes + 1 to detect oversize
			limitedReader := io.LimitReader(part, int64(MaxAudioBytes+1))
			data, err := io.ReadAll(limitedReader)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "Error reading audio file")
				return
			}
			fileData = data
			break
		}
	}

	if len(fileData) == 0 {
		writeJSONError(w, http.StatusBadRequest, "录音为空，请重新录音。")
		return
	}
	if len(fileData) > MaxAudioBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "音频不能超过 25 MB。")
		return
	}

	profile := ResolveOpenCodexProfile(r.Context(), s.options.Store)
	if profile.APIKey == "" || profile.InferenceURL == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "请先在网关设置中配置 OpenCodex 推理地址和数据密钥。")
		return
	}

	targetURL := profile.InferenceURL + "/audio/transcriptions"

	// Construct upstream request: multipart/form-data with file and model=gpt-4o-transcribe
	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	_ = writer.WriteField("model", "gpt-4o-transcribe")

	fn := fileName
	if fn == "" {
		fn = "recording.wav"
	}
	partHeader := make(map[string][]string)
	partHeader["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, filepath.Base(fn))}
	partHeader["Content-Type"] = []string{fileMime}

	partWriter, err := writer.CreatePart(partHeader)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to create multipart form")
		return
	}
	if _, err := partWriter.Write(fileData); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to write multipart form")
		return
	}
	if err := writer.Close(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to finalize multipart form")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bodyBuf)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to create upstream request")
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-OpenCodex-API-Key", profile.APIKey)

	client := s.options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 120 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // Do not follow redirects preserving credentials
			},
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) || (r.Context().Err() == nil && strings.Contains(err.Error(), "timeout")) {
			writeJSONError(w, http.StatusGatewayTimeout, "语音转写超时，请重试。")
			return
		}
		writeJSONError(w, http.StatusBadGateway, "无法连接语音网关，请检查网络和网关配置。")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("语音网关返回 HTTP %d，请检查网关权限或稍后重试。", resp.StatusCode))
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "语音网关未返回有效的转写文本。")
		return
	}

	var result struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || result.Text == nil {
		writeJSONError(w, http.StatusBadGateway, "语音网关未返回有效的转写文本。")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"text": *result.Text})
}
