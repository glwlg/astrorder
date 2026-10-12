package protocol

import "fmt"

func (d *Daemon) beginControl() error {
	d.admissionMu.Lock()
	defer d.admissionMu.Unlock()
	if d.shutdownPending {
		return fmt.Errorf("daemon is draining; new controls are refused")
	}
	d.activeControls++
	return nil
}

func (d *Daemon) endControl() {
	d.admissionMu.Lock()
	d.activeControls--
	d.admissionMu.Unlock()
}

// CancelShutdown releases an accepted provisional admission gate only when
// its response failed. Once shutdown is signaled, new work stays prohibited.
func (d *Daemon) CancelShutdown() {
	d.admissionMu.Lock()
	defer d.admissionMu.Unlock()
	select {
	case <-d.shutdownRequested:
		return
	default:
	}
	if d.shutdownPending {
		d.shutdownPending = false
		d.registry.CancelShutdown()
	}
}
