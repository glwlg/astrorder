package notifications

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	webpush "github.com/SherClockHolmes/webpush-go"
)

const subscriptionPrefix = "web_push:subscription:"
const pushPath = "/api/v1/notifications/push"

type vapidKeys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

type subscription struct {
	webpush.Subscription
	Origin    string    `json:"origin"`
	Mobile    bool      `json:"mobile"`
	Cursor    int64     `json:"cursor"`
	Failures  int       `json:"failures"`
	RetryAt   time.Time `json:"retry_at"`
	LastError string    `json:"last_error"`
}

type Notice struct {
	Key       string    `json:"key"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	TargetID  string    `json:"target_id"`
	Source    string    `json:"source"`
}

type Service struct {
	store  *store.Store
	auth   *auth.Authenticator
	keys   vapidKeys
	client webpush.HTTPClient
	logger *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func New(ctx context.Context, st *store.Store, authorizer *auth.Authenticator, logger *slog.Logger) (*Service, error) {
	if st == nil || authorizer == nil {
		return nil, errors.New("push store and authorizer are required")
	}
	var keys vapidKeys
	var raw string
	err := st.DB().QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key='web_push:vapid'`).Scan(&raw)
	if err == sql.ErrNoRows {
		keys.Private, keys.Public, err = webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, err
		}
		if err = st.SetPreference(ctx, "web_push:vapid", keys, false); err != nil {
			return nil, err
		}
		err = st.DB().QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key='web_push:vapid'`).Scan(&raw)
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &keys); err != nil || keys.Private == "" || keys.Public == "" {
		return nil, errors.New("stored Web Push keys are invalid")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	s := &Service{store: st, auth: authorizer, keys: keys, client: pushHTTPClient(), logger: logger, cancel: cancel, done: make(chan struct{})}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := s.deliverPending(workerCtx); err != nil && workerCtx.Err() == nil {
					s.logger.Error("Web Push delivery scan failed", "error", err)
				}
			}
		}
	}()
	return s, nil
}

func (s *Service) Close() { s.cancel(); <-s.done }

func Matches(method, path string) bool {
	return path == pushPath && (method == "GET" || method == "PUT" || method == "DELETE") ||
		path == pushPath+"/test" && method == "POST"
}

func subscriptionKey(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return subscriptionPrefix + hex.EncodeToString(sum[:])
}

func pushError(w http.ResponseWriter, detail string, status int) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if failure := s.auth.AuthorizeBrowser(r); failure != nil {
		pushError(w, failure.Detail, failure.Status)
		return
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]string{"public_key": s.keys.Public})
		return
	}
	var input struct {
		webpush.Subscription
		Origin string `json:"origin"`
		Mobile bool   `json:"mobile"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(input.Endpoint) > 4096 || input.Endpoint == "" {
		pushError(w, "通知订阅格式不正确", http.StatusUnprocessableEntity)
		return
	}
	key := subscriptionKey(input.Endpoint)
	if r.Method == http.MethodDelete {
		if _, err := s.store.DeletePreference(r.Context(), key); err != nil {
			pushError(w, "停用后台通知失败", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
		return
	}
	if r.URL.Path == pushPath+"/test" {
		var sub subscription
		if err := s.readSubscription(r.Context(), key, &sub); err != nil {
			pushError(w, "请先启用后台通知", 404)
			return
		}
		notice := Notice{Key: fmt.Sprintf("push-test-%d", time.Now().UnixNano()), Kind: "task_completed", Title: "星序 · 测试通知", Message: "后台通知已连接。任务完成和待回答问题将在这里提醒。", CreatedAt: time.Now()}
		code, err := s.send(r.Context(), sub, notice)
		if err != nil || code < 200 || code >= 300 {
			pushError(w, "推送服务未接受测试通知，请检查网络后重试", 502)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
		return
	}
	if err := validateSubscription(input.Subscription, input.Origin, r.Header.Get("Origin")); err != nil {
		pushError(w, err.Error(), 422)
		return
	}
	err := s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		sub := subscription{Subscription: input.Subscription, Origin: input.Origin, Mobile: input.Mobile}
		var previous string
		err := tx.QueryRowContext(r.Context(), `SELECT value FROM workspace_preferences WHERE key=?`, key).Scan(&previous)
		if err == nil {
			var old subscription
			if err := json.Unmarshal([]byte(previous), &old); err != nil {
				return err
			}
			sub.Cursor = old.Cursor
		} else if err == sql.ErrNoRows {
			if err := tx.QueryRowContext(r.Context(), `SELECT coalesce(max(cursor),0) FROM events`).Scan(&sub.Cursor); err != nil {
				return err
			}
		} else {
			return err
		}
		raw, err := json.Marshal(sub)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO workspace_preferences(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(raw))
		return err
	})
	if err != nil {
		pushError(w, "保存通知订阅失败", 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": true})
}

func validateSubscription(sub webpush.Subscription, origin, requestOrigin string) error {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return errors.New("推送地址必须是 HTTPS 地址")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicIP(ip) {
		return errors.New("推送地址不能指向本机或内网")
	}
	o, err := url.Parse(origin)
	if err != nil || o.Scheme != "https" || o.Host == "" || o.User != nil || o.RawQuery != "" || o.Fragment != "" || o.Path != "" ||
		(requestOrigin != "" && requestOrigin != origin) {
		return errors.New("后台通知需要从星序的 HTTPS 页面启用")
	}
	authKey, err := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	if err != nil || len(authKey) != 16 {
		return errors.New("通知订阅密钥不正确")
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(sub.Keys.P256dh)
	if err != nil {
		return errors.New("通知订阅公钥不正确")
	}
	if x, _ := elliptic.Unmarshal(elliptic.P256(), publicKey); x == nil {
		return errors.New("通知订阅公钥不正确")
	}
	return nil
}

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	v4 := ip.To4()
	return v4 == nil || !(v4[0] == 100 && v4[1]&0xc0 == 64)
}

func pushHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, address := range addresses {
				if !publicIP(address.IP) {
					return nil, errors.New("push endpoint resolved to a non-public address")
				}
			}
			if len(addresses) == 0 {
				return nil, errors.New("push endpoint has no address")
			}
			return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		}},
	}
}

func (s *Service) readSubscription(ctx context.Context, key string, sub *subscription) error {
	var raw string
	if err := s.store.DB().QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), sub)
}

func (s *Service) send(ctx context.Context, sub subscription, notice Notice) (int, error) {
	path := "/"
	if notice.SessionID != "" && notice.AgentID != "" {
		prefix := ""
		if sub.Mobile {
			prefix = "/mobile"
		}
		path = prefix + "/chat/" + url.PathEscape(notice.SessionID) + "?agent_id=" + url.QueryEscape(notice.AgentID)
	}
	ttl := 3600
	if notice.Kind == "approval_pending" {
		ttl = 300
	}
	payload, err := json.Marshal(map[string]any{"key": notice.Key, "kind": notice.Kind, "title": notice.Title, "message": notice.Message, "url": path})
	if err != nil {
		return 0, err
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, &sub.Subscription, &webpush.Options{
		HTTPClient: s.client, Subscriber: sub.Origin, VAPIDPublicKey: s.keys.Public, VAPIDPrivateKey: s.keys.Private, TTL: ttl, Urgency: webpush.UrgencyHigh,
	})
	if err != nil {
		// Endpoint URLs are capability credentials; do not expose transport errors.
		return 0, errors.New("push transport failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
	return response.StatusCode, nil
}

func (s *Service) deliverPending(ctx context.Context) error {
	rows, err := s.store.QueryRows(ctx, `SELECT key FROM workspace_preferences WHERE instr(key,?)=1`, subscriptionPrefix)
	if err != nil {
		return err
	}
	for _, row := range rows {
		key := text(row, "key")
		var sub subscription
		var previous string
		if err := s.store.DB().QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, key).Scan(&previous); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		if err := json.Unmarshal([]byte(previous), &sub); err != nil {
			return err
		}
		if time.Now().Before(sub.RetryAt) {
			continue
		}
		notices, err := s.store.QueryRows(ctx, `SELECT cursor,data FROM events WHERE type=? AND cursor>? ORDER BY cursor LIMIT 20`, EventType, sub.Cursor)
		if err != nil {
			return err
		}
		for _, row := range notices {
			raw, err := json.Marshal(row["data"])
			if err != nil {
				return err
			}
			var notice Notice
			if err := json.Unmarshal(raw, &notice); err != nil {
				return err
			}
			cursor := row["cursor"].(int64)
			deliver, err := s.stillRelevant(ctx, notice)
			if err != nil {
				return err
			}
			code := http.StatusNoContent
			if deliver {
				code, err = s.send(ctx, sub, notice)
			}
			if code == 404 || code == 410 {
				_, err = s.store.DB().ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=? AND value=?`, key, previous)
				if err != nil {
					return err
				}
				break
			}
			if err != nil || code < 200 || code >= 300 {
				sub.Failures++
				delay := min(time.Duration(1<<min(sub.Failures, 6))*time.Second, time.Minute)
				sub.RetryAt = time.Now().Add(delay)
				sub.LastError = fmt.Sprintf("推送暂未送达（HTTP %d）", code)
			} else {
				sub.Cursor, sub.Failures, sub.RetryAt, sub.LastError = cursor, 0, time.Time{}, ""
			}
			// A concurrent unsubscribe or renewal must win over this delivery.
			err = s.store.DB().QueryRowContext(ctx, `UPDATE workspace_preferences SET value=json_set(value,'$.cursor',?,'$.failures',?,'$.retry_at',?,'$.last_error',?) WHERE key=? AND value=? RETURNING value`,
				sub.Cursor, sub.Failures, sub.RetryAt.Format(time.RFC3339Nano), sub.LastError, key, previous).Scan(&previous)
			if err == sql.ErrNoRows {
				break
			}
			if err != nil {
				return err
			}
			if sub.Failures > 0 {
				break
			}
		}
	}
	return nil
}

func (s *Service) stillRelevant(ctx context.Context, notice Notice) (bool, error) {
	ttl := time.Hour
	if notice.Kind == "approval_pending" {
		ttl = 5 * time.Minute
	}
	if notice.CreatedAt.IsZero() || time.Since(notice.CreatedAt) > ttl {
		return false, nil
	}
	if notice.Source == "approval.upsert" {
		approval, err := s.store.Approval(ctx, notice.AgentID, notice.SessionID, notice.TargetID)
		return approval != nil && approval["state"] == "pending", err
	}
	if notice.Source == "message.upsert" {
		var answers int
		err := s.store.DB().QueryRowContext(ctx, `SELECT count(*) FROM commands WHERE agent_id=? AND session_id=? AND target_id=? AND action='send' AND state IN ('accepted','running','completed')`,
			notice.AgentID, notice.SessionID, notice.TargetID).Scan(&answers)
		return answers == 0, err
	}
	return true, nil
}
