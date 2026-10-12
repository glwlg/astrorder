package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultOpenVikingURL = "http://127.0.0.1:1933"
	defaultClientTimeout = 3 * time.Second
	defaultTreeTimeout   = 10 * time.Second
	maxOpenVikingBody    = 16_000_000
)

// OpenVikingClientOptions defines the exact upstream endpoint and bounded request behavior.
type OpenVikingClientOptions struct {
	BaseURL     string
	APIKey      string
	Timeout     time.Duration
	TreeTimeout time.Duration
	HTTPClient  *http.Client
}

// OpenVikingClient is the read/query-only OpenViking REST client used by the memory service.
type OpenVikingClient struct {
	baseURL     *url.URL
	apiKey      string
	timeout     time.Duration
	treeTimeout time.Duration
	httpClient  *http.Client
}

// HealthResult intentionally reports transport and response failures as data, matching the legacy API.
type HealthResult struct {
	Healthy bool    `json:"healthy"`
	Status  string  `json:"status"`
	Version *string `json:"version"`
	Error   string  `json:"error,omitempty"`
}

func NewOpenVikingClient(options OpenVikingClientOptions) (*OpenVikingClient, error) {
	raw := strings.TrimSpace(options.BaseURL)
	if raw == "" {
		return nil, errors.New("OpenViking base URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("OpenViking base URL must be a complete http or https URL without userinfo")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultClientTimeout
	}
	treeTimeout := options.TreeTimeout
	if treeTimeout <= 0 {
		treeTimeout = defaultTreeTimeout
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &OpenVikingClient{
		baseURL: parsed, apiKey: options.APIKey, timeout: timeout, treeTimeout: treeTimeout, httpClient: client,
	}, nil
}

func (c *OpenVikingClient) Health(ctx context.Context) HealthResult {
	var payload map[string]any
	status, err := c.request(ctx, c.timeout, http.MethodGet, "/health", nil, nil, false, &payload)
	if err != nil {
		return HealthResult{Healthy: false, Status: "unreachable", Error: c.sanitize(err.Error())}
	}
	if status != http.StatusOK {
		return HealthResult{Healthy: false, Status: fmt.Sprintf("HTTP %d", status)}
	}
	result := HealthResult{Healthy: true, Status: "ok"}
	if value, ok := payload["status"].(string); ok && value != "" {
		result.Status = value
	}
	if value, ok := payload["version"].(string); ok {
		result.Version = &value
	}
	return result
}

func (c *OpenVikingClient) ListDir(ctx context.Context, uri string) ([]map[string]any, error) {
	query := url.Values{"uri": {uri}, "recursive": {"false"}}
	var items []map[string]any
	status, err := c.request(ctx, c.timeout, http.MethodGet, "/api/v1/fs/ls", query, nil, true, resultReceiver(&items))
	return items, c.statusError(status, err)
}

func (c *OpenVikingClient) Tree(ctx context.Context, uri string) ([]map[string]any, error) {
	query := url.Values{"uri": {uri}, "level_limit": {"10"}, "node_limit": {"5000"}}
	var items []map[string]any
	status, err := c.request(ctx, c.treeTimeout, http.MethodGet, "/api/v1/fs/tree", query, nil, true, resultReceiver(&items))
	return items, c.statusError(status, err)
}

func (c *OpenVikingClient) ReadContent(ctx context.Context, uri string) (string, error) {
	query := url.Values{"uri": {uri}}
	var content string
	status, err := c.request(ctx, c.timeout, http.MethodGet, "/api/v1/content/read", query, nil, true, resultReceiver(&content))
	return content, c.statusError(status, err)
}

// Search keeps limit client-side for HTTP compatibility; the legacy OpenViking request does not transmit it.
func (c *OpenVikingClient) Search(ctx context.Context, queryText, scope string, limit int) ([]map[string]any, error) {
	payload := map[string]any{"query": queryText}
	if scope != "" {
		payload["scope"] = scope
	}
	var items []map[string]any
	var envelope struct {
		Result struct {
			Memories json.RawMessage `json:"memories"`
		} `json:"result"`
	}
	status, err := c.request(ctx, c.timeout, http.MethodPost, "/api/v1/search/search", nil, payload, true, &envelope)
	if err = c.statusError(status, err); err != nil {
		return nil, err
	}
	if len(envelope.Result.Memories) == 0 || string(envelope.Result.Memories) == "null" {
		return []map[string]any{}, nil
	}
	if err := decodeUseNumber(envelope.Result.Memories, &items); err != nil {
		return nil, fmt.Errorf("decode OpenViking result: invalid JSON: %w", err)
	}
	return items, nil
}

func resultReceiver(target any) any {
	return &struct {
		Result any `json:"result"`
	}{Result: target}
}

func (c *OpenVikingClient) request(ctx context.Context, timeout time.Duration, method, endpoint string, query url.Values, payload any, authenticated bool, target any) (int, error) {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + endpoint
	requestURL.RawQuery = query.Encode()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("encode OpenViking request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL.String(), body)
	if err != nil {
		return 0, fmt.Errorf("build OpenViking request: %w", err)
	}
	if authenticated {
		request.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("OpenViking request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxOpenVikingBody))
		return response.StatusCode, nil
	}
	limited := io.LimitReader(response.Body, maxOpenVikingBody+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return response.StatusCode, fmt.Errorf("read OpenViking response: %w", err)
	}
	if len(raw) > maxOpenVikingBody {
		return response.StatusCode, errors.New("OpenViking response exceeds size limit")
	}
	if err := decodeUseNumber(raw, target); err != nil {
		return response.StatusCode, fmt.Errorf("decode OpenViking response: invalid JSON: %w", err)
	}
	return response.StatusCode, nil
}

func decodeUseNumber(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (c *OpenVikingClient) statusError(status int, err error) error {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errors.New(c.sanitize(err.Error()))
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("OpenViking HTTP %d", status)
	}
	return nil
}

func (c *OpenVikingClient) sanitize(value string) string {
	if c.apiKey != "" {
		value = strings.ReplaceAll(value, c.apiKey, "[REDACTED]")
	}
	return value
}
