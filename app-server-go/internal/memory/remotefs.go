package memory

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxTargetFrame = 20_000_000

type TargetFileOperation string

const (
	TargetFileRead  TargetFileOperation = "read"
	TargetFileWrite TargetFileOperation = "write_atomic"
)

type TargetFileRequest struct {
	Operation  TargetFileOperation `json:"operation"`
	Path       string              `json:"path"`
	DataBase64 string              `json:"data_base64,omitempty"`
	Mode       uint32              `json:"mode,omitempty"`
}

type TargetFileResponse struct {
	OK         bool   `json:"ok"`
	DataBase64 string `json:"data_base64,omitempty"`
	NotFound   bool   `json:"not_found,omitempty"`
	Error      string `json:"error,omitempty"`
}

// FramedTargetTransport is implemented by WSL native helper and SSH/SFTP adapters.
// Requests are length-prefixed JSON so paths and content never enter a command shell.
type FramedTargetTransport interface {
	Exchange(context.Context, Target, []byte) ([]byte, error)
}

type FramedTargetFS struct {
	Transport FramedTargetTransport
}

func (f FramedTargetFS) ReadFile(ctx context.Context, target Target, filename string) ([]byte, error) {
	response, err := f.exchange(ctx, target, TargetFileRequest{Operation: TargetFileRead, Path: filename})
	if err != nil {
		return nil, err
	}
	if response.NotFound {
		return nil, ErrTargetFileNotFound
	}
	if !response.OK {
		return nil, errors.New(response.Error)
	}
	raw, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil {
		return nil, errors.New("target filesystem returned invalid base64")
	}
	return raw, nil
}

func (f FramedTargetFS) WriteFileAtomic(ctx context.Context, target Target, filename string, raw []byte, mode uint32) error {
	response, err := f.exchange(ctx, target, TargetFileRequest{
		Operation: TargetFileWrite, Path: filename, DataBase64: base64.StdEncoding.EncodeToString(raw), Mode: mode,
	})
	if err != nil {
		return err
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	return nil
}

func (f FramedTargetFS) exchange(ctx context.Context, target Target, request TargetFileRequest) (TargetFileResponse, error) {
	if f.Transport == nil {
		return TargetFileResponse{}, errors.New("target filesystem transport is required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return TargetFileResponse{}, err
	}
	frame, err := EncodeFrame(payload)
	if err != nil {
		return TargetFileResponse{}, err
	}
	responseFrame, err := f.Transport.Exchange(ctx, target, frame)
	if err != nil {
		return TargetFileResponse{}, err
	}
	responsePayload, err := DecodeFrame(responseFrame)
	if err != nil {
		return TargetFileResponse{}, err
	}
	var response TargetFileResponse
	if err := decodeSingleJSON(responsePayload, &response); err != nil {
		return TargetFileResponse{}, fmt.Errorf("decode target filesystem response: %w", err)
	}
	return response, nil
}

func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) > maxTargetFrame {
		return nil, errors.New("target filesystem frame exceeds size limit")
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

func DecodeFrame(frame []byte) ([]byte, error) {
	if len(frame) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	size := binary.BigEndian.Uint32(frame[:4])
	if size > maxTargetFrame || int(size) != len(frame)-4 {
		return nil, errors.New("invalid target filesystem frame length")
	}
	return frame[4:], nil
}
