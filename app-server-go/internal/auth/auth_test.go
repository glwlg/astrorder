package auth

import (
	"context"
	"net/http"
	"testing"

	"astrorder.dev/app-server/internal/config"
)

type lookupFunc func(context.Context) (OriginPreferences, error)

func (f lookupFunc) LookupOriginPreferences(ctx context.Context) (OriginPreferences, error) {
	return f(ctx)
}

func testAuthenticator(lookup DynamicOriginLookup) *Authenticator {
	return New(config.Config{
		Listen:          "127.0.0.1:30002",
		BrowserSecret:   "browser-secret",
		ConnectorSecret: "connector-secret",
		AllowedOrigins:  []string{"http://configured.example/"},
	}, lookup)
}

func TestBrowserCredentialPrecedenceMatchesExistingContract(t *testing.T) {
	a := testAuthenticator(nil)
	req, _ := http.NewRequest(http.MethodGet, "http://service.test/private?token=browser-secret", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "wrong-cookie"})
	if a.IsBrowserAuthenticated(req) {
		t.Fatal("a non-empty invalid cookie must take precedence over bearer and query credentials")
	}

	for name, configure := range map[string]func(*http.Request){
		"bearer": func(req *http.Request) { req.Header.Set("Authorization", "Bearer browser-secret") },
		"query":  func(*http.Request) {},
	} {
		t.Run("empty cookie falls through to "+name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "http://service.test/private?token=browser-secret", nil)
			configure(req)
			req.AddCookie(&http.Cookie{Name: CookieName, Value: ""})
			if err := a.AuthorizeBrowser(req); err != nil {
				t.Fatalf("empty cookie blocked %s credential: %v", name, err)
			}
		})
	}
}

func TestAgentEmptyCookieFallsThroughWithoutChangingNonEmptyPrecedence(t *testing.T) {
	a := testAuthenticator(nil)
	req, _ := http.NewRequest(http.MethodGet, "http://service.test/private", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: ""})
	req.Header.Set("Authorization", "Bearer connector-secret")
	principal, failure := a.AuthorizeAgent(req)
	if failure != nil || principal != PrincipalConnector {
		t.Fatalf("empty cookie agent authorization = %q, %v", principal, failure)
	}

	req, _ = http.NewRequest(http.MethodGet, "http://service.test/private", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "invalid"})
	req.Header.Set("Authorization", "Bearer connector-secret")
	if _, failure := a.AuthorizeAgent(req); failure == nil || failure.Status != http.StatusUnauthorized {
		t.Fatalf("non-empty invalid cookie did not retain precedence: %v", failure)
	}
}

func TestBrowserAndConnectorCredentialsRemainSeparate(t *testing.T) {
	a := testAuthenticator(nil)
	browser, _ := http.NewRequest(http.MethodGet, "http://service.test/private", nil)
	browser.Header.Set("Authorization", "Bearer browser-secret")
	if err := a.AuthorizeConnector(browser); err == nil || err.Status != http.StatusUnauthorized {
		t.Fatalf("browser credential authorized connector: %v", err)
	}

	connector, _ := http.NewRequest(http.MethodGet, "http://service.test/private?token=connector-secret", nil)
	connector.Header.Set("Authorization", "Bearer connector-secret")
	if err := a.AuthorizeConnector(connector); err != nil {
		t.Fatalf("connector bearer rejected: %v", err)
	}
	connector.Header.Del("Authorization")
	if err := a.AuthorizeConnector(connector); err == nil {
		t.Fatal("connector query credential must not be accepted")
	}

	agent, _ := http.NewRequest(http.MethodGet, "http://service.test/private", nil)
	agent.Header.Set("Authorization", "Bearer connector-secret")
	principal, err := a.AuthorizeAgent(agent)
	if err != nil || principal != PrincipalConnector {
		t.Fatalf("agent connector authorization = %q, %v", principal, err)
	}
}

func TestOriginValidationSupportsConfiguredDynamicSameHostAndLAN(t *testing.T) {
	lookup := lookupFunc(func(context.Context) (OriginPreferences, error) {
		return OriginPreferences{
			AllowedOrigins: []string{"https://dynamic.example/"},
			PublicURL:      "https://public.example/",
		}, nil
	})
	a := testAuthenticator(lookup)
	cases := []struct {
		origin string
		host   string
		want   bool
	}{
		{"", "service.test", true},
		{"http://configured.example", "service.test", true},
		{"https://dynamic.example", "service.test", true},
		{"https://public.example", "service.test", true},
		{"http://service.test", "service.test", true},
		// Same-host acceptance intentionally preserves the Python baseline even
		// for a non-HTTP scheme; tightening this requires a separate contract change.
		{"file://service.test", "service.test", true},
		{"http://192.168.1.25:30002", "service.test", true},
		{"http://10.1.2.3", "service.test", true},
		{"http://169.254.1.2:30002", "service.test", true},
		{"http://192.0.2.10:30002", "service.test", true},
		{"http://198.51.100.10:30002", "service.test", true},
		{"http://203.0.113.10:30002", "service.test", true},
		{"http://[2001:db8::1]:30002", "service.test", true},
		{"http://[fe80::1]:30002", "service.test", true},
		{"http://[2001:20::1]:30002", "service.test", false},
		{"http://[::ffff:192.0.2.1]:30002", "service.test", true},
		{"http://[::ffff:8.8.8.8]:30002", "service.test", false},
		{"http://[::ffff:100.64.0.1]:30002", "service.test", false},
		{"http://[::ffff:192.0.0.9]:30002", "service.test", false},
		{"http://100.64.0.1:30002", "service.test", false},
		{"http://192.0.0.9:30002", "service.test", false},
		{"http://192.168.1.25:4444", "service.test", false},
		{"https://evil.example", "service.test", false},
	}
	for _, tc := range cases {
		if got := a.IsAllowedOrigin(context.Background(), tc.origin, tc.host); got != tc.want {
			t.Errorf("IsAllowedOrigin(%q, %q) = %v, want %v", tc.origin, tc.host, got, tc.want)
		}
	}
}

func TestMissingBrowserConfigurationFailsClosed(t *testing.T) {
	a := New(config.Config{}, nil)
	req, _ := http.NewRequest(http.MethodGet, "http://service.test/private", nil)
	if err := a.AuthorizeBrowser(req); err == nil || err.Status != http.StatusServiceUnavailable {
		t.Fatalf("AuthorizeBrowser error = %v", err)
	}
}
