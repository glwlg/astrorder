package settingsapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPersonalTodosCRUD(t *testing.T) {
	h := newHandler(t, nil)

	// Create
	createRes := call(h, http.MethodPost, "/api/v1/todos", `{"text":"准备周五客户汇报 PPT","priority":"high","due_date":"2026-11-28","project_key":"华东客户"}`)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create todo failed: %d %s", createRes.Code, createRes.Body.String())
	}
	var created OfficeTodoItem
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != "open" || created.Priority != "high" {
		t.Fatalf("unexpected created todo: %+v", created)
	}

	// List
	listRes := call(h, http.MethodGet, "/api/v1/todos", "")
	if listRes.Code != http.StatusOK {
		t.Fatalf("list todos failed: %d %s", listRes.Code, listRes.Body.String())
	}
	var listed struct {
		Items     []OfficeTodoItem `json:"items"`
		OpenCount int              `json:"open_count"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.OpenCount != 1 {
		t.Fatalf("unexpected list result: %+v", listed)
	}

	// Patch -> done + edit text
	patchRes := call(h, http.MethodPatch, "/api/v1/todos/"+created.ID, `{"status":"done","text":"已完成周五汇报 PPT"}`)
	if patchRes.Code != http.StatusOK {
		t.Fatalf("patch todo failed: %d %s", patchRes.Code, patchRes.Body.String())
	}
	var patched OfficeTodoItem
	if err := json.Unmarshal(patchRes.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Status != "done" || patched.Text != "已完成周五汇报 PPT" {
		t.Fatalf("unexpected patched item: %+v", patched)
	}

	// Delete
	delRes := call(h, http.MethodDelete, "/api/v1/todos/"+created.ID, "")
	if delRes.Code != http.StatusOK {
		t.Fatalf("delete todo failed: %d %s", delRes.Code, delRes.Body.String())
	}
	afterList := call(h, http.MethodGet, "/api/v1/todos", "")
	_ = json.Unmarshal(afterList.Body.Bytes(), &listed)
	if len(listed.Items) != 0 {
		t.Fatalf("expected 0 items after delete, got %+v", listed.Items)
	}
}
