package sessions

import (
	"net/http"
	"strings"
)

// Registration describes one route matched by the sessions package.
type Registration struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type routeMatch struct {
	method  string
	pattern string
	// kind: static, session_id, command_id, operation_id
	kind string
}

var allRoutes = []Registration{
	{Method: http.MethodGet, Path: "/api/v1/bootstrap"},
	{Method: http.MethodGet, Path: "/api/v1/presence"},
	{Method: http.MethodGet, Path: "/api/v1/user-activity"},
	{Method: http.MethodGet, Path: "/api/v1/open-sessions"},
	{Method: http.MethodGet, Path: "/api/v1/sessions"},
	{Method: http.MethodPost, Path: "/api/v1/sessions"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/refresh-activity"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/batch-delete"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/handoff-curate"},
	{Method: http.MethodPatch, Path: "/api/v1/sessions/{session_id}"},
	{Method: http.MethodDelete, Path: "/api/v1/sessions/{session_id}"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/sync"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/fork"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/handoff"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/messages"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/models"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/agent-commands"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/agent-mentions"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/model"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/model"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/reasoning"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/approval-mode"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/approval-mode"},
	{Method: http.MethodPost, Path: "/api/v1/sessions/{session_id}/user-input"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/commands"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/background-processes"},
	{Method: http.MethodGet, Path: "/api/v1/sessions/{session_id}/tasks"},
	{Method: http.MethodPost, Path: "/api/v1/commands"},
	{Method: http.MethodPatch, Path: "/api/v1/commands/{command_id}"},
	{Method: http.MethodDelete, Path: "/api/v1/commands/{command_id}"},
	{Method: http.MethodPost, Path: "/api/v1/commands/{command_id}/send"},
	{Method: http.MethodPost, Path: "/api/v1/projects"},
	{Method: http.MethodPost, Path: "/api/v1/projects/delete"},
	{Method: http.MethodDelete, Path: "/api/v1/projects"},
	{Method: http.MethodPost, Path: "/api/v1/background-tasks/{operation_id}/cancel"},
	{Method: http.MethodPost, Path: "/api/v1/handoffs/{operation_id}/cancel"},
}

// Registrations returns the exact list of route registrations owned by this package.
func Registrations() []Registration {
	res := make([]Registration, len(allRoutes))
	copy(res, allRoutes)
	return res
}

// Matches returns true if the method and path match any route owned by this package.
func Matches(method, path string) bool {
	route, _ := parseRoute(method, path)
	return route != ""
}

type parsedParams struct {
	SessionID   string
	CommandID   string
	OperationID string
}

func parseRoute(method, path string) (string, parsedParams) {
	params := parsedParams{}
	switch path {
	case "/api/v1/bootstrap":
		if method == http.MethodGet {
			return "bootstrap", params
		}
	case "/api/v1/presence":
		if method == http.MethodGet {
			return "presence", params
		}
	case "/api/v1/user-activity":
		if method == http.MethodGet {
			return "user_activity", params
		}
	case "/api/v1/open-sessions":
		if method == http.MethodGet {
			return "open_sessions", params
		}
	case "/api/v1/sessions":
		if method == http.MethodGet {
			return "list_sessions", params
		}
		if method == http.MethodPost {
			return "create_session", params
		}
	case "/api/v1/sessions/refresh-activity":
		if method == http.MethodPost {
			return "refresh_activity", params
		}
	case "/api/v1/sessions/batch-delete":
		if method == http.MethodPost {
			return "batch_delete_sessions", params
		}
	case "/api/v1/sessions/handoff-curate":
		if method == http.MethodPost {
			return "handoff_curate", params
		}
	case "/api/v1/commands":
		if method == http.MethodPost {
			return "create_command", params
		}
	case "/api/v1/projects":
		if method == http.MethodPost {
			return "create_project", params
		}
		if method == http.MethodDelete {
			return "delete_project", params
		}
	case "/api/v1/projects/delete":
		if method == http.MethodPost {
			return "delete_project", params
		}
	}

	// Dynamic routes under /api/v1/sessions/{session_id}...
	if strings.HasPrefix(path, "/api/v1/sessions/") {
		sub := strings.TrimPrefix(path, "/api/v1/sessions/")
		parts := strings.Split(sub, "/")
		if len(parts) == 1 && parts[0] != "" {
			params.SessionID = parts[0]
			if method == http.MethodPatch {
				return "update_session", params
			}
			if method == http.MethodDelete {
				return "delete_session", params
			}
		} else if len(parts) == 2 && parts[0] != "" {
			params.SessionID = parts[0]
			action := parts[1]
			switch action {
			case "user-input":
				if method == http.MethodPost {
					return "respond_user_input", params
				}
			case "view-lease":
				if method == http.MethodPost {
					return "view_lease", params
				}
			case "sync":
				if method == http.MethodPost {
					return "sync_session", params
				}
			case "fork":
				if method == http.MethodPost {
					return "fork_session", params
				}
			case "handoff":
				if method == http.MethodPost {
					return "handoff_session", params
				}
			case "messages":
				if method == http.MethodGet {
					return "list_messages", params
				}
			case "models":
				if method == http.MethodGet {
					return "session_models", params
				}
			case "agent-commands":
				if method == http.MethodGet {
					return "session_agent_commands", params
				}
			case "agent-mentions":
				if method == http.MethodGet {
					return "session_agent_mentions", params
				}
			case "model":
				if method == http.MethodGet {
					return "get_session_model", params
				}
				if method == http.MethodPost {
					return "set_session_model", params
				}
			case "reasoning":
				if method == http.MethodPost {
					return "set_session_reasoning", params
				}
			case "goal":
				if method == http.MethodPut || method == http.MethodDelete {
					return "manage_session_goal", params
				}
			case "approval-mode":
				if method == http.MethodGet {
					return "get_session_approval_mode", params
				}
				if method == http.MethodPost {
					return "set_session_approval_mode", params
				}
			case "commands":
				if method == http.MethodGet {
					return "list_commands", params
				}
			case "background-processes":
				if method == http.MethodGet {
					return "list_background_processes", params
				}
			case "tasks":
				if method == http.MethodGet {
					return "list_tasks", params
				}
			}
		}
	}

	// Dynamic routes under /api/v1/commands/{command_id}...
	if strings.HasPrefix(path, "/api/v1/commands/") {
		sub := strings.TrimPrefix(path, "/api/v1/commands/")
		parts := strings.Split(sub, "/")
		if len(parts) == 1 && parts[0] != "" {
			params.CommandID = parts[0]
			if method == http.MethodPatch {
				return "edit_queued_command", params
			}
			if method == http.MethodDelete {
				return "delete_queued_command", params
			}
		} else if len(parts) == 2 && parts[0] != "" && parts[1] == "send" {
			params.CommandID = parts[0]
			if method == http.MethodPost {
				return "send_queued_command", params
			}
		}
	}

	// Dynamic routes under /api/v1/background-tasks/{operation_id}/cancel and /api/v1/handoffs/{operation_id}/cancel
	if strings.HasPrefix(path, "/api/v1/background-tasks/") {
		sub := strings.TrimPrefix(path, "/api/v1/background-tasks/")
		parts := strings.Split(sub, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "cancel" {
			params.OperationID = parts[0]
			if method == http.MethodPost {
				return "cancel_handoff", params
			}
		}
	}
	if strings.HasPrefix(path, "/api/v1/handoffs/") {
		sub := strings.TrimPrefix(path, "/api/v1/handoffs/")
		parts := strings.Split(sub, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "cancel" {
			params.OperationID = parts[0]
			if method == http.MethodPost {
				return "cancel_handoff", params
			}
		}
	}

	return "", params
}
