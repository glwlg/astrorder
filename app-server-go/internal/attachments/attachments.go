// Package attachments implements portable blob persistence and the attachment
// upload/download HTTP contracts. The blob root is always injected.
package attachments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"astrorder.dev/app-server/internal/auth"
)

const defaultMaxSize = 10 * 1024 * 1024

type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
	Owner  string `json:"owner"`
}

func Registrations() []Registration {
	return []Registration{
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/attachments", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/attachments/{attachment_id}", Auth: "browser", Owner: "go-app"},
	}
}

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Store interface {
	AddAttachment(context.Context, map[string]any) (map[string]any, error)
	GetAttachment(context.Context, string) (map[string]any, error)
}

type Publisher interface {
	Publish(context.Context, string, map[string]any) error
}

type Options struct {
	Authorizer   BrowserAuthorizer
	Store        Store
	Publisher    Publisher
	Root         string
	MaxSize      int64
	AllowedTypes []string
}

type Handler struct {
	authorizer BrowserAuthorizer
	store      Store
	publisher  Publisher
	root       string
	maxSize    int64
	allowed    map[string]bool
}

func NewHandler(options Options) (http.Handler, error) {
	if strings.TrimSpace(options.Root) == "" {
		return nil, errors.New("attachment root is required")
	}
	root, err := filepath.Abs(filepath.Clean(options.Root))
	if err != nil {
		return nil, fmt.Errorf("resolve attachment root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create attachment root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("inspect attachment root: %w", err)
	}
	maximum := options.MaxSize
	if maximum <= 0 {
		maximum = defaultMaxSize
	}
	allowed := make(map[string]bool, len(options.AllowedTypes))
	for _, item := range options.AllowedTypes {
		if normalized := normalizeMediaType(item); normalized != "" {
			allowed[normalized] = true
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("at least one attachment content type is required")
	}
	return &Handler{authorizer: options.Authorizer, store: options.Store, publisher: options.Publisher, root: root, maxSize: maximum, allowed: allowed}, nil
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if h.authorizer == nil {
		writeError(response, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := h.authorizer.AuthorizeBrowser(request); failure != nil {
		writeError(response, failure.Status, failure.Detail)
		return
	}
	if h.store == nil {
		writeError(response, http.StatusServiceUnavailable, "Attachment store is not configured")
		return
	}
	var err error
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/attachments":
		err = h.upload(response, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/v1/attachments/"):
		id := strings.TrimPrefix(request.URL.Path, "/api/v1/attachments/")
		if id == "" || strings.Contains(id, "/") {
			writeError(response, http.StatusNotFound, "Not Found")
			return
		}
		err = h.download(response, request, id)
	default:
		writeError(response, http.StatusNotFound, "Not Found")
		return
	}
	if err != nil {
		var attachmentError *Error
		if errors.As(err, &attachmentError) {
			writeError(response, attachmentError.Status, attachmentError.Detail)
		} else {
			writeError(response, http.StatusInternalServerError, "Internal server error")
		}
	}
}

type Error struct {
	Status int
	Detail string
	Err    error
}

func (e *Error) Error() string { return e.Detail }
func (e *Error) Unwrap() error { return e.Err }

func (h *Handler) upload(response http.ResponseWriter, request *http.Request) error {
	request.Body = http.MaxBytesReader(response, request.Body, h.maxSize+1024*1024)
	reader, err := request.MultipartReader()
	if err != nil {
		return &Error{Status: 400, Detail: "Invalid multipart upload", Err: err}
	}
	var sourcePath *string
	var fileName, mediaType string
	var temporary *os.File
	var temporaryName string
	var size int64
	defer func() {
		if temporary != nil {
			_ = temporary.Close()
		}
		if temporaryName != "" {
			_ = os.Remove(temporaryName)
		}
	}()
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return &Error{Status: 400, Detail: "Invalid multipart upload", Err: nextErr}
		}
		switch part.FormName() {
		case "source_path":
			value, readErr := io.ReadAll(io.LimitReader(part, 4097))
			part.Close()
			if readErr != nil || len(value) > 4096 || strings.ContainsAny(string(value), "\x00\r\n") {
				return &Error{Status: 400, Detail: "Attachment source path is invalid", Err: readErr}
			}
			clean := strings.TrimSpace(string(value))
			if clean != "" {
				sourcePath = &clean
			}
		case "file":
			if fileName != "" || part.FileName() == "" {
				part.Close()
				continue
			}
			fileName, err = safeName(part.FileName())
			if err != nil {
				part.Close()
				return err
			}
			mediaType = normalizeMediaType(part.Header.Get("Content-Type"))
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			if !h.allowed[mediaType] {
				part.Close()
				return &Error{Status: 415, Detail: "Attachment content type is not allowed"}
			}
			temporary, err = os.CreateTemp(h.root, ".attachment-*.tmp")
			if err != nil {
				part.Close()
				return &Error{Status: 500, Detail: "Failed to store attachment", Err: err}
			}
			temporaryName = temporary.Name()
			size, err = copyLimited(request.Context(), temporary, part, h.maxSize)
			part.Close()
			if err != nil {
				return err
			}
		default:
			part.Close()
		}
	}
	if fileName == "" || temporary == nil {
		return &Error{Status: 400, Detail: "Attachment file is required"}
	}
	if sourcePath != nil {
		if baseAnyPath(*sourcePath) != fileName || !isAbsoluteAnyPath(*sourcePath) {
			return &Error{Status: 400, Detail: "Attachment source path is invalid"}
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return err
	}
	storageID, err := randomHex(16)
	if err != nil {
		return err
	}
	storageName := storageID + ".blob"
	target := filepath.Join(h.root, storageName)
	published := false
	defer func() {
		if !published {
			_ = os.Remove(target)
		}
	}()
	if err := temporary.Sync(); err != nil {
		return &Error{Status: 500, Detail: "Failed to store attachment", Err: err}
	}
	if err := temporary.Close(); err != nil {
		return &Error{Status: 500, Detail: "Failed to store attachment", Err: err}
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return &Error{Status: 500, Detail: "Failed to publish attachment", Err: err}
	}
	var storedSource any
	if sourcePath != nil {
		storedSource = *sourcePath
	}
	_, err = h.store.AddAttachment(request.Context(), map[string]any{
		"id": id, "name": fileName, "media_type": mediaType, "size": size,
		"storage_name": storageName, "source_path": storedSource,
	})
	if err != nil {
		return &Error{Status: 500, Detail: "Failed to persist attachment", Err: err}
	}
	// The row now owns this blob. Event publication failure must not remove
	// committed data and leave the attachment record pointing at a missing file.
	published = true
	if h.publisher != nil {
		if err := h.publisher.Publish(request.Context(), "attachment.created", map[string]any{"id": id, "name": fileName, "size": size}); err != nil {
			return &Error{Status: 500, Detail: "Attachment event could not be published", Err: err}
		}
	}
	public := map[string]any{
		"id": id, "name": fileName, "media_type": mediaType,
		"url": "/api/v1/attachments/" + id,
	}
	writeJSON(response, http.StatusCreated, public)
	return nil
}

func (h *Handler) download(response http.ResponseWriter, request *http.Request, id string) error {
	record, err := h.store.GetAttachment(request.Context(), id)
	if err != nil {
		return &Error{Status: 500, Detail: "Failed to read attachment metadata", Err: err}
	}
	if record == nil {
		return &Error{Status: 404, Detail: "Attachment was not found"}
	}
	storageName := strings.TrimSpace(fmt.Sprint(record["storage_name"]))
	if !safeStorageName(storageName) {
		return &Error{Status: 404, Detail: "Attachment was not found"}
	}
	path := filepath.Join(h.root, storageName)
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || !samePath(filepath.Dir(canonical), h.root) {
		return &Error{Status: 404, Detail: "Attachment was not found", Err: err}
	}
	file, err := os.Open(canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return &Error{Status: 404, Detail: "Attachment was not found", Err: err}
		}
		return &Error{Status: 500, Detail: "Failed to open attachment", Err: err}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return &Error{Status: 404, Detail: "Attachment was not found", Err: err}
	}
	name := fmt.Sprint(record["name"])
	mediaType := normalizeMediaType(fmt.Sprint(record["media_type"]))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	response.Header().Set("Content-Type", mediaType)
	response.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	response.WriteHeader(http.StatusOK)
	_, err = copyContext(request.Context(), response, file)
	return err
}

func isAbsoluteAnyPath(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func baseAnyPath(value string) string {
	return filepath.Base(strings.ReplaceAll(value, `\`, "/"))
}

func safeName(filename string) (string, error) {
	if filename == "" || filename == "." || filename == ".." {
		return "", &Error{Status: 400, Detail: "Attachment filename is required"}
	}
	decodedFilename := filename
	if strings.Contains(filename, "%") {
		if decoded, err := url.PathUnescape(filename); err == nil {
			decodedFilename = decoded
		}
	}
	for _, candidate := range []string{filename, decodedFilename} {
		if strings.ContainsAny(candidate, `/\\`) || filepath.Base(candidate) != candidate || filepath.IsAbs(candidate) || (len(candidate) >= 2 && candidate[1] == ':') {
			return "", &Error{Status: 400, Detail: "Attachment filename must not contain a path"}
		}
	}
	for _, character := range filename {
		if unicode.IsControl(character) {
			return "", &Error{Status: 400, Detail: "Attachment filename contains control characters"}
		}
	}
	if len([]rune(filename)) > 256 {
		return "", &Error{Status: 400, Detail: "Attachment filename is too long"}
	}
	return decodedFilename, nil
}

func normalizeMediaType(value string) string {
	if parsed, _, err := mime.ParseMediaType(value); err == nil {
		return strings.ToLower(strings.TrimSpace(parsed))
	}
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

func safeStorageName(value string) bool {
	return value != "" && filepath.Base(value) == value && !strings.ContainsAny(value, `/\`) && !strings.Contains(value, "..") && !filepath.IsAbs(value)
}

func copyLimited(ctx context.Context, destination io.Writer, source io.Reader, maximum int64) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		remaining := maximum + 1 - total
		if remaining <= 0 {
			return total, &Error{Status: 413, Detail: "Attachment is too large"}
		}
		length, readErr := source.Read(buffer[:min(int64(len(buffer)), remaining)])
		if length > 0 {
			total += int64(length)
			if total > maximum {
				return total, &Error{Status: 413, Detail: "Attachment is too large"}
			}
			if _, err := destination.Write(buffer[:length]); err != nil {
				return total, &Error{Status: 500, Detail: "Failed to store attachment", Err: err}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, &Error{Status: 400, Detail: "Failed to read attachment upload", Err: readErr}
		}
	}
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		length, readErr := source.Read(buffer)
		if length > 0 {
			written, writeErr := destination.Write(buffer[:length])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func randomHex(bytesLength int) (string, error) {
	value := make([]byte, bytesLength)
	if _, err := rand.Read(value); err != nil {
		return "", &Error{Status: 500, Detail: "Failed to allocate attachment identifier", Err: err}
	}
	return hex.EncodeToString(value), nil
}

func samePath(left, right string) bool {
	left, _ = filepath.Abs(filepath.Clean(left))
	right, _ = filepath.Abs(filepath.Clean(right))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func writeError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
