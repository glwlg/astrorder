// Package auth implements Astrorder browser and connector authentication.
package auth

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/config"
)

const CookieName = "astrorder_session"

type Principal string

const (
	PrincipalBrowser   Principal = "browser"
	PrincipalConnector Principal = "connector"
)

// OriginPreferences is the dynamic network preference projection needed by
// authentication. Its implementation remains owned by the future store layer.
type OriginPreferences struct {
	AllowedOrigins []string
	PublicURL      string
}

// DynamicOriginLookup supplies current network preferences without making the
// P4 foundation a persistence owner.
type DynamicOriginLookup interface {
	LookupOriginPreferences(context.Context) (OriginPreferences, error)
}

// OriginLookupFunc adapts a function to DynamicOriginLookup.
type OriginLookupFunc func(context.Context) (OriginPreferences, error)

func (f OriginLookupFunc) LookupOriginPreferences(ctx context.Context) (OriginPreferences, error) {
	return f(ctx)
}

// Error is an authentication failure with the existing HTTP status/detail
// contract.
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string { return e.Detail }

// Authenticator is immutable and safe for concurrent use when its injected
// lookup is safe for concurrent use.
type Authenticator struct {
	config config.Config
	lookup DynamicOriginLookup
}

func New(cfg config.Config, lookup DynamicOriginLookup) *Authenticator {
	return &Authenticator{config: cfg, lookup: lookup}
}

func (a *Authenticator) AuthorizeBrowser(request *http.Request) *Error {
	if a.config.BrowserSecret == "" {
		return &Error{Status: http.StatusServiceUnavailable, Detail: "Authentication is not configured"}
	}
	if !a.IsAllowedOrigin(request.Context(), request.Header.Get("Origin"), request.Host) {
		return &Error{Status: http.StatusForbidden, Detail: "Origin is not allowed"}
	}
	if !matches(requestToken(request), a.config.BrowserSecret) {
		return &Error{Status: http.StatusUnauthorized, Detail: "Authentication required"}
	}
	return nil
}

// AuthorizeConnector accepts only a connector Bearer credential and validates
// Origin. This is the external Connector WebSocket contract.
func (a *Authenticator) AuthorizeConnector(request *http.Request) *Error {
	if a.config.ConnectorSecret == "" {
		return &Error{Status: http.StatusServiceUnavailable, Detail: "Authentication is not configured"}
	}
	if !a.IsAllowedOrigin(request.Context(), request.Header.Get("Origin"), "") {
		return &Error{Status: http.StatusForbidden, Detail: "Origin is not allowed"}
	}
	if !matches(bearer(request.Header), a.config.ConnectorSecret) {
		return &Error{Status: http.StatusUnauthorized, Detail: "Authentication required"}
	}
	return nil
}

// AuthorizeAgent preserves the existing cookie/Bearer/query credential order.
// Connector credentials take precedence; browser credentials with an Origin
// additionally receive browser origin validation.
func (a *Authenticator) AuthorizeAgent(request *http.Request) (Principal, *Error) {
	token := requestToken(request)
	if matches(token, a.config.ConnectorSecret) {
		return PrincipalConnector, nil
	}
	if matches(token, a.config.BrowserSecret) {
		if request.Header.Get("Origin") != "" && !a.IsAllowedOrigin(request.Context(), request.Header.Get("Origin"), request.Host) {
			return "", &Error{Status: http.StatusForbidden, Detail: "Origin is not allowed"}
		}
		return PrincipalBrowser, nil
	}
	if a.config.BrowserSecret == "" && a.config.ConnectorSecret == "" {
		return "", &Error{Status: http.StatusServiceUnavailable, Detail: "Authentication is not configured"}
	}
	return "", &Error{Status: http.StatusUnauthorized, Detail: "Authentication required"}
}

func (a *Authenticator) IsBrowserAuthenticated(request *http.Request) bool {
	return a.config.BrowserSecret != "" && matches(requestToken(request), a.config.BrowserSecret)
}

func (a *Authenticator) IsBrowserBearer(request *http.Request) bool {
	return a.config.BrowserSecret != "" && matches(bearer(request.Header), a.config.BrowserSecret)
}

func (a *Authenticator) IsAllowedOrigin(ctx context.Context, origin, host string) bool {
	if origin == "" {
		return true
	}
	normalized := normalizeOrigin(origin)
	for _, allowed := range a.allowedOrigins(ctx) {
		if normalized == normalizeOrigin(allowed) {
			return true
		}
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if host != "" && strings.EqualFold(parsed.Host, host) {
		return true
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || (!pythonIsPrivate(ip) && !ip.IsLoopback()) {
		return false
	}
	port, hasPort := originPort(parsed)
	if !hasPort {
		return true
	}
	if port == 30001 || port == 30002 {
		return true
	}
	_, configuredPort, err := net.SplitHostPort(a.config.Listen)
	return err == nil && configuredPort == strconv.Itoa(port)
}

// IsCORSOrigin follows Starlette's configured P4 CORS compatibility rule:
// explicit origins plus HTTP(S) localhost, 127.0.0.1, and RFC1918 IPv4 on any
// port. It is intentionally narrower than browser authorization.
func (a *Authenticator) IsCORSOrigin(ctx context.Context, origin, _ string) bool {
	normalized := normalizeOrigin(origin)
	for _, allowed := range a.allowedOrigins(ctx) {
		if normalized == normalizeOrigin(allowed) {
			return true
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	hostname := parsed.Hostname()
	if strings.EqualFold(hostname, "localhost") || hostname == "127.0.0.1" {
		return true
	}
	ip := net.ParseIP(hostname)
	if ip == nil || ip.To4() == nil {
		return false
	}
	for _, block := range mustIPNetworks("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16") {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *Authenticator) allowedOrigins(ctx context.Context) []string {
	origins := append([]string(nil), a.config.AllowedOrigins...)
	if a.lookup == nil {
		return origins
	}
	preferences, err := a.lookup.LookupOriginPreferences(ctx)
	if err != nil {
		return origins
	}
	origins = append(origins, preferences.AllowedOrigins...)
	if strings.TrimSpace(preferences.PublicURL) != "" {
		origins = append(origins, preferences.PublicURL)
	}
	return origins
}

func requestToken(request *http.Request) string {
	if cookie, err := request.Cookie(CookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if token := bearer(request.Header); token != "" {
		return token
	}
	return request.URL.Query().Get("token")
}

func bearer(header http.Header) string {
	value := header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(value, "Bearer ")
}

func matches(token, secret string) bool {
	if token == "" || secret == "" || len(token) != len(secret) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

var pythonPrivateNetworks = mustIPNetworks(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.0.170/31",
	"192.0.2.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"255.255.255.255/32",
	"::1/128",
	"::/128",
	"::ffff:0.0.0.0/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"2002::/16",
	"3fff::/20",
	"fc00::/7",
	"fe80::/10",
)

var pythonPrivateExcluded = mustIPNetworks(
	"100.64.0.0/10",
	"192.0.0.9/32",
	"192.0.0.10/32",
	"2001:1::1/128",
	"2001:1::2/128",
	"2001:3::/32",
	"2001:4:112::/48",
	"2001:20::/28",
	"2001:30::/28",
)

func pythonIsPrivate(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if mapped := ip.To4(); mapped != nil {
		ip = mapped
	}
	for _, exception := range pythonPrivateExcluded {
		if len(exception.Mask) != len(ip) {
			continue
		}
		if exception.Contains(ip) {
			return false
		}
	}
	for _, network := range pythonPrivateNetworks {
		// net.IPNet.Contains maps IPv4-mapped IPv6 networks back to IPv4.
		// The ::ffff:0.0.0.0/96 entry would become /0 and match public IPv4.
		// Python classifies mapped addresses using only their IPv4 address.
		if len(network.Mask) != len(ip) {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func mustIPNetworks(values ...string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic("invalid private network table: " + value)
		}
		networks = append(networks, network)
	}
	return networks
}

func normalizeOrigin(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), "/")
}

func originPort(parsed *url.URL) (int, bool) {
	raw := parsed.Port()
	if raw == "" {
		return 0, false
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true
	}
	return port, true
}
