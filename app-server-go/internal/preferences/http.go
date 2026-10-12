package preferences

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

// BrowserAuthorizer preserves the shared browser token and Origin policy.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Options struct {
	Store      *store.Store
	Authorizer BrowserAuthorizer
}

type Registration struct {
	Method string
	Path   string
}

type route struct {
	Registration
	write     bool
	importing bool
}

var routes = []route{
	{Registration: Registration{Method: http.MethodGet, Path: "/api/v1/preferences"}},
	{Registration: Registration{Method: http.MethodPatch, Path: "/api/v1/preferences"}, write: true},
	{Registration: Registration{Method: http.MethodPost, Path: "/api/v1/preferences/import"}, write: true, importing: true},
}

// Registrations enumerates the same exact routes used by this handler. Mounting
// and write ownership remain explicit responsibilities of the App constructor.
func Registrations() []Registration {
	result := make([]Registration, len(routes))
	for i, entry := range routes {
		result[i] = entry.Registration
	}
	return result
}

func NewHandler(options Options) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var selected *route
		allowed := ""
		for i := range routes {
			entry := &routes[i]
			if entry.Path == request.URL.Path && allowed == "" {
				allowed = entry.Method
			}
			if entry.Path == request.URL.Path && entry.Method == request.Method {
				selected = entry
				break
			}
		}
		if selected == nil {
			if allowed != "" {
				response.Header().Set("Allow", allowed)
				writeHTTPError(response, http.StatusMethodNotAllowed, "Method Not Allowed")
				return
			}
			writeHTTPError(response, http.StatusNotFound, "Not Found")
			return
		}
		if options.Authorizer == nil {
			writeHTTPError(response, http.StatusServiceUnavailable, "Authentication is not configured")
			return
		}
		if failure := options.Authorizer.AuthorizeBrowser(request); failure != nil {
			writeHTTPError(response, failure.Status, failure.Detail)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		if options.Store == nil {
			writeHTTPError(response, http.StatusServiceUnavailable, "Preference store is not configured")
			return
		}
		var state State
		var err error
		if selected.write {
			patch, decodeErr := decodePatch(request.Body)
			if decodeErr != nil {
				writeHTTPError(response, http.StatusUnprocessableEntity, "Invalid request")
				return
			}
			if _, validationErr := validatePatch(patch, selected.importing); validationErr != nil {
				writeHTTPError(response, http.StatusUnprocessableEntity, "Invalid request")
				return
			}
			if selected.importing {
				state, err = Import(request.Context(), options.Store, patch)
			} else {
				state, err = Apply(request.Context(), options.Store, patch)
			}
		} else {
			state, err = Read(request.Context(), options.Store)
		}
		if err != nil {
			writeHTTPError(response, http.StatusInternalServerError, "Internal server error")
			return
		}
		writeHTTPJSON(response, http.StatusOK, state)
	})
}

func decodePatch(reader io.Reader) (Patch, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return Patch{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Patch{}, io.ErrUnexpectedEOF
	}
	var patch Patch
	for key, value := range fields {
		switch key {
		case "appearance", "session_pins":
			var values map[string]any
			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.UseNumber()
			if err := decoder.Decode(&values); err != nil || values == nil {
				return Patch{}, io.ErrUnexpectedEOF
			}
			if key == "appearance" {
				patch.Appearance = values
			} else {
				patch.SessionPins = values
			}
		case "pinned_projects", "project_order":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			var items []json.RawMessage
			if err := json.Unmarshal(value, &items); err != nil {
				return Patch{}, err
			}
			list := make([]string, 0, len(items))
			for _, item := range items {
				var text string
				if bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
					return Patch{}, io.ErrUnexpectedEOF
				}
				if err := json.Unmarshal(item, &text); err != nil {
					return Patch{}, err
				}
				list = append(list, text)
			}
			if key == "pinned_projects" {
				patch.PinnedProjects = &list
			} else {
				patch.ProjectOrder = &list
			}
		default:
			return Patch{}, io.ErrUnexpectedEOF
		}
	}
	return patch, nil
}

func writeHTTPError(response http.ResponseWriter, status int, detail string) {
	writeHTTPJSON(response, status, map[string]string{"detail": detail})
}

func writeHTTPJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
