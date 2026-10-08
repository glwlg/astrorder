package protocol

import (
	"astrorder.dev/session-daemon/internal/modelconfig"
	"context"
	"fmt"
	"time"
)

func (d *Daemon) wakeModelReloads() {
	d.mu.Lock()
	wake := d.modelWake
	d.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
func (d *Daemon) runModelReloads(ctx context.Context, service *modelconfig.Service, wake <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
			_, err := service.Drain(attempt)
			cancel()
			d.mu.Lock()
			d.modelReloadError = ""
			if err != nil && ctx.Err() == nil {
				d.modelReloadError = err.Error()
			}
			d.mu.Unlock()
		}
	}
}
func (d *Daemon) closeModelConfig() error {
	d.mu.Lock()
	stop, done := d.modelStop, d.modelDone
	d.modelWake = nil
	d.mu.Unlock()
	if stop == nil {
		return nil
	}
	stop()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("native model reload worker exit was not confirmed")
	}
}
