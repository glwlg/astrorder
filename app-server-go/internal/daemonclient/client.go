package daemonclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	AuthorizationMetadataKey = "authorization"
	MaxGRPCWireBytes         = 33554432
	MaxCompatBusinessBytes   = 16000000

	RequiredProtocolMajor = 1
	RequiredProtocolMinor = 0

	DefaultRequestTimeout = 10 * time.Second
)

// Authorized full service names as registered by session-daemon-go
var requiredServices = []string{
	"astrorder.sessiond.v1.ConnectorIngress",
	"astrorder.sessiond.v1.DaemonControl",
	"astrorder.sessiond.v1.EventStream",
	"astrorder.sessiond.v1.ModelConfig",
	"astrorder.sessiond.v1.OperationQuery",
	"astrorder.sessiond.v1.RuntimeQuery",
	"astrorder.sessiond.v1.SessionControl",
	"astrorder.sessiond.v1.TerminalStream",
}

// Config specifies the client dialing parameters.
type Config struct {
	Target         string
	Credential     string
	RequestTimeout time.Duration
}

// Client wraps direct gRPC clients for Astrorder session daemon services.
type Client struct {
	conn         *grpc.ClientConn
	daemonID     string
	capabilities *sessiondv1.GetCapabilitiesResponse

	Daemon    sessiondv1.DaemonControlClient
	Session   sessiondv1.SessionControlClient
	Runtime   sessiondv1.RuntimeQueryClient
	Operation sessiondv1.OperationQueryClient
	Model     sessiondv1.ModelConfigClient
	Events    sessiondv1.EventStreamClient
	Terminal  sessiondv1.TerminalStreamClient
	Connector sessiondv1.ConnectorIngressClient

	cancelMu sync.Mutex
	cancels  map[int]context.CancelFunc
	nextSub  int
	closed   bool
}

// ValidateTarget validates and canonicalizes an explicit loopback transport.
// It strictly rejects WebSocket URIs, non-loopback IPs, missing/zero ports, and unparseable authorities.
func ValidateTarget(target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", errors.New("target cannot be empty")
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return "", errors.New("WebSocket transport is rejected; must be explicit gRPC loopback")
	}

	var hostPart, portPart string

	if strings.Contains(trimmed, "://") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("invalid target URI: %w", err)
		}
		// Only explicit passthrough/dns schemes allowed if URI scheme used
		if u.Scheme != "passthrough" && u.Scheme != "dns" {
			return "", fmt.Errorf("unsupported target scheme: %s", u.Scheme)
		}
		hostPart = u.Hostname()
		portPart = u.Port()
		if hostPart == "" || portPart == "" {
			return "", errors.New("target URI must include valid non-empty host and port")
		}
	} else {
		host, port, err := net.SplitHostPort(trimmed)
		if err != nil {
			return "", fmt.Errorf("target must be host:port: %w", err)
		}
		hostPart = host
		portPart = port
	}

	portNum, err := strconv.Atoi(portPart)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return "", fmt.Errorf("invalid port %q: must be between 1 and 65535", portPart)
	}

	if !isLoopback(hostPart) {
		return "", fmt.Errorf("non-loopback target rejected: %s", hostPart)
	}

	return net.JoinHostPort(hostPart, portPart), nil
}

func isLoopback(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// Dial creates an authenticated gRPC connection to the loopback Session Daemon.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	canonicalTarget, err := ValidateTarget(cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("validate target: %w", err)
	}
	if strings.TrimSpace(cfg.Credential) == "" {
		return nil, errors.New("daemon credential must be configured")
	}

	authHeader := "Bearer " + cfg.Credential

	unaryInterceptor := func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		ctx = metadata.AppendToOutgoingContext(ctx, AuthorizationMetadataKey, authHeader)
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	streamInterceptor := func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, AuthorizationMetadataKey, authHeader)
		return streamer(ctx, desc, cc, method, opts...)
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(unaryInterceptor),
		grpc.WithStreamInterceptor(streamInterceptor),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxGRPCWireBytes),
			grpc.MaxCallSendMsgSize(MaxGRPCWireBytes),
		),
	}

	conn, err := grpc.NewClient(canonicalTarget, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial daemon: %w", err)
	}

	client := &Client{
		conn:      conn,
		Daemon:    sessiondv1.NewDaemonControlClient(conn),
		Session:   sessiondv1.NewSessionControlClient(conn),
		Runtime:   historyPagingClient{RuntimeQueryClient: sessiondv1.NewRuntimeQueryClient(conn)},
		Operation: sessiondv1.NewOperationQueryClient(conn),
		Model:     sessiondv1.NewModelConfigClient(conn),
		Events:    sessiondv1.NewEventStreamClient(conn),
		Terminal:  sessiondv1.NewTerminalStreamClient(conn),
		Connector: sessiondv1.NewConnectorIngressClient(conn),
		cancels:   make(map[int]context.CancelFunc),
	}

	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := client.Daemon.GetCapabilities(probeCtx, &sessiondv1.GetCapabilitiesRequest{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("daemon GetCapabilities failed: %w", err)
	}

	if err := validateCapabilities(resp); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("validate capabilities: %w", err)
	}

	client.daemonID = resp.GetDaemonId()
	client.capabilities = resp

	return client, nil
}

func validateCapabilities(resp *sessiondv1.GetCapabilitiesResponse) error {
	if resp == nil {
		return errors.New("empty capabilities response")
	}
	if strings.TrimSpace(resp.GetDaemonId()) == "" {
		return errors.New("daemon ID must not be empty")
	}
	if resp.GetProtocolMajor() != RequiredProtocolMajor {
		return fmt.Errorf("unsupported protocol major version: %d (expected %d)", resp.GetProtocolMajor(), RequiredProtocolMajor)
	}
	if resp.GetProtocolMinor() < RequiredProtocolMinor {
		return fmt.Errorf("unsupported protocol minor version: %d (minimum required %d)", resp.GetProtocolMinor(), RequiredProtocolMinor)
	}

	if resp.GetMaxWireBytes() != sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES {
		return fmt.Errorf("unsupported wire limit: %v", resp.GetMaxWireBytes())
	}
	if resp.GetMaxBusinessBytes() != sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES {
		return fmt.Errorf("unsupported business limit: %v", resp.GetMaxBusinessBytes())
	}

	if resp.GetEventStreamVersion() != sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1 {
		return fmt.Errorf("unsupported event stream version: %v", resp.GetEventStreamVersion())
	}

	if !resp.GetOperationLookup() {
		return errors.New("daemon operation lookup capability is required")
	}

	hasLegacyV1 := false
	for _, v := range resp.GetCanonicalizationVersions() {
		if v == sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
			hasLegacyV1 = true
			break
		}
	}
	if !hasLegacyV1 {
		return errors.New("daemon canonicalization version legacy JSON v1 is required")
	}

	registered := make(map[string]bool)
	for _, svc := range resp.GetServices() {
		registered[svc] = true
	}
	for _, req := range requiredServices {
		if !registered[req] {
			return fmt.Errorf("missing required service: %s", req)
		}
	}

	return nil
}

// DaemonID returns the authoritative daemon ID discovered at dial time.
func (c *Client) DaemonID() string {
	return c.daemonID
}

// Capabilities returns a deep clone of the server capabilities.
func (c *Client) Capabilities() *sessiondv1.GetCapabilitiesResponse {
	if c.capabilities == nil {
		return nil
	}
	services := make([]string, len(c.capabilities.Services))
	copy(services, c.capabilities.Services)
	sort.Strings(services)

	canonVersions := make([]sessiondv1.CanonicalizationVersion, len(c.capabilities.CanonicalizationVersions))
	copy(canonVersions, c.capabilities.CanonicalizationVersions)

	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor:            c.capabilities.ProtocolMajor,
		ProtocolMinor:            c.capabilities.ProtocolMinor,
		DaemonId:                 c.capabilities.DaemonId,
		Services:                 services,
		CanonicalizationVersions: canonVersions,
		MaxBusinessBytes:         c.capabilities.MaxBusinessBytes,
		MaxWireBytes:             c.capabilities.MaxWireBytes,
		EventStreamVersion:       c.capabilities.EventStreamVersion,
		OperationLookup:          c.capabilities.OperationLookup,
	}
}

// RegisterSubscription retains a cancel func for an active subscription.
func (c *Client) RegisterSubscription(cancel context.CancelFunc) func() {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	if c.closed {
		cancel()
		return func() {}
	}
	id := c.nextSub
	c.nextSub++
	c.cancels[id] = cancel
	return func() {
		c.cancelMu.Lock()
		defer c.cancelMu.Unlock()
		delete(c.cancels, id)
	}
}

// Close releases only client-side resources (gRPC connection and subscriptions).
// It does NOT stop the daemon or managed runtimes.
func (c *Client) Close() error {
	c.cancelMu.Lock()
	if c.closed {
		c.cancelMu.Unlock()
		return nil
	}
	c.closed = true
	for _, cancel := range c.cancels {
		if cancel != nil {
			cancel()
		}
	}
	c.cancels = nil
	c.cancelMu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
