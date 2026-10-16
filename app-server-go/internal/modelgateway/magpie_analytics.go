package modelgateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Magpie totals keep cache traffic separate from input/output tokens.
type magpieTotals struct {
	Calls      int64   `json:"calls"`
	Errors     int64   `json:"errors"`
	Retried    int64   `json:"retried"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Reasoning  int64   `json:"reasoning"`
	Cost       float64 `json:"cost"`
	Unpriced   int64   `json:"unpriced"`
}

type magpieGroup struct {
	magpieTotals
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Account  string `json:"account"`
	Name     string `json:"name"`
}
type magpiePoint struct {
	magpieTotals
	Time time.Time `json:"time"`
}
type magpieSummary struct {
	magpieTotals
	Period   string        `json:"period"`
	Bucket   string        `json:"bucket"`
	Since    time.Time     `json:"since"`
	Series   []magpiePoint `json:"series"`
	Models   []magpieGroup `json:"models"`
	Accounts []magpieGroup `json:"accounts"`
}

func (t *magpieTotals) add(b magpieTotals) {
	t.Calls += b.Calls
	t.Errors += b.Errors
	t.Retried += b.Retried
	t.Input += b.Input
	t.Output += b.Output
	t.CacheRead += b.CacheRead
	t.CacheWrite += b.CacheWrite
	t.Reasoning += b.Reasoning
	t.Cost += b.Cost
	t.Unpriced += b.Unpriced
}
func ratio(n, d int64) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// Magpie's input excludes cache reads and writes; reasoning is already in output.
func (t magpieTotals) totalTokens() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

func (t magpieTotals) metrics() map[string]any {
	observed := t.Input + t.CacheRead + t.CacheWrite
	return map[string]any{
		"requests": t.Calls, "errors": t.Errors, "retriedRequests": t.Retried,
		"totalTokens": t.totalTokens(), "inputTokens": t.Input, "outputTokens": t.Output,
		"cachedInputTokens": t.CacheRead, "cacheReadInputTokens": t.CacheRead, "cacheCreationInputTokens": t.CacheWrite,
		"cacheObservedInputTokens": observed, "cacheHitRate": ratio(t.CacheRead, observed), "reasoningOutputTokens": t.Reasoning,
		"estimatedCostUsd": t.Cost, "unpricedRequests": t.Unpriced,
	}
}

func magpieEndpoint(cfg GatewayConfig, path string) (string, error) {
	u, err := url.Parse(strings.TrimRight(cfg.InferenceURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("Magpie 推理地址无效")
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/models")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.Path += "/magpie/" + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (h *Handler) readMagpie(r *http.Request, cfg GatewayConfig, path string, q url.Values, out any) error {
	endpoint, err := magpieEndpoint(cfg, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("Magpie 请求地址无效")
	}
	req.URL.RawQuery = q.Encode()
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Magpie 请求失败，请检查推理地址和网络连接")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("Magpie 返回 HTTP %d：请检查网关 API Key、局域网共享和该 Key 的用量／额度访问权限", resp.StatusCode)
		}
		return fmt.Errorf("Magpie 返回 HTTP %d", resp.StatusCode)
	}
	const maxBody = 32 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return fmt.Errorf("Magpie 响应读取失败或超过大小限制")
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return fmt.Errorf("Magpie 返回无效 JSON 对象")
	}
	if _, ok := envelope["error"]; ok {
		return fmt.Errorf("Magpie 返回接口错误")
	}
	required := "calls"
	if path == "quotas" {
		required = "data"
	}
	if path == "usage/requests" {
		required = "rows"
	}
	if v, ok := envelope[required]; !ok || string(v) == "null" {
		return fmt.Errorf("Magpie 响应缺少 %s 字段", required)
	}
	if err = json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("Magpie 响应格式无效")
	}
	return nil
}

func (h *Handler) serveMagpieUsage(w http.ResponseWriter, r *http.Request, cfg GatewayConfig, period, surface, since, until string) {
	var s magpieSummary
	var err error
	if surface == "all" && since == "" {
		err = h.readMagpie(r, cfg, "usage", url.Values{"period": {period}}, &s)
	} else {
		s, err = h.filteredMagpieUsage(r, cfg, period, surface, since, until)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	providers := map[string]magpieTotals{}
	models := make([]map[string]any, 0, len(s.Models))
	totalTokens := s.totalTokens()
	for _, g := range s.Models {
		row := g.metrics()
		row["provider"] = g.Provider
		row["model"] = g.Model
		row["shareRatio"] = ratio(g.totalTokens(), totalTokens)
		models = append(models, row)
		t := providers[g.Provider]
		t.add(g.magpieTotals)
		providers[g.Provider] = t
	}
	providerRows := make([]map[string]any, 0, len(providers))
	for p, t := range providers {
		row := t.metrics()
		row["provider"] = p
		row["shareRatio"] = ratio(t.totalTokens(), totalTokens)
		providerRows = append(providerRows, row)
	}
	sort.Slice(providerRows, func(i, j int) bool {
		return providerRows[i]["provider"].(string) < providerRows[j]["provider"].(string)
	})
	accounts := make([]map[string]any, 0, len(s.Accounts))
	for _, g := range s.Accounts {
		row := g.metrics()
		label := g.Account
		if label == "" {
			label = "未记录账号"
		}
		row["accountLogLabel"] = g.Provider + " · " + label
		row["ambiguous"] = g.Account == ""
		accounts = append(accounts, row)
	}
	dayTotals := map[string]magpieTotals{}
	for _, p := range s.Series {
		day := p.Time.Format(time.DateOnly)
		t := dayTotals[day]
		t.add(p.magpieTotals)
		dayTotals[day] = t
	}
	days := make([]map[string]any, 0, len(dayTotals))
	for day, t := range dayTotals {
		row := t.metrics()
		row["date"] = day
		days = append(days, row)
	}
	sort.Slice(days, func(i, j int) bool { return days[i]["date"].(string) < days[j]["date"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"gatewayType": "magpie", "bucket": s.Bucket, "range": period, "surface": surface, "since": s.Since.UnixMilli(), "generatedAt": time.Now().UTC().Format(time.RFC3339), "summary": s.metrics(), "days": days, "models": models, "providers": providerRows, "accounts": accounts})
}

// Only metadata is decoded; message bodies are neither requested separately nor persisted.
type magpieUsageRow struct {
	Time       time.Time `json:"t"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Account    string    `json:"providerAccount"`
	Host       string    `json:"host"`
	Input      int64     `json:"in"`
	Output     int64     `json:"out"`
	CacheRead  int64     `json:"cache_read"`
	CacheWrite int64     `json:"cache_write"`
	Reasoning  int64     `json:"reasoning"`
	Cost       float64   `json:"cost"`
	Priced     bool      `json:"priced"`
	Status     int       `json:"status"`
	Error      string    `json:"err"`
	Tries      int64     `json:"tries"`
	HandedOn   bool      `json:"handed_on"`
	Rejected   bool      `json:"rejected"`
}

func (h *Handler) filteredMagpieUsage(r *http.Request, cfg GatewayConfig, period, surface, since, until string) (magpieSummary, error) {
	s := magpieSummary{Period: period, Bucket: "day"}
	start, end := int64(0), int64(0)
	if since != "" {
		start, _ = strconv.ParseInt(since, 10, 64)
		end, _ = strconv.ParseInt(until, 10, 64)
		s.Since = time.UnixMilli(start)
		period = "all"
	}
	q := url.Values{"period": {period}, "limit": {"500"}}
	if surface != "all" {
		q.Set("agent", surface)
	}
	modelGroups := map[string]magpieGroup{}
	accountGroups := map[string]magpieGroup{}
	points := map[string]magpiePoint{}
	for offset, pages := 0, 0; ; pages++ {
		if pages >= 2000 {
			return s, fmt.Errorf("Magpie 筛选记录超过读取上限，请缩小时间范围")
		}
		q.Set("offset", strconv.Itoa(offset))
		var page struct {
			Rows   []magpieUsageRow `json:"rows"`
			Total  int              `json:"total"`
			Offset int              `json:"offset"`
		}
		if err := h.readMagpie(r, cfg, "usage/requests", q, &page); err != nil {
			return s, err
		}
		if page.Offset != offset || (len(page.Rows) == 0 && offset < page.Total) {
			return s, fmt.Errorf("Magpie 请求分页不完整，请重试")
		}
		for _, row := range page.Rows {
			if row.Time.IsZero() {
				return s, fmt.Errorf("Magpie 请求记录缺少有效时间")
			}
			ts := row.Time.UnixMilli()
			if since != "" && (ts < start || ts >= end) {
				continue
			}
			// Legacy local refusals have no explicit rejected flag.
			if row.Rejected || (row.Status >= 400 && row.Provider == "") {
				continue
			}
			t := magpieTotals{Input: row.Input, Output: row.Output, CacheRead: row.CacheRead, CacheWrite: row.CacheWrite, Reasoning: row.Reasoning, Cost: row.Cost}
			if !row.HandedOn {
				t.Calls = 1
				if row.Status >= 400 || row.Error != "" {
					t.Errors = 1
				}
				if row.Tries > 1 {
					t.Retried = 1
				}
			}
			if !row.Priced && t.totalTokens() > 0 {
				t.Unpriced = 1
			}
			s.magpieTotals.add(t)
			if s.Since.IsZero() || (since == "" && row.Time.Before(s.Since)) {
				s.Since = row.Time
			}
			key := row.Provider + "\x00" + row.Model
			g := modelGroups[key]
			g.Provider = row.Provider
			g.Model = row.Model
			g.magpieTotals.add(t)
			modelGroups[key] = g
			account := row.Account
			if account == "" {
				if _, a, ok := strings.Cut(row.Host, " as "); ok {
					account = a
				}
			}
			if account != "" {
				key = row.Provider + "\x00" + account
				g = accountGroups[key]
				g.Provider = row.Provider
				g.Account = account
				g.magpieTotals.add(t)
				accountGroups[key] = g
			}
			day := row.Time.Format(time.DateOnly)
			p := points[day]
			p.Time = time.Date(row.Time.Year(), row.Time.Month(), row.Time.Day(), 0, 0, 0, 0, row.Time.Location())
			p.magpieTotals.add(t)
			points[day] = p
		}
		offset += len(page.Rows)
		if offset >= page.Total {
			break
		}
	}
	for _, g := range modelGroups {
		s.Models = append(s.Models, g)
	}
	sort.Slice(s.Models, func(i, j int) bool {
		return s.Models[i].Provider+"/"+s.Models[i].Model < s.Models[j].Provider+"/"+s.Models[j].Model
	})
	for _, g := range accountGroups {
		s.Accounts = append(s.Accounts, g)
	}
	sort.Slice(s.Accounts, func(i, j int) bool {
		return s.Accounts[i].Provider+s.Accounts[i].Account < s.Accounts[j].Provider+s.Accounts[j].Account
	})
	for _, p := range points {
		s.Series = append(s.Series, p)
	}
	return s, nil
}
