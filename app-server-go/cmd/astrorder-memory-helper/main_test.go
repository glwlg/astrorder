package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/memory"
)

func TestMemoryHelperHomeAction(t *testing.T) {
	tempHome := t.TempDir()
	cmd := exec.Command("go", "run", ".", "home")
	cmd.Env = append(os.Environ(), "USERPROFILE="+tempHome, "HOME="+tempHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run home command failed: %v, out: %s", err, string(out))
	}
	got := filepath.ToSlash(string(bytes.TrimSpace(out)))
	want := filepath.ToSlash(tempHome)
	if got != want {
		t.Fatalf("home = %q, want %q", got, want)
	}
}

func TestMemoryHelperTargetFSRoundTripAndSizeCap(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "sample.txt")

	// 1. Write atomic
	writeReq := memory.TargetFileRequest{
		Operation:  memory.TargetFileWrite,
		Path:       testFile,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("hello helper")),
		Mode:       0o644,
	}
	writeJSON, _ := json.Marshal(writeReq)
	writeFrame, _ := memory.EncodeFrame(writeJSON)

	cmdWrite := exec.Command("go", "run", ".", "target-fs-v1")
	cmdWrite.Stdin = bytes.NewReader(writeFrame)
	var outWrite bytes.Buffer
	cmdWrite.Stdout = &outWrite
	if err := cmdWrite.Run(); err != nil {
		t.Fatalf("run write failed: %v", err)
	}
	respPayload, err := memory.DecodeFrame(outWrite.Bytes())
	if err != nil {
		t.Fatalf("decode write response frame: %v", err)
	}
	var writeResp memory.TargetFileResponse
	if err := json.Unmarshal(respPayload, &writeResp); err != nil || !writeResp.OK {
		t.Fatalf("write response not ok: %#v, err: %v", writeResp, err)
	}

	// 2. Read
	readReq := memory.TargetFileRequest{
		Operation: memory.TargetFileRead,
		Path:      testFile,
	}
	readJSON, _ := json.Marshal(readReq)
	readFrame, _ := memory.EncodeFrame(readJSON)

	cmdRead := exec.Command("go", "run", ".", "target-fs-v1")
	cmdRead.Stdin = bytes.NewReader(readFrame)
	var outRead bytes.Buffer
	cmdRead.Stdout = &outRead
	if err := cmdRead.Run(); err != nil {
		t.Fatalf("run read failed: %v", err)
	}
	readRespPayload, err := memory.DecodeFrame(outRead.Bytes())
	if err != nil {
		t.Fatalf("decode read response frame: %v", err)
	}
	var readResp memory.TargetFileResponse
	if err := json.Unmarshal(readRespPayload, &readResp); err != nil || !readResp.OK {
		t.Fatalf("read response not ok: %#v, err: %v", readResp, err)
	}
	decodedContent, _ := base64.StdEncoding.DecodeString(readResp.DataBase64)
	if string(decodedContent) != "hello helper" {
		t.Fatalf("content = %q, want 'hello helper'", string(decodedContent))
	}

	// 3. Size cap > 20,000,000 rejected
	badFrame := make([]byte, 4)
	binary.BigEndian.PutUint32(badFrame, 25_000_000)
	cmdBad := exec.Command("go", "run", ".", "target-fs-v1")
	cmdBad.Stdin = bytes.NewReader(badFrame)
	err = cmdBad.Run()
	if err == nil {
		t.Fatal("expected failure on frame length exceeding 20MB")
	}
}
