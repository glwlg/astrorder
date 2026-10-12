package agentremote

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
	DefaultBaseURL    = "http://127.0.0.1:30001"
	InvokePath        = "/api/v1/agent/invoke"
	DefaultTimeout    = 60 * time.Second
	MaxBusinessBytes  = 16_000_000 // Exact 16,000,000 bytes
	MaxErrorBodyBytes = 64 << 10   // 64KB
)

// EnvLookup allows injecting environment variable discovery.
type EnvLookup func(string) (string, bool)

// FileReader allows injecting token file reading.
type FileReader func(string) ([]byte, error)

// Config holds client resolution settings.
type Config struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// ValidateURL checks scheme is http/https and forbids userinfo, query, or fragment.
func ValidateURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("empty URL")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("URL host is required")
	}
	if u.User != nil {
		return nil, errors.New("URL userinfo credentials forbidden")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("URL query and fragment forbidden")
	}
	return u, nil
}

// ResolveConfig discovers BaseURL and Token from env and token files.
func ResolveConfig(lookup EnvLookup, readFile FileReader) (Config, error) {
	if lookup == nil {
		lookup = func(k string) (string, bool) { return "", false }
	}
	baseURL := DefaultBaseURL
	if u, ok := lookup("ASTRORDER_AGENT_URL"); ok && strings.TrimSpace(u) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(u), "/")
	}

	token := ""
	for _, key := range []string{"ASTRORDER_AGENT_TOKEN", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET"} {
		if val, ok := lookup(key); ok && strings.TrimSpace(val) != "" {
			token = strings.TrimSpace(val)
			break
		}
	}
	if token == "" {
		tokenFile := ".token"
		if tf, ok := lookup("ASTRORDER_TOKEN_FILE"); ok && strings.TrimSpace(tf) != "" {
			tokenFile = strings.TrimSpace(tf)
		}
		if readFile != nil {
			if data, err := readFile(tokenFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
				token = string(bytes.TrimSpace(data))
			}
		}
	}
	if token == "" {
		return Config{}, errors.New("Missing Astrorder agent token (ASTRORDER_AGENT_TOKEN or .token)")
	}

	client := &http.Client{
		Timeout: DefaultTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Strict: never leak bearer token on redirects
		},
	}

	return Config{
		BaseURL:    baseURL,
		Token:      token,
		HTTPClient: client,
	}, nil
}

// Client executes capabilities against the remote Astrorder App Server.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new Client with validated configuration.
func NewClient(cfg Config) (*Client, error) {
	u, err := ValidateURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if cfg.Token == "" {
		return nil, errors.New("token is required")
	}
	var client *http.Client
	if cfg.HTTPClient != nil {
		c := *cfg.HTTPClient
		client = &c
	} else {
		client = &http.Client{
			Timeout: DefaultTimeout,
		}
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	cleanBase := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if u.Path != "" && u.Path != "/" {
		cleanBase += strings.TrimRight(u.Path, "/")
	}
	return &Client{
		baseURL:    cleanBase,
		token:      cfg.Token,
		httpClient: client,
	}, nil
}

// Invoke calls /api/v1/agent/invoke with capability name and JSON payload.
func (c *Client) Invoke(ctx context.Context, capability string, payload map[string]any) (any, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{
		"capability": capability,
		"input":      payload,
	}); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqBody := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(reqBody) > MaxBusinessBytes {
		return nil, fmt.Errorf("request payload exceeds maximum limit of %d bytes", MaxBusinessBytes)
	}

	endpoint := c.baseURL + InvokePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes))
		detail := strings.TrimSpace(string(errBytes))
		return nil, fmt.Errorf("Astrorder agent API %d: %s", resp.StatusCode, detail)
	}

	limited := io.LimitReader(resp.Body, MaxBusinessBytes+1)
	respData, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(respData) > MaxBusinessBytes {
		return nil, fmt.Errorf("response exceeded limit of %d bytes", MaxBusinessBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(respData))
	dec.UseNumber()
	var result any
	if err := dec.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("response contained trailing data")
	}
	return result, nil
}
