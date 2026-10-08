package protocol

import (
	"astrorder.dev/session-daemon/internal/modelconfig"
	"context"
	"fmt"
	"time"
)

// ConfigureModelConfig only binds an explicitly selected configuration home.
// Registration never writes native files or starts a native process.
func (d *Daemon) ConfigureModelConfig(cfg modelconfig.Config) error {
	if cfg.Busy == nil && cfg.BusyFor == nil {
		cfg.BusyFor = d.registry.BusyFor
	}
	if cfg.Reload == nil {
		cfg.Reload = d.registry.ReloadConfig
	}
	service, err := modelconfig.New(cfg)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.modelConfig != nil {
		return fmt.Errorf("model configuration service is already configured")
	}
	d.modelConfig = service
	ctx, stop := context.WithCancel(context.Background())
	d.modelWake = make(chan struct{}, 1)
	d.modelDone = make(chan struct{})
	d.modelStop = stop
	go d.runModelReloads(ctx, service, d.modelWake, d.modelDone)
	return nil
}
func (d *Daemon) modelConfigControl(parent context.Context, request map[string]any) map[string]any {
	action, _ := request["action"].(string)
	if action != "model_config.plan" && action != "model_config.apply" && action != "model_config.reload" {
		return errorResponse(request["request_id"], "unsupported model configuration action")
	}
	if action != "model_config.reload" {
		target, ok := request["target"].(map[string]any)
		if !ok {
			return errorResponse(request["request_id"], "model configuration target is invalid")
		}
		kind, _ := target["kind"].(string)
		if kind != "local" {
			return errorResponse(request["request_id"], "model configuration target is not configured")
		}
	}
	d.mu.Lock()
	service := d.modelConfig
	d.mu.Unlock()
	if service == nil {
		return errorResponse(request["request_id"], "model configuration service is not configured")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	result, err := service.Execute(ctx, action, request)
	if err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	return map[string]any{"action": action + ".result", "request_id": request["request_id"], "daemon_id": d.daemonID, "result": result}
}
