package agentcli

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

const callerKey = "hermes::hermes-session-42"

// Session-scoped coordination calls must transmit the caller's own identity,
// not the server's "anonymous_worker" fallback.
func TestCallerScopeDefaultsOverRealTransport(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		input      string
		field      string
		want       string
		absent     bool
	}{
		{"lock acquire", "swarm.lock.acquire", `{"resource":"git:main"}`, "owner_key", callerKey, false},
		{"lock release", "swarm.lock.release", `{"resource":"git:main"}`, "owner_key", callerKey, false},
		{"sos escalate", "swarm.sos.escalate", `{"reason":"port collision"}`, "key", callerKey, false},
		{"plugin open", "plugins.open", `{"plugin_id":"browser"}`, "session_key", callerKey, false},
		{"plugin close", "plugins.close", `{"plugin_id":"browser"}`, "session_key", callerKey, false},
		{"lock acquire explicit same owner", "swarm.lock.acquire", `{"resource":"git:main","owner_key":"hermes::hermes-session-42"}`, "owner_key", callerKey, false},
		{"sos escalate explicit same key", "swarm.sos.escalate", `{"reason":"x","key":"hermes::hermes-session-42"}`, "key", callerKey, false},
		// An explicit plugin target is legitimate: the server already routes it.
		{"plugin open explicit target", "plugins.open", `{"plugin_id":"browser","session_key":"codex::other"}`, "session_key", "codex::other", false},
		{"plugin open agent+session target keeps server rule", "plugins.open", `{"plugin_id":"browser","agent_id":"codex","session_id":"other"}`, "session_key", "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			var recorded []observedRequest
			server := newTestServer(t, &requests, &recorded)
			defer server.Close()

			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"invoke", test.capability, "--input", test.input}, strings.NewReader(""), &stdout, &stderr, mapLookup(baseEnv(server.URL)))
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if requests.Load() != 1 || len(recorded) != 1 {
				t.Fatalf("requests = %d, recorded = %d", requests.Load(), len(recorded))
			}
			got := recorded[0]
			if got.capability != test.capability {
				t.Fatalf("capability = %q", got.capability)
			}
			value, present := got.input[test.field]
			if test.absent {
				if present {
					t.Fatalf("%s must stay absent, got %v (input=%v)", test.field, value, got.input)
				}
			} else if str, _ := value.(string); str != test.want {
				t.Fatalf("%s = %v, want %q (input=%v)", test.field, value, test.want, got.input)
			}
			if test.absent {
				if got.input["agent_id"] != "codex" || got.input["session_id"] != "other" {
					t.Fatalf("explicit target fields were rewritten: %v", got.input)
				}
			}
			caller, ok := got.input["caller"].(map[string]any)
			if !ok || caller["session_id"] != "hermes-session-42" || caller["agent_id"] != "hermes" {
				t.Fatalf("caller not transmitted: %v", got.input["caller"])
			}
		})
	}
}

func TestCallerScopeRejectsExplicitBadValuesBeforeHTTP(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		input      string
		want       string
	}{
		{"lock owner null", "swarm.lock.acquire", `{"resource":"git:main","owner_key":null}`, "owner_key must not be null"},
		{"lock owner empty", "swarm.lock.acquire", `{"resource":"git:main","owner_key":""}`, "owner_key must not be empty"},
		{"lock owner number", "swarm.lock.acquire", `{"resource":"git:main","owner_key":42}`, "owner_key must be a string"},
		{"lock owner conflict", "swarm.lock.acquire", `{"resource":"git:main","owner_key":"codex::other"}`, "does not match configured caller"},
		{"lock release owner conflict", "swarm.lock.release", `{"resource":"git:main","owner_key":"codex::other"}`, "does not match configured caller"},
		{"sos key null", "swarm.sos.escalate", `{"reason":"x","key":null}`, "key must not be null"},
		{"sos key empty", "swarm.sos.escalate", `{"reason":"x","key":""}`, "key must not be empty"},
		{"sos key number", "swarm.sos.escalate", `{"reason":"x","key":42}`, "key must be a string"},
		{"sos key conflict", "swarm.sos.escalate", `{"reason":"x","key":"codex::other"}`, "does not match configured caller"},
		{"plugin open null", "plugins.open", `{"plugin_id":"browser","session_key":null}`, "session_key must not be null"},
		{"plugin open empty", "plugins.open", `{"plugin_id":"browser","session_key":""}`, "session_key must not be empty"},
		{"plugin open number", "plugins.open", `{"plugin_id":"browser","session_key":42}`, "session_key must be a string"},
		{"plugin close null", "plugins.close", `{"plugin_id":"browser","session_key":null}`, "session_key must not be null"},
		{"plugin close empty", "plugins.close", `{"plugin_id":"browser","session_key":""}`, "session_key must not be empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := newTestServer(t, &requests, nil)
			defer server.Close()

			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"invoke", test.capability, "--input", test.input}, strings.NewReader(""), &stdout, &stderr, mapLookup(baseEnv(server.URL)))
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if requests.Load() != 0 {
				t.Fatalf("invalid scope reached HTTP: requests = %d", requests.Load())
			}
			if !strings.Contains(stderr.String(), "caller scope failed") || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}

// Without a resolvable caller these capabilities must fail before any HTTP call.
func TestCallerScopeRequiresCallerBeforeHTTP(t *testing.T) {
	for _, capability := range []string{"swarm.lock.acquire", "swarm.lock.release", "swarm.sos.escalate", "plugins.open", "plugins.close"} {
		t.Run(capability, func(t *testing.T) {
			var requests atomic.Int32
			server := newTestServer(t, &requests, nil)
			defer server.Close()

			env := map[string]string{
				"ASTRORDER_AGENT_TOKEN": "token",
				"ASTRORDER_AGENT_URL":   server.URL,
			}
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"invoke", capability, "--input", `{}`}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
			if code != 1 || requests.Load() != 0 || !strings.Contains(stderr.String(), "caller resolution failed") {
				t.Fatalf("exit = %d, requests = %d, stderr = %q", code, requests.Load(), stderr.String())
			}
		})
	}
}
