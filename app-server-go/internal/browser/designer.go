package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed designer_runtime.js
var designerRuntime string

type designCommand struct {
	AgentID    string   `json:"agent_id"`
	SessionID  string   `json:"session_id"`
	TargetID   string   `json:"target_id"`
	Action     string   `json:"action"`
	DocumentID string   `json:"document_id,omitempty"`
	ElementID  string   `json:"element_id,omitempty"`
	RatioX     *float64 `json:"ratio_x,omitempty"`
	RatioY     *float64 `json:"ratio_y,omitempty"`
	Property   string   `json:"property,omitempty"`
	Value      string   `json:"value,omitempty"`
}

var designColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)
var designLength = regexp.MustCompile(`^-?([0-9]+(\.[0-9]+)?|\.[0-9]+)px$`)

func (c designCommand) validate() error {
	if c.AgentID == "" || c.SessionID == "" || c.TargetID == "" {
		return fmt.Errorf("请选择当前会话中的预览页面")
	}
	switch c.Action {
	case "state":
		return nil
	case "select":
		if c.DocumentID == "" || c.RatioX == nil || c.RatioY == nil {
			return fmt.Errorf("缺少页面或选取位置")
		}
		for _, ratio := range []*float64{c.RatioX, c.RatioY} {
			if math.IsNaN(*ratio) || math.IsInf(*ratio, 0) || *ratio < 0 || *ratio > 1 {
				return fmt.Errorf("选取位置超出页面范围")
			}
		}
	case "parent", "undo", "reset", "export":
		if c.DocumentID == "" {
			return fmt.Errorf("页面已变化，请重新打开调整界面")
		}
	case "apply":
		if c.DocumentID == "" || c.ElementID == "" {
			return fmt.Errorf("请先选取页面元素")
		}
		switch c.Property {
		case "color", "background-color":
			if !designColor.MatchString(c.Value) {
				return fmt.Errorf("请输入有效的颜色")
			}
		case "font-size", "width", "height", "border-radius", "gap",
			"padding-top", "padding-right", "padding-bottom", "padding-left",
			"margin-top", "margin-right", "margin-bottom", "margin-left":
			if !designLength.MatchString(c.Value) {
				return fmt.Errorf("请输入有效的像素值")
			}
			value, err := strconv.ParseFloat(strings.TrimSuffix(c.Value, "px"), 64)
			if err != nil || value > 4000 || value < -1000 ||
				(!strings.HasPrefix(c.Property, "margin-") && value < 0) {
				return fmt.Errorf("尺寸超出可调整范围")
			}
		default:
			return fmt.Errorf("暂不支持调整此样式")
		}
	default:
		return fmt.Errorf("未知的界面调整操作")
	}
	return nil
}

func (h *Handler) handleDesign(w http.ResponseWriter, r *http.Request) error {
	var command designCommand
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return &httpError{status: http.StatusUnprocessableEntity, message: "界面调整请求无效"}
	}
	if err := command.validate(); err != nil {
		return &httpError{status: http.StatusUnprocessableEntity, message: err.Error()}
	}
	if err := h.verifySession(r.Context(), command.AgentID, command.SessionID); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	sessionKey := command.AgentID + "::" + command.SessionID
	// Never create or adopt a tab while editing: the selected target must already
	// belong to this session.
	page, _, err := h.manager.SessionPage(ctx, sessionKey, false, false, command.TargetID)
	if err != nil {
		return err
	}
	client, err := h.manager.ConnectPage(ctx, page, sessionKey)
	if err != nil {
		return err
	}
	defer client.CloseNow()

	encoded, err := json.Marshal(command)
	if err != nil {
		return err
	}
	result, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "(" + designerRuntime + ")(" + string(encoded) + ")",
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return err
	}
	var evaluation struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evaluation); err != nil {
		return err
	}
	if len(evaluation.ExceptionDetails) > 0 || len(evaluation.Result.Value) == 0 {
		return &httpError{status: http.StatusConflict, message: "页面无法调整，请刷新预览后重试"}
	}
	var state struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(evaluation.Result.Value, &state); err != nil {
		return err
	}
	if state.Error != "" {
		return &httpError{status: http.StatusConflict, message: state.Error}
	}
	snapshot, err := h.manager.CaptureScreenshot(ctx, sessionKey, true, command.TargetID)
	if err != nil {
		return err
	}
	h.publishMirrorUpdated(ctx, command.AgentID, command.SessionID, snapshot)
	writeJSON(w, http.StatusOK, map[string]any{"state": evaluation.Result.Value, "snapshot": snapshot})
	return nil
}
