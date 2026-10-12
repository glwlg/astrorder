package browser

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	headRegex = regexp.MustCompile(`(?i)(<head[^>]*>)`)
)

// StripProxyHeaders removes CORS, frame embedding, and compression headers.
func StripProxyHeaders(h http.Header) {
	stripList := []string{
		"x-frame-options",
		"content-security-policy",
		"content-security-policy-report-only",
		"content-encoding",
		"content-length",
	}
	for _, key := range stripList {
		h.Del(key)
		h.Del(strings.Title(key))
		h.Del(strings.ToUpper(key))
	}
}

// ProxyBrowser executes a HTTP GET request to targetURL, injects <base href="..."> into HTML,
// strips frame-busting/CSP headers, and responds.
func ProxyBrowser(w http.ResponseWriter, r *http.Request, targetURL string) {
	cleanURL := strings.TrimSpace(targetURL)
	if !strings.HasPrefix(cleanURL, "http://") && !strings.HasPrefix(cleanURL, "https://") {
		cleanURL = "http://" + cleanURL
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, cleanURL, nil)
	if err != nil {
		renderProxyError(w, cleanURL, err)
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		renderProxyError(w, cleanURL, err)
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		renderProxyError(w, cleanURL, err)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/html"
	}

	finalURL := resp.Request.URL.String()
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		htmlStr := string(bodyBytes)
		if headRegex.MatchString(htmlStr) {
			htmlStr = headRegex.ReplaceAllString(htmlStr, fmt.Sprintf("$1\n  <base href=\"%s\">", finalURL))
		} else {
			htmlStr = fmt.Sprintf("<base href=\"%s\">", finalURL) + htmlStr
		}
		bodyBytes = []byte(htmlStr)
	}

	// Copy headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	StripProxyHeaders(w.Header())
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", contentType)

	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(bodyBytes)
}

func renderProxyError(w http.ResponseWriter, cleanURL string, err error) {
	errHTML := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>代理访问异常</title>
<style>body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 32px; background: #0b0f19; color: #f3f4f6; } .card { background: #1f2937; border-radius: 8px; padding: 24px; max-width: 600px; margin: 40px auto; border: 1px solid #374151; } h3 { margin-top: 0; color: #ef4444; } code { background: #111827; padding: 2px 6px; border-radius: 4px; color: #93c5fd; }</style>
</head>
<body>
    <div class="card">
        <h3>无法直接在内嵌代理中加载该网址</h3>
        <p>请求地址: <code>%s</code></p>
        <p>错误详情: %s</p>
        <p style="color: #9ca3af; font-size: 14px; margin-top: 16px;">建议：点击右上角的 🖥️ 设备浏览器图标，直接在系统的原生 Edge / Chrome 中打开该页面。</p>
    </div>
</body>
</html>`, url.PathEscape(cleanURL), err.Error())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(errHTML))
}
