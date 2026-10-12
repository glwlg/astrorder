package targetfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestServeHelperStreamsBinaryReadAndAtomicWrite(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "二进制 file.bin")
	payload := []byte{0, 1, 2, 0xff, '\n', 'x'}

	writeData := helperData(t, root, []string{root}, struct {
		Path string `json:"path"`
		Mode uint32 `json:"mode"`
	}{Path: target, Mode: 0o600})
	var input bytes.Buffer
	writeRequest := Request{Version: ProtocolVersion, Action: "write_atomic", Data: writeData, PayloadSize: int64(len(payload))}
	encoded, _ := json.Marshal(writeRequest)
	input.Write(encoded)
	input.WriteByte('\n')
	input.Write(payload)
	var output bytes.Buffer
	if err := ServeHelper(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	response, body := decodeHelperOutput(t, output.Bytes())
	if !response.OK || len(body) != 0 {
		t.Fatalf("response=%+v body=%v", response, body)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("written=%v err=%v", got, err)
	}

	readData := helperData(t, root, []string{root}, struct {
		Path string `json:"path"`
	}{Path: target})
	encoded, _ = json.Marshal(Request{Version: ProtocolVersion, Action: "read", Data: readData})
	input.Reset()
	input.Write(encoded)
	input.WriteByte('\n')
	output.Reset()
	if err := ServeHelper(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	response, body = decodeHelperOutput(t, output.Bytes())
	if !response.OK || response.PayloadSize != int64(len(payload)) || !bytes.Equal(body, payload) {
		t.Fatalf("response=%+v body=%v", response, body)
	}
}

func TestServeHelperRejectsTruncatedBoundedWriteWithoutPublishing(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "truncated.bin")
	data := helperData(t, root, []string{root}, struct {
		Path string `json:"path"`
		Mode uint32 `json:"mode"`
	}{Path: target, Mode: 0o600})
	request, _ := json.Marshal(Request{Version: ProtocolVersion, Action: "write_atomic", Data: data, PayloadSize: 10})
	input := append(append(request, '\n'), []byte("short")...)
	var output bytes.Buffer
	if err := ServeHelper(context.Background(), bytes.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	response, _ := decodeHelperOutput(t, output.Bytes())
	if response.OK || response.Error == nil {
		t.Fatalf("truncated write response=%+v", response)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("truncated target exists: %v", err)
	}
}

func TestServeHelperRejectsScopeEscapeWithStableError(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := helperData(t, root, []string{root}, struct {
		Path string `json:"path"`
	}{Path: outside})
	encoded, _ := json.Marshal(Request{Version: ProtocolVersion, Action: "read", Data: data})
	var output bytes.Buffer
	if err := ServeHelper(context.Background(), bytes.NewReader(append(encoded, '\n')), &output); err != nil {
		t.Fatal(err)
	}
	response, body := decodeHelperOutput(t, output.Bytes())
	if response.OK || response.Error == nil || response.Error.Status != 403 || response.Error.Code != "path_out_of_scope" || len(body) != 0 {
		t.Fatalf("unexpected response=%+v body=%v", response, body)
	}
}

func TestServeHelperExportAndExtractPreserveTreeAndRejectNoPartialSuccess(t *testing.T) {
	remote := t.TempDir()
	directory := filepath.Join(remote, "报告 dir")
	if err := os.MkdirAll(filepath.Join(directory, "子"), 0o755); err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 0xff, 'x'}
	if err := os.WriteFile(filepath.Join(directory, "子", "图.bin"), binary, 0o600); err != nil {
		t.Fatal(err)
	}
	data := helperData(t, remote, []string{remote}, struct {
		Paths []string `json:"paths"`
	}{Paths: []string{directory}})
	encoded, _ := json.Marshal(Request{Version: ProtocolVersion, Action: "export", Data: data})
	var output bytes.Buffer
	if err := ServeHelper(context.Background(), bytes.NewReader(append(encoded, '\n')), &output); err != nil {
		t.Fatal(err)
	}
	response, framed := decodeHelperOutput(t, output.Bytes())
	if !response.OK || response.PayloadSize != -1 {
		t.Fatalf("unexpected response: %+v", response)
	}
	local := t.TempDir()
	result, err := extractExport(context.Background(), bytes.NewReader(framed), local)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(local, "报告 dir")
	if len(result.Paths) != 1 || result.Paths[0] != wantRoot {
		t.Fatalf("paths=%q", result.Paths)
	}
	got, err := os.ReadFile(filepath.Join(wantRoot, "子", "图.bin"))
	if err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("exported=%v err=%v", got, err)
	}

	truncated := framed[:len(framed)-1]
	second := t.TempDir()
	if _, err := extractExport(context.Background(), bytes.NewReader(truncated), second); err == nil {
		t.Fatal("truncated export was accepted")
	}
	entries, err := os.ReadDir(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial export remained: %v", entries)
	}
}

func TestExtractExportRejectsTraversal(t *testing.T) {
	entry, _ := json.Marshal(exportEntry{Path: "../escape", Type: "file", Mode: 0o600, Size: 1})
	var framed bytes.Buffer
	_ = binary.Write(&framed, binary.BigEndian, uint32(len(entry)))
	framed.Write(entry)
	framed.WriteByte('x')
	_ = binary.Write(&framed, binary.BigEndian, uint32(0))
	if _, err := extractExport(context.Background(), &framed, t.TempDir()); err == nil {
		t.Fatal("traversal export was accepted")
	}
}

func helperData(t *testing.T, workspace string, roots []string, request any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(scopeRequest{Workspace: workspace, AllowedRoots: roots, Request: raw})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeHelperOutput(t *testing.T, output []byte) (Response, []byte) {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(output))
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(bytes.TrimSpace(line), &response); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}
