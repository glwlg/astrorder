package protocol

import (
	"astrorder.dev/session-daemon/internal/events"
	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"encoding/json"
	"errors"
)

func (d *Daemon) ConfigureRetention(total, perSession, seconds int64) error {
	return d.journal.ConfigureRetention(total, perSession, seconds)
}

func Open(secret, path string) (*Daemon, error) {
	journal, err := events.Open(path, 2000)
	if err != nil {
		return nil, err
	}
	daemon := &Daemon{
		shutdownRequested: make(chan struct{}),
		secret:            secret,
		daemonID:          journal.DaemonID(),
		journal:           journal,
		registry:          core.NewRegistry(),
		connectors:        map[string]map[string]any{},
		agentControls:     map[string]string{},
		pendingEvents:     map[string][]map[string]any{},
	}
	if err = daemon.markLostOwners(); err != nil {
		_ = daemon.Close()
		return nil, err
	}
	return daemon, nil
}

// markLostOwners records that a restarted process cannot adopt native stdio.
// Running and waiting-approval rows become visible errors. Idle and existing
// errors are left unchanged, and nothing is re-sent to the native agent.
func (d *Daemon) markLostOwners() error {
	for id, state := range d.journal.Statuses() {
		status, _ := state["status"].(string)
		if status != "running" && status != "waiting_approval" {
			continue
		}
		prior, err := d.journal.Replay(id, 0)
		if err != nil {
			return err
		}
		payload := map[string]any{
			"adopted":         false,
			"previous_status": status,
			"reason":          "native process handle does not survive daemon restart",
		}
		if pid := latestOwnerPID(prior); pid > 0 {
			payload["owner_pid"] = pid
			payload["orphan_alive"] = process.Alive(pid)
		} else {
			payload["orphan_alive"] = false
		}
		if _, err = d.journal.Commit(id, "runtime.ownership_lost", payload, "error"); err != nil {
			return err
		}
	}
	return nil
}

func latestOwnerPID(frames []events.Frame) int {
	pid := 0
	for _, frame := range frames {
		if frame.Payload == nil {
			continue
		}
		switch value := frame.Payload["owner_pid"].(type) {
		case json.Number:
			if parsed, err := value.Int64(); err == nil && parsed > 0 && int64(int(parsed)) == parsed {
				pid = int(parsed)
			}
		case float64:
			if value > 0 {
				pid = int(value)
			}
		case int:
			if value > 0 {
				pid = value
			}
		case int64:
			if value > 0 {
				pid = int(value)
			}
		}
	}
	return pid
}

func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		var extraErr error
		d.mu.Lock()
		closers := d.closers
		d.closers = nil
		d.mu.Unlock()
		for _, c := range closers {
			if err := c(); err != nil {
				extraErr = errors.Join(extraErr, err)
			}
		}
		d.closeErr = errors.Join(d.closeModelConfig(), d.registry.Close(), d.journal.Close(), extraErr)
	})
	return d.closeErr
}
