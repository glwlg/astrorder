package lifecycle

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// DaemonUpdateResult is shared by the read-only check and the update command.
type DaemonUpdateResult struct {
	DaemonStatusResult
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	CandidatePath   string `json:"candidate_path"`
	BackupPath      string `json:"backup_path,omitempty"`
	Message         string `json:"message"`
}

// CheckDaemonUpdate only inspects the candidate beside the installed executable.
// A missing update never requires credentials or contacts the running daemon.
func CheckDaemonUpdate(executable string) (DaemonUpdateResult, error) {
	result := DaemonUpdateResult{
		DaemonStatusResult: DaemonStatusResult{State: "no_update"},
		CandidatePath:      executable + ".new",
		Message:            "没有可用的小内核更新",
	}
	if !filepath.IsAbs(executable) || filepath.Base(executable) != "astrorder-sessiond.exe" {
		return result, errors.New("daemon update requires the installed astrorder-sessiond.exe path")
	}
	info, err := os.Lstat(result.CandidatePath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("inspect daemon update: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return result, errors.New("daemon update must be a nonempty regular file")
	}
	result.State = "update_available"
	result.UpdateAvailable = true
	result.Message = "发现小内核更新"
	return result, nil
}

// UpdateDaemonWithLifecycle owns the entire stop/replace/start transaction under
// the same lock used by normal starts and stops. Call it from the independent ctl.
func UpdateDaemonWithLifecycle(ctx context.Context, options DaemonOptions, confirmActive bool) (DaemonUpdateResult, error) {
	result, err := CheckDaemonUpdate(options.ExecutablePath)
	if err != nil || !result.UpdateAvailable {
		return result, err
	}
	if err := validateDaemonUpdateBinary(result.CandidatePath); err != nil {
		return result, err
	}
	validated, err := validateDaemonOptions(options)
	if err != nil {
		return result, err
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return result, err
	}
	defer unlock()

	// Copy a fixed snapshot before shutdown, so a concurrently staged .new
	// cannot alter the bytes that were checked for this operation.
	source, err := os.Open(result.CandidatePath)
	if err != nil {
		return result, err
	}
	defer source.Close()
	candidateInfo, err := source.Stat()
	if err != nil {
		return result, err
	}
	staged, err := os.CreateTemp(filepath.Dir(options.ExecutablePath), ".astrorder-update-*.exe")
	if err != nil {
		return result, fmt.Errorf("prepare daemon update: %w", err)
	}
	defer os.Remove(staged.Name())
	_, copyErr := io.Copy(staged, source)
	syncErr := staged.Sync()
	closeErr := staged.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return result, fmt.Errorf("stage daemon update: %w", err)
	}
	if err := validateDaemonUpdateBinary(staged.Name()); err != nil {
		return result, err
	}

	status, err := daemonStatus(ctx, validated)
	if err != nil {
		return result, err
	}
	if status.State != StateStopped && (!status.IdentityVerified || !status.PortOwned) {
		return result, errors.New("refusing daemon update without exact process and listener ownership")
	}

	// A unique backup is prepared before stopping; a disk/write failure leaves
	// the original daemon running and its executable untouched.
	original, err := os.Open(options.ExecutablePath)
	if err != nil {
		return result, err
	}
	backup, err := os.CreateTemp(filepath.Dir(options.ExecutablePath), "astrorder-sessiond.exe.backup-*")
	if err != nil {
		original.Close()
		return result, err
	}
	_, copyErr = io.Copy(backup, original)
	original.Close()
	syncErr = backup.Sync()
	closeErr = backup.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		os.Remove(backup.Name())
		return result, fmt.Errorf("back up daemon executable: %w", err)
	}
	result.BackupPath = backup.Name()
	if _, err := stopDaemonLocked(ctx, validated, confirmActive); err != nil {
		os.Remove(backup.Name())
		result.BackupPath = ""
		return result, fmt.Errorf("stop daemon before update: %w", err)
	}
	if err := replaceDaemonExecutable(ctx, staged.Name(), options.ExecutablePath); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		_, recoveryErr := startDaemonLocked(recoveryCtx, validated)
		return result, errors.Join(fmt.Errorf("replace daemon executable: %w", err), recoveryErr)
	}
	status, startErr := startDaemonLocked(ctx, validated)
	if startErr != nil {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if err := replaceDaemonExecutable(recoveryCtx, result.BackupPath, options.ExecutablePath); err != nil {
			return result, errors.Join(startErr, fmt.Errorf("restore daemon executable from %s: %w", result.BackupPath, err))
		}
		status, recoveryErr := startDaemonLocked(recoveryCtx, validated)
		result.DaemonStatusResult = status
		return result, errors.Join(fmt.Errorf("updated daemon failed to start; previous executable restored: %w", startErr), recoveryErr)
	}
	result.DaemonStatusResult = status
	result.Updated = true
	result.Message = "小内核更新完成"
	// Keep a newly staged candidate if it replaced the file while we worked.
	source.Close()
	if current, statErr := os.Stat(result.CandidatePath); statErr == nil && os.SameFile(candidateInfo, current) && current.Size() == candidateInfo.Size() && current.ModTime() == candidateInfo.ModTime() {
		if err := os.Remove(result.CandidatePath); err != nil {
			result.Message += "；待更新文件清理失败：" + err.Error()
		} else {
			result.UpdateAvailable = false
		}
	}
	return result, nil
}

func replaceDaemonExecutable(ctx context.Context, source, destination string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := replaceFile(source, destination)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func validateDaemonUpdateBinary(path string) error {
	binary, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("invalid daemon update executable: %w", err)
	}
	defer binary.Close()
	machine := map[string]uint16{"amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64, "386": pe.IMAGE_FILE_MACHINE_I386}[runtime.GOARCH]
	if binary.Machine != machine || binary.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || binary.Characteristics&pe.IMAGE_FILE_DLL != 0 || binary.OptionalHeader == nil {
		return errors.New("daemon update is not a compatible Windows executable")
	}
	return nil
}
