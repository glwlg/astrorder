package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

type frameTransportFixture struct {
	request TargetFileRequest
	files   map[string][]byte
}

func (f *frameTransportFixture) Exchange(_ context.Context, _ Target, frame []byte) ([]byte, error) {
	payload, err := DecodeFrame(frame)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, &f.request); err != nil {
		return nil, err
	}
	response := TargetFileResponse{OK: true}
	switch f.request.Operation {
	case TargetFileRead:
		raw, ok := f.files[f.request.Path]
		if !ok {
			response.OK = false
			response.NotFound = true
		} else {
			response.DataBase64 = base64.StdEncoding.EncodeToString(raw)
		}
	case TargetFileWrite:
		raw, decodeErr := base64.StdEncoding.DecodeString(f.request.DataBase64)
		if decodeErr != nil {
			return nil, decodeErr
		}
		f.files[f.request.Path] = raw
	}
	encoded, _ := json.Marshal(response)
	return EncodeFrame(encoded)
}

func TestFramedTargetFSRoundTripDoesNotShellInterpolate(t *testing.T) {
	t.Parallel()
	transport := &frameTransportFixture{files: map[string][]byte{}}
	filesystem := FramedTargetFS{Transport: transport}
	target := Target{ID: "wsl", Kind: TargetWSL}
	filename := "/home/user/weird ; $(touch no) ' file"
	raw := []byte("secret $() `cmd` \x00 bytes")
	if err := filesystem.WriteFileAtomic(context.Background(), target, filename, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if transport.request.Path != filename || transport.request.Mode != 0o600 {
		t.Fatalf("request = %#v", transport.request)
	}
	actual, err := filesystem.ReadFile(context.Background(), target, filename)
	if err != nil || !reflect.DeepEqual(actual, raw) {
		t.Fatalf("actual=%q err=%v", actual, err)
	}
}

func TestDecodeFrameRejectsInvalidLength(t *testing.T) {
	t.Parallel()
	if _, err := DecodeFrame([]byte{0, 0, 0, 10, 1}); err == nil {
		t.Fatal("accepted invalid frame")
	}
}
