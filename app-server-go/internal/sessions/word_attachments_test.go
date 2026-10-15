package sessions

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/attachments"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/targetfs"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestWordAttachmentUploadAndCodexSend(t *testing.T) {
	var docx bytes.Buffer
	archive := zip.NewWriter(&docx)
	for _, entry := range []struct{ name, content string }{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>接口替换说明</w:t></w:r></w:p></w:body></w:document>`},
	} {
		file, err := archive.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(file, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, media string
		content     []byte
	}{
		{"接口说明.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx.Bytes()},
		{"旧版说明.doc", "application/msword", []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 0x00, 0xff}},
		{"未识别类型.docx", "application/octet-stream", docx.Bytes()},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sent *pb.SendRequest
			srv := &mockDaemonServer{sendCb: func(request *pb.SendRequest) (*pb.CommandReceipt, error) {
				sent = request
				return &pb.CommandReceipt{
					OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true,
					Session: request.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE,
					Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
				}, nil
			}}
			h, st, _, cleanup := setupTestServer(t, srv)
			defer cleanup()
			ctx := context.Background()
			work := t.TempDir()
			h.attachmentsDir = t.TempDir()
			h.resolver = targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: work, AllowedRoots: []string{work}})
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "codex", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "word-session", "agent_id": "codex", "title": "Word fixture", "workspace": work, "status": "idle", "control_state": "owned", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			upload, err := attachments.NewHandler(attachments.Options{Authorizer: h.authorizer, Store: st, Root: h.attachmentsDir, AllowedTypes: config.DefaultAttachmentTypes()})
			if err != nil {
				t.Fatal(err)
			}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", `form-data; name="file"; filename="`+test.name+`"`)
			header.Set("Content-Type", test.media)
			part, err := form.CreatePart(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(test.content); err != nil {
				t.Fatal(err)
			}
			if err = form.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/attachments", &body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			request.Header.Set("Authorization", "Bearer browser-sec")
			response := httptest.NewRecorder()
			upload.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("Word upload=%d %s", response.Code, response.Body.String())
			}
			var record struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				MediaType string `json:"media_type"`
			}
			if err = json.Unmarshal(response.Body.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record.ID == "" || record.Name != test.name || record.MediaType != test.media {
				t.Fatalf("Word metadata=%+v", record)
			}
			request = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+record.ID, nil)
			request.Header.Set("Authorization", "Bearer browser-sec")
			response = httptest.NewRecorder()
			upload.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), test.content) {
				t.Fatal("downloaded Word bytes differ from upload")
			}

			command, err := h.Submit(ctx, map[string]any{"id": "word-command", "agent_id": "codex", "session_id": "word-session", "action": "send", "text": "请读取附件中的接口说明", "attachment_ids": []any{record.ID}})
			if err != nil || command["state"] != "accepted" || sent == nil {
				t.Fatalf("Word dispatch: command=%+v err=%v sent=%v", command, err, sent != nil)
			}
			if sent.Session.GetAgentId() != "codex" || sent.Session.SessionId != "word-session" || len(sent.Input) != 1 || sent.Input[0].GetText() == nil {
				t.Fatalf("incorrect Word dispatch scope/input: %+v", sent)
			}
			matches, err := filepath.Glob(filepath.Join(work, ".astrorder", "attachments", "*", test.name))
			if err != nil || len(matches) != 1 {
				t.Fatalf("staged Word files=%v err=%v", matches, err)
			}
			staged, err := os.ReadFile(matches[0])
			if err != nil || !bytes.Equal(staged, test.content) {
				t.Fatal("Word bytes changed while staging for Codex")
			}
			text := sent.Input[0].GetText().Text
			if !strings.Contains(text, strings.ReplaceAll(matches[0], "\\", "/")) || !strings.Contains(text, "请读取附件中的接口说明") || strings.Contains(text, h.attachmentsDir) {
				t.Fatalf("Codex did not receive the staged Word path and original request: %s", text)
			}
		})
	}
}
