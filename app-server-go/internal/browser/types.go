// Package browser implements native browser automation capabilities (F13),
// CDP client management, session tab isolation, upstream browser engines,
// browser launch/proxying, and agent capability bindings.
package browser

import (
	"context"
	"net/http"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

const (
	defaultDevToolsPort = 9337
	sessionPrefix       = "astrorder:"
)

// Allowed CDP domains for agent CDP capability.
var allowedCDPDomains = map[string]bool{
	"Runtime":     true,
	"DOM":         true,
	"CSS":         true,
	"Console":     true,
	"Log":         true,
	"Network":     true,
	"Performance": true,
	"Page":        true,
}

// BrowserAuthorizer checks browser request authentication.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Publisher publishes events to the event hub.
type Publisher interface {
	Publish(context.Context, string, map[string]any) error
}

// Options configure the browser handler and manager.
type Options struct {
	Store      *store.Store
	Authorizer BrowserAuthorizer
	Publisher  Publisher
	CDPURL     string // Optional override for DevTools base URL, e.g. "http://127.0.0.1:9337"
	NoAutoExec bool   // If true, never launch browser binary automatically
}

// Tab represents an open tab in the browser.
type Tab struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// TabsInfo holds tabs information for a session.
type TabsInfo struct {
	SessionKey     string `json:"session_key"`
	ActiveTargetID string `json:"active_target_id,omitempty"`
	Tabs           []Tab  `json:"tabs"`
}

// Snapshot represents a captured mirror state.
type Snapshot struct {
	URL            string  `json:"url"`
	Title          string  `json:"title"`
	Screenshot     string  `json:"screenshot"`
	MimeType       string  `json:"mime_type"`
	Revision       int64   `json:"revision"`
	CapturedAt     float64 `json:"captured_at"`
	SessionKey     string  `json:"session_key"`
	TargetID       string  `json:"target_id,omitempty"`
	ActiveTargetID string  `json:"active_target_id,omitempty"`
	Tabs           []Tab   `json:"tabs"`
	ErrorCount     int     `json:"error_count"`
}

// LogEntry represents a console/network/error log.
type LogEntry struct {
	Type string  `json:"type"`
	Text string  `json:"text"`
	Time float64 `json:"time"`
}

// Diagnostics holds diagnostic logs for a tab.
type Diagnostics struct {
	SessionKey string     `json:"session_key"`
	TargetID   string     `json:"target_id"`
	Logs       []LogEntry `json:"logs"`
}

// PageContent represents extracted textual page content.
type PageContent struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Text       string `json:"text"`
	SessionKey string `json:"session_key"`
	TargetID   string `json:"target_id"`
}
