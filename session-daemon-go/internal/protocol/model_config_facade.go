package protocol

import (
	"context"
	"fmt"
)

// ExecuteModelConfig executes a model configuration control request (plan, apply, reload)
// using daemon admission control, shared operation authority in control(), and the existing model configuration service.
func (d *Daemon) ExecuteModelConfig(ctx context.Context, request map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.beginControl(); err != nil {
		return nil, fmt.Errorf("model configuration admission refused: %w", err)
	}
	defer d.endControl()

	res := d.control(ctx, request)
	if res["action"] == "error" {
		if res["operation_state"] == "uncertain" {
			return map[string]any{"operation_state": "uncertain"}, fmt.Errorf("%v", res["detail"])
		}
		return nil, fmt.Errorf("%v", res["detail"])
	}
	result, _ := res["result"].(map[string]any)
	return result, nil
}

// ModelConfigControl executes a model configuration control request with full response envelope.
func (d *Daemon) ModelConfigControl(ctx context.Context, request map[string]any) map[string]any {
	if err := ctx.Err(); err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	if err := d.beginControl(); err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	defer d.endControl()
	return d.modelConfigControl(ctx, request)
}
