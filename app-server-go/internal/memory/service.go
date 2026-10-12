package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/auth"
)

const maxMemoryRequestBody = 64 * 1024

const (
	RegistrationHTTP = "http"
	AuthBrowser      = "browser"
)

type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
}

type Authorizer func(*http.Request) error

type ClientFactory func(context.Context) (*OpenVikingClient, error)

type ServiceOptions struct {
	Config    *ConfigService
	Client    ClientFactory
	Targets   TargetResolver
	Syncer    *Syncer
	Authorize Authorizer
}

type Service struct {
	config    *ConfigService
	client    ClientFactory
	targets   TargetResolver
	syncer    *Syncer
	authorize Authorizer
	routes    []routeDefinition
}

type routeDefinition struct {
	registration Registration
	handler      http.HandlerFunc
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Config == nil || options.Client == nil || options.Targets == nil || options.Syncer == nil || options.Authorize == nil {
		return nil, errors.New("memory config, client, targets, syncer, and authorizer are required")
	}
	service := &Service{config: options.Config, client: options.Client, targets: options.Targets, syncer: options.Syncer, authorize: options.Authorize}
	service.routes = []routeDefinition{
		service.route(http.MethodGet, "/api/v1/memory/config", service.getConfig),
		service.route(http.MethodPost, "/api/v1/memory/config", service.updateConfig),
		service.route(http.MethodGet, "/api/v1/memory/health", service.health),
		service.route(http.MethodGet, "/api/v1/memory/ls", service.list),
		service.route(http.MethodGet, "/api/v1/memory/tree", service.tree),
		service.route(http.MethodGet, "/api/v1/memory/content", service.content),
		service.route(http.MethodPost, "/api/v1/memory/search", service.search),
		service.route(http.MethodGet, "/api/v1/memory/targets", service.listTargets),
		service.route(http.MethodGet, "/api/v1/memory/targets/{target_id}/status", service.targetStatus),
		service.route(http.MethodPost, "/api/v1/memory/apply", service.apply),
	}
	return service, nil
}

func (s *Service) route(method, path string, handler http.HandlerFunc) routeDefinition {
	return routeDefinition{Registration{Kind: RegistrationHTTP, Method: method, Path: path, Auth: AuthBrowser}, handler}
}

func (s *Service) Registrations() []Registration {
	registrations := make([]Registration, len(s.routes))
	for index, route := range s.routes {
		registrations[index] = route.registration
	}
	return registrations
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, route := range s.routes {
		mux.HandleFunc(route.registration.Method+" "+route.registration.Path, s.authenticated(route.handler))
	}
	return mux
}

func (s *Service) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if err := s.authorize(request); err != nil {
			status := http.StatusUnauthorized
			var authError *auth.Error
			if errors.As(err, &authError) && authError.Status >= 400 && authError.Status <= 599 {
				status = authError.Status
			}
			writeServiceError(response, status, err.Error())
			return
		}
		next(response, request)
	}
}

func (s *Service) getConfig(response http.ResponseWriter, request *http.Request) {
	view, err := s.config.View(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "读取 OpenViking 配置失败")
		return
	}
	writeServiceJSON(response, http.StatusOK, view)
}

func (s *Service) updateConfig(response http.ResponseWriter, request *http.Request) {
	var payload ConfigUpdate
	if err := decodeRequest(response, request, &payload); err != nil {
		writeServiceError(response, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	view, err := s.config.Update(request.Context(), payload)
	if err != nil {
		writeServiceError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeServiceJSON(response, http.StatusOK, view)
}

func (s *Service) health(response http.ResponseWriter, request *http.Request) {
	client, err := s.client(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "创建 OpenViking 客户端失败")
		return
	}
	writeServiceJSON(response, http.StatusOK, client.Health(request.Context()))
}

func (s *Service) list(response http.ResponseWriter, request *http.Request) {
	uri := request.URL.Query().Get("uri")
	if uri == "" {
		uri = "viking://"
	}
	client, err := s.client(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "创建 OpenViking 客户端失败")
		return
	}
	items, err := client.ListDir(request.Context(), uri)
	if err != nil {
		writeServiceError(response, http.StatusBadGateway, "OpenViking 目录列取失败: "+err.Error())
		return
	}
	writeServiceJSON(response, http.StatusOK, map[string]any{"uri": uri, "items": items})
}

func (s *Service) tree(response http.ResponseWriter, request *http.Request) {
	uri := request.URL.Query().Get("uri")
	if uri == "" {
		uri = "viking://"
	}
	client, err := s.client(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "创建 OpenViking 客户端失败")
		return
	}
	items, err := client.Tree(request.Context(), uri)
	if err != nil {
		writeServiceError(response, http.StatusBadGateway, "OpenViking 目录树获取失败: "+err.Error())
		return
	}
	writeServiceJSON(response, http.StatusOK, map[string]any{"uri": uri, "items": items})
}

func (s *Service) content(response http.ResponseWriter, request *http.Request) {
	uri := request.URL.Query().Get("uri")
	if uri == "" {
		writeServiceError(response, http.StatusUnprocessableEntity, "uri is required")
		return
	}
	client, err := s.client(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "创建 OpenViking 客户端失败")
		return
	}
	content, err := client.ReadContent(request.Context(), uri)
	if err != nil {
		writeServiceError(response, http.StatusBadGateway, "OpenViking 内容读取失败: "+err.Error())
		return
	}
	writeServiceJSON(response, http.StatusOK, map[string]any{"uri": uri, "content": content})
}

type searchPayload struct {
	Query string  `json:"query"`
	Scope *string `json:"scope,omitempty"`
	Limit int     `json:"limit"`
}

func (s *Service) search(response http.ResponseWriter, request *http.Request) {
	payload := searchPayload{Limit: 5}
	if err := decodeRequest(response, request, &payload); err != nil || len(payload.Query) < 1 || len(payload.Query) > 1000 || payload.Limit < 1 || payload.Limit > 20 || (payload.Scope != nil && len(*payload.Scope) > 512) {
		writeServiceError(response, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	scope := ""
	if payload.Scope != nil {
		scope = *payload.Scope
	}
	client, err := s.client(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "创建 OpenViking 客户端失败")
		return
	}
	items, err := client.Search(request.Context(), payload.Query, scope, payload.Limit)
	if err != nil {
		writeServiceError(response, http.StatusBadGateway, "OpenViking 语义搜索失败: "+err.Error())
		return
	}
	writeServiceJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) listTargets(response http.ResponseWriter, request *http.Request) {
	targets, err := s.targets.ResolveTargets(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "列取同步目标失败")
		return
	}
	writeServiceJSON(response, http.StatusOK, map[string]any{"items": targets})
}

func (s *Service) targetStatus(response http.ResponseWriter, request *http.Request) {
	config, err := s.config.Active(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "读取 OpenViking 配置失败")
		return
	}
	statuses, err := s.syncer.InspectSelected(request.Context(), config, []string{request.PathValue("target_id")})
	if err != nil {
		writeServiceError(response, http.StatusNotFound, err.Error())
		return
	}
	if len(statuses) != 1 {
		writeServiceError(response, http.StatusNotFound, "同步目标不存在")
		return
	}
	writeServiceJSON(response, http.StatusOK, statuses[0])
}

func (s *Service) apply(response http.ResponseWriter, request *http.Request) {
	var payload struct {
		TargetIDs []string `json:"target_ids"`
	}
	if err := decodeRequest(response, request, &payload); err != nil || payload.TargetIDs == nil {
		writeServiceError(response, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	config, err := s.config.Active(request.Context())
	if err != nil {
		writeServiceError(response, http.StatusInternalServerError, "读取 OpenViking 配置失败")
		return
	}
	result, err := s.syncer.ApplySelected(request.Context(), config, payload.TargetIDs)
	if err != nil {
		writeServiceError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeServiceJSON(response, http.StatusOK, map[string]any{"items": result.Targets})
}

func decodeRequest(response http.ResponseWriter, request *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxMemoryRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	returnTrailing := decoder.Decode(&trailing)
	if returnTrailing != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func writeServiceJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func writeServiceError(response http.ResponseWriter, status int, detail string) {
	if strings.TrimSpace(detail) == "" {
		detail = http.StatusText(status)
	}
	writeServiceJSON(response, status, map[string]string{"detail": detail})
}

func NewClientFactory(config *ConfigService, options OpenVikingClientOptions) ClientFactory {
	return func(ctx context.Context) (*OpenVikingClient, error) {
		active, err := config.Active(ctx)
		if err != nil {
			return nil, err
		}
		options.BaseURL = active.URL
		options.APIKey = active.APIKey
		client, err := NewOpenVikingClient(options)
		if err != nil {
			return nil, fmt.Errorf("create OpenViking client: %w", err)
		}
		return client, nil
	}
}
