package settingsapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	todosBasePath = "/api/v1/todos"
	prefTodosKey  = "office:todos"
	maxTodosCount = 500
)

var todosMu sync.Mutex

// OfficeTodoItem represents a personal/office task managed inside the unified Action Inbox.
type OfficeTodoItem struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Status     string `json:"status"` // "open" | "done"
	Priority   string `json:"priority"` // "high" | "medium" | "low"
	DueDate    string `json:"due_date,omitempty"`
	Owner      string `json:"owner,omitempty"`
	ProjectKey string `json:"project_key,omitempty"`
	Note       string `json:"note,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

func randomTodoID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "todo_" + hex.EncodeToString(b[:])
}

func normalizeTodoStatus(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "done") {
		return "done"
	}
	return "open"
}

func normalizeTodoPriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "high", "高", "p0", "p1":
		return "high"
	case "low", "低", "p3":
		return "low"
	default:
		return "medium"
	}
}

func matchesTodos(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	if clean == todosBasePath {
		return method == http.MethodGet || method == http.MethodPost
	}
	if strings.HasPrefix(clean, todosBasePath+"/") {
		id := strings.TrimPrefix(clean, todosBasePath+"/")
		if id != "" && !strings.Contains(id, "/") {
			return method == http.MethodPatch || method == http.MethodDelete
		}
	}
	return false
}

func (h *handler) serveTodos(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimRight(r.URL.Path, "/")
	if clean == todosBasePath {
		if r.Method == http.MethodGet {
			h.handleListTodos(w, r)
		} else {
			h.handleCreateTodo(w, r)
		}
		return
	}
	if strings.HasPrefix(clean, todosBasePath+"/") {
		id := strings.TrimPrefix(clean, todosBasePath+"/")
		if r.Method == http.MethodPatch {
			h.handlePatchTodo(w, r, id)
			return
		}
		if r.Method == http.MethodDelete {
			h.handleDeleteTodo(w, r, id)
			return
		}
	}
	fail(w, http.StatusNotFound, "Not Found")
}

func (h *handler) loadTodos(ctx context.Context) ([]OfficeTodoItem, error) {
	if h.Store == nil {
		return []OfficeTodoItem{}, nil
	}
	pref, err := h.Store.GetPreference(ctx, prefTodosKey)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []OfficeTodoItem{}, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return []OfficeTodoItem{}, nil
	}
	var list []OfficeTodoItem
	if err := json.Unmarshal(raw, &list); err != nil {
		return []OfficeTodoItem{}, nil
	}
	for i := range list {
		list[i].Status = normalizeTodoStatus(list[i].Status)
		list[i].Priority = normalizeTodoPriority(list[i].Priority)
	}
	sortTodos(list)
	return list, nil
}

func sortTodos(list []OfficeTodoItem) {
	priorityRank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Status != list[j].Status {
			return list[i].Status == "open"
		}
		if list[i].DueDate != list[j].DueDate {
			if list[i].DueDate == "" {
				return false
			}
			if list[j].DueDate == "" {
				return true
			}
			return list[i].DueDate < list[j].DueDate
		}
		if priorityRank[list[i].Priority] != priorityRank[list[j].Priority] {
			return priorityRank[list[i].Priority] < priorityRank[list[j].Priority]
		}
		return list[i].UpdatedAt > list[j].UpdatedAt
	})
}

func (h *handler) saveTodos(ctx context.Context, list []OfficeTodoItem) error {
	if h.Store == nil {
		return errors.New("store unavailable")
	}
	sortTodos(list)
	if len(list) > maxTodosCount {
		list = list[:maxTodosCount]
	}
	return h.Store.SetPreference(ctx, prefTodosKey, list, true)
}

func (h *handler) handleListTodos(w http.ResponseWriter, r *http.Request) {
	list, err := h.loadTodos(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取待办列表失败")
		return
	}
	openCount := 0
	for _, it := range list {
		if it.Status == "open" {
			openCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      list,
		"open_count": openCount,
	})
}

func (h *handler) handleCreateTodo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text       string `json:"text"`
		Priority   string `json:"priority"`
		DueDate    string `json:"due_date"`
		Owner      string `json:"owner"`
		ProjectKey string `json:"project_key"`
		Note       string `json:"note"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		fail(w, http.StatusUnprocessableEntity, "待办内容不能为空")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item := OfficeTodoItem{
		ID:         randomTodoID(),
		Text:       text,
		Status:     "open",
		Priority:   normalizeTodoPriority(req.Priority),
		DueDate:    strings.TrimSpace(req.DueDate),
		Owner:      strings.TrimSpace(req.Owner),
		ProjectKey: strings.TrimSpace(req.ProjectKey),
		Note:       strings.TrimSpace(req.Note),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	todosMu.Lock()
	defer todosMu.Unlock()
	list, err := h.loadTodos(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取待办列表失败")
		return
	}
	list = append([]OfficeTodoItem{item}, list...)
	if err := h.saveTodos(r.Context(), list); err != nil {
		fail(w, http.StatusInternalServerError, "保存待办失败")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) handlePatchTodo(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Text       *string `json:"text"`
		Status     *string `json:"status"`
		Priority   *string `json:"priority"`
		DueDate    *string `json:"due_date"`
		Owner      *string `json:"owner"`
		ProjectKey *string `json:"project_key"`
		Note       *string `json:"note"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	todosMu.Lock()
	defer todosMu.Unlock()
	list, err := h.loadTodos(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取待办列表失败")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if req.Text != nil && strings.TrimSpace(*req.Text) != "" {
			list[i].Text = strings.TrimSpace(*req.Text)
		}
		if req.Status != nil {
			list[i].Status = normalizeTodoStatus(*req.Status)
		}
		if req.Priority != nil {
			list[i].Priority = normalizeTodoPriority(*req.Priority)
		}
		if req.DueDate != nil {
			list[i].DueDate = strings.TrimSpace(*req.DueDate)
		}
		if req.Owner != nil {
			list[i].Owner = strings.TrimSpace(*req.Owner)
		}
		if req.ProjectKey != nil {
			list[i].ProjectKey = strings.TrimSpace(*req.ProjectKey)
		}
		if req.Note != nil {
			list[i].Note = strings.TrimSpace(*req.Note)
		}
		list[i].UpdatedAt = now
		updated := list[i]
		if err := h.saveTodos(r.Context(), list); err != nil {
			fail(w, http.StatusInternalServerError, "更新待办失败")
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}
	fail(w, http.StatusNotFound, "未找到对应待办")
}

func (h *handler) handleDeleteTodo(w http.ResponseWriter, r *http.Request, id string) {
	todosMu.Lock()
	defer todosMu.Unlock()
	list, err := h.loadTodos(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取待办列表失败")
		return
	}
	next := make([]OfficeTodoItem, 0, len(list))
	found := false
	for _, it := range list {
		if it.ID == id {
			found = true
			continue
		}
		next = append(next, it)
	}
	if !found {
		fail(w, http.StatusNotFound, "未找到对应待办")
		return
	}
	if err := h.saveTodos(r.Context(), next); err != nil {
		fail(w, http.StatusInternalServerError, "删除待办失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}
