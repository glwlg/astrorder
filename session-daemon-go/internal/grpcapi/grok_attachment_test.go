package grpcapi_test

import (
	"encoding/base64"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestGrokSendPreservesAttachmentBytesOverOfficialGRPC(t *testing.T) {
	f := startFixture(t)
	a := &controlRuntimeFixture{}
	f.daemon.RegisterRuntime("grok", a)
	client := pb.NewSessionControlClient(f.conn)
	_, err := client.AttachSession(f.ctx, &pb.AttachSessionRequest{Session: &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("fixture")}, Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK}})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("中文附件\n\x00literal bytes")
	_, err = client.Send(f.ctx, &pb.SendRequest{Meta: controlMeta("grok-attachment-send"), Session: &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("fixture")}, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "read attachment"}}}, {Part: &pb.InputPart_Attachment{Attachment: &pb.AttachmentInput{Name: "example.txt", MediaType: "text/plain", Content: data}}}}})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) != 1 {
		t.Fatalf("expected one native send: %+v", a.requests)
	}
	attachments, ok := a.requests[0].Fields["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("lost attachments: %+v", a.requests[0].Fields)
	}
	attachment := attachments[0].(map[string]any)
	if attachment["name"] != "example.txt" || attachment["media_type"] != "text/plain" || attachment["content_base64"] != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("attachment changed: %+v", attachment)
	}
}
