package integrations

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxHTMLFetchBytes = 512 * 1024
	maxIconFetchBytes = 128 * 1024
	metaUserAgent     = "Mozilla/5.0 (compatible; AstrorderIntegrationFetcher/1.0)"
)

// SiteMeta holds auto-discovered title and favicon data for an external URL.
type SiteMeta struct {
	Title         string `json:"title"`
	IconDataURL   string `json:"icon_data_url"`
	IconSourceURL string `json:"icon_source_url"`
}

var (
	titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	linkRe  = regexp.MustCompile(`(?is)<link\b(?:[^>"']|"[^"]*"|'[^']*')*>`)
	attrRe  = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)
)

type iconCandidate struct {
	href     string
	priority int
}

// FetchSiteMeta fetches an external web page's title and best available icon,
// encoding the icon as a data: URI so the sidebar renders it reliably without
// CORS or intranet connectivity issues.
func FetchSiteMeta(ctx context.Context, customClient *http.Client, rawURL string) (SiteMeta, error) {
	targetURL, err := url.Parse(rawURL)
	if err != nil {
		return SiteMeta{}, err
	}
	jar, _ := cookiejar.New(nil)
	var client *http.Client
	if customClient == nil {
		client = defaultFetchClient(false, jar)
	} else {
		copied := *customClient
		if copied.Jar == nil {
			copied.Jar = jar
		}
		client = &copied
	}

	var meta SiteMeta
	var candidates []iconCandidate
	baseURL := targetURL

	resp, err := doGetWithPrivateTLSFallback(ctx, client, customClient == nil, jar, rawURL)
	if err == nil {
		defer resp.Body.Close()
		if resp.Request != nil && resp.Request.URL != nil {
			baseURL = resp.Request.URL
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			if strings.Contains(contentType, "html") || strings.Contains(contentType, "xml") || contentType == "" {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTMLFetchBytes))
				meta.Title = extractHTMLTitle(string(body))
				candidates = extractIconCandidates(string(body), baseURL)
			}
		}
	}

	// Append common root icon paths as low-priority fallback candidates.
	if baseURL != nil && baseURL.Scheme != "" && baseURL.Host != "" {
		for idx, path := range []string{"/favicon.svg", "/favicon.ico", "/apple-touch-icon.png"} {
			fallback := (&url.URL{Scheme: baseURL.Scheme, Host: baseURL.Host, Path: path}).String()
			candidates = append(candidates, iconCandidate{href: fallback, priority: 100 + idx})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].priority < candidates[j].priority
	})

	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand.href == "" || seen[cand.href] {
			continue
		}
		seen[cand.href] = true
		if strings.HasPrefix(strings.ToLower(cand.href), "data:image/") {
			if normalized := normalizeInlineIconDataURL(cand.href); normalized != "" {
				meta.IconDataURL = normalized
				meta.IconSourceURL = "inline"
				break
			}
			continue
		}
		dataURL, fetchErr := downloadIconAsDataURL(ctx, client, customClient == nil, jar, cand.href)
		if fetchErr == nil && dataURL != "" {
			meta.IconDataURL = dataURL
			meta.IconSourceURL = cand.href
			break
		}
	}

	if meta.Title == "" && meta.IconDataURL == "" && err != nil {
		return meta, err
	}
	return meta, nil
}

func defaultFetchClient(insecure bool, jar http.CookieJar) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 6 * time.Second}).DialContext,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // only used for private/LAN self-signed fallback
	}
	return &http.Client{
		Timeout:   12 * time.Second,
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

func isPrivateOrLANHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".internal") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func doGetWithPrivateTLSFallback(ctx context.Context, client *http.Client, allowFallback bool, jar http.CookieJar, rawURL string) (*http.Response, error) {
	do := func(c *http.Client) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", metaUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,image/*,*/*;q=0.8")
		return c.Do(req)
	}
	resp, err := do(client)
	if err != nil && allowFallback && strings.HasPrefix(strings.ToLower(rawURL), "https://") && isPrivateOrLANHost(rawURL) {
		return do(defaultFetchClient(true, jar))
	}
	return resp, err
}

func extractHTMLTitle(doc string) string {
	match := titleRe.FindStringSubmatch(doc)
	if len(match) < 2 {
		return ""
	}
	decoded := html.UnescapeString(match[1])
	return strings.Join(strings.Fields(decoded), " ")
}

func extractIconCandidates(doc string, base *url.URL) []iconCandidate {
	var out []iconCandidate
	for _, tag := range linkRe.FindAllString(doc, 64) {
		attrs := parseHTMLAttrs(tag)
		rel := strings.ToLower(strings.TrimSpace(attrs["rel"]))
		href := strings.TrimSpace(html.UnescapeString(attrs["href"]))
		if href == "" || rel == "" {
			continue
		}
		isIcon := false
		priority := 50
		for _, token := range strings.Fields(rel) {
			if token == "icon" || token == "apple-touch-icon" || token == "apple-touch-icon-precomposed" || token == "mask-icon" {
				isIcon = true
			}
		}
		if !isIcon {
			continue
		}
		mimeType := strings.ToLower(strings.TrimSpace(attrs["type"]))
		lowerHref := strings.ToLower(href)
		switch {
		case strings.Contains(mimeType, "svg") || strings.HasSuffix(lowerHref, ".svg") || strings.HasPrefix(lowerHref, "data:image/svg+xml"):
			priority = 10
		case strings.Contains(rel, "apple-touch-icon"):
			priority = 20
		case strings.Contains(mimeType, "png") || strings.HasSuffix(lowerHref, ".png"):
			priority = 25
		case strings.Contains(mimeType, "webp") || strings.HasSuffix(lowerHref, ".webp"):
			priority = 30
		default:
			priority = 40
		}

		resolved := href
		if !strings.HasPrefix(lowerHref, "data:") && base != nil {
			if ref, err := url.Parse(href); err == nil {
				u := base.ResolveReference(ref)
				if u.Scheme == "http" || u.Scheme == "https" {
					resolved = u.String()
				} else {
					continue
				}
			} else {
				continue
			}
		}
		out = append(out, iconCandidate{href: resolved, priority: priority})
	}
	return out
}

func parseHTMLAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		key := strings.ToLower(m[1])
		val := m[2]
		if val == "" {
			val = m[3]
		}
		if val == "" {
			val = m[4]
		}
		attrs[key] = val
	}
	return attrs
}

func normalizeInlineIconDataURL(href string) string {
	if len(href) > maxIconBytes {
		return ""
	}
	if strings.Contains(strings.ToLower(href), ";base64,") {
		return href
	}
	comma := strings.Index(href, ",")
	if comma < 0 {
		return ""
	}
	header := href[:comma]
	payload := href[comma+1:]
	if unescaped, err := url.PathUnescape(payload); err == nil {
		payload = unescaped
	}
	mimeType := strings.TrimPrefix(header, "data:")
	if semi := strings.Index(mimeType, ";"); semi >= 0 {
		mimeType = mimeType[:semi]
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return ""
	}
	encoded := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString([]byte(payload))
	if len(encoded) > maxIconBytes {
		return ""
	}
	return encoded
}

func downloadIconAsDataURL(ctx context.Context, client *http.Client, allowFallback bool, jar http.CookieJar, iconURL string) (string, error) {
	resp, err := doGetWithPrivateTLSFallback(ctx, client, allowFallback, jar, iconURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("icon status not ok")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIconFetchBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxIconFetchBytes {
		return "", errors.New("icon empty or too large")
	}
	mimeType := detectIconMIME(resp.Header.Get("Content-Type"), iconURL, data)
	if mimeType == "" {
		return "", errors.New("not an image")
	}
	encoded := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	if len(encoded) > maxIconBytes {
		return "", errors.New("encoded icon too large")
	}
	return encoded, nil
}

func detectIconMIME(headerCT, iconURL string, data []byte) string {
	ct := strings.ToLower(strings.TrimSpace(headerCT))
	if semi := strings.Index(ct, ";"); semi >= 0 {
		ct = strings.TrimSpace(ct[:semi])
	}
	if strings.Contains(ct, "html") {
		return ""
	}
	trimmed := bytes.TrimSpace(data)
	lowerPrefix := bytes.ToLower(trimmed)
	if len(lowerPrefix) > 512 {
		lowerPrefix = lowerPrefix[:512]
	}
	if bytes.HasPrefix(lowerPrefix, []byte("<svg")) ||
		(bytes.HasPrefix(lowerPrefix, []byte("<?xml")) && bytes.Contains(lowerPrefix, []byte("<svg"))) ||
		(bytes.HasPrefix(lowerPrefix, []byte("<!--")) && bytes.Contains(lowerPrefix, []byte("<svg"))) {
		return "image/svg+xml"
	}
	if len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 && (data[2] == 0x01 || data[2] == 0x02) && data[3] == 0x00 {
		return "image/x-icon"
	}
	detected := http.DetectContentType(data)
	if strings.HasPrefix(detected, "image/") {
		return detected
	}
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	lowerURL := strings.ToLower(iconURL)
	if strings.HasSuffix(lowerURL, ".svg") && bytes.Contains(bytes.ToLower(trimmed), []byte("<svg")) {
		return "image/svg+xml"
	}
	return ""
}
