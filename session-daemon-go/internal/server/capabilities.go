package server

import (
	"encoding/json"
	"net/http"

	"astrorder.dev/session-daemon/internal/parity"
)

type Config struct {
	Secret string
}

type Server struct {
	secret string
}

func New(config Config) *Server {
	return &Server{secret: config.Secret}
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/capabilities", server.capabilities)
	return mux
}

func (server *Server) capabilities(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+server.secret || server.secret == "" {
		http.Error(response, "daemon authentication failed", http.StatusUnauthorized)
		return
	}
	var manifest struct {
		RuntimeTypes []string `json:"runtime_types"`
	}
	if err := json.Unmarshal(parity.Manifest, &manifest); err != nil {
		http.Error(response, "daemon contract is invalid", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(map[string]any{
		"implemented":   true,
		"runtime_types": manifest.RuntimeTypes,
	})
}
