//go:build linux

package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type linuxTerminal struct {
	ptm     *os.File
	cmd     *exec.Cmd
	closeMu sync.Mutex
	closed  bool
}

func defaultPlatformTerminal(shell string, cwd string, env []string, cols, rows int) (Terminal, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open ptmx: %w", err)
	}

	if err := unix.IoctlSetPointerInt(int(ptm.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		ptm.Close()
		return nil, fmt.Errorf("unlockpt: %w", err)
	}

	ptn, err := unix.IoctlGetInt(int(ptm.Fd()), unix.TIOCGPTN)
	if err != nil {
		ptm.Close()
		return nil, fmt.Errorf("ptsname: %w", err)
	}

	ptsPath := fmt.Sprintf("/dev/pts/%d", ptn)
	pts, err := os.OpenFile(ptsPath, os.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		ptm.Close()
		return nil, fmt.Errorf("open pts %s: %w", ptsPath, err)
	}
	defer pts.Close()

	_ = unix.IoctlSetWinsize(int(ptm.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
		Col: uint16(cols),
		Row: uint16(rows),
	})

	cmd := exec.Command(shell)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := cmd.Start(); err != nil {
		ptm.Close()
		return nil, fmt.Errorf("start pty process: %w", err)
	}

	return &linuxTerminal{
		ptm: ptm,
		cmd: cmd,
	}, nil
}

func (t *linuxTerminal) Read(p []byte) (n int, err error) {
	n, err = t.ptm.Read(p)
	if err != nil {
		if pathErr, ok := err.(*os.PathError); ok {
			err = pathErr.Err
		}
		if errors.Is(err, syscall.EIO) || errors.Is(err, io.EOF) {
			return n, io.EOF
		}
		return n, err
	}
	return n, nil
}

func (t *linuxTerminal) Write(p []byte) (n int, err error) {
	return t.ptm.Write(p)
}

func (t *linuxTerminal) PID() int {
	if t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

func (t *linuxTerminal) Resize(cols, rows int) error {
	return unix.IoctlSetWinsize(int(t.ptm.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
		Col: uint16(cols),
		Row: uint16(rows),
	})
}

func (t *linuxTerminal) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true

	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}
	return t.ptm.Close()
}

func findDefaultShell() string {
	for _, sh := range []string{"zsh", "bash", "sh"} {
		if path, err := exec.LookPath(sh); err == nil {
			return path
		}
	}
	return "/bin/sh"
}
