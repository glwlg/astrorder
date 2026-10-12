package contracts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestGoPythonWireInterop(t *testing.T) {
	python := os.Getenv("ASTRORDER_INTEROP_PYTHON")
	if python == "" {
		t.Skip("set ASTRORDER_INTEROP_PYTHON to run transitional Go/Python wire interoperability")
	}
	empty := ""
	zero := uint32(0)
	falseValue := false
	nativeBytes := []byte(`{"request_id":9007199254740993,"native_unknown":{"unsigned":18446744073709551615}}`)
	terminalBytes := []byte{0x00, 0xff, 0x80, 0xe2, 0x82, 0xac, 0x0d, 0x0a}
	input := &pb.InteropFixture{
		Present: &pb.PresenceFixture{
			OptionalText:  &empty,
			OptionalCount: &zero,
			Enabled:       &falseValue,
			NullableText:  &pb.NullableString{State: &pb.NullableString_Null{Null: pb.NullValue_NULL_VALUE_NULL}},
		},
		Absent:                &pb.PresenceFixture{},
		SignedAboveJavascript: 9_007_199_254_740_993,
		UnsignedValue:         18_446_744_073_709_551_615,
		Native:                &pb.NativeJson{Schema: "interop.native@1", Utf8Json: nativeBytes},
		Terminal:              &pb.TerminalOutput{Data: terminalBytes, ChunkSeq: 9_007_199_254_740_994},
	}
	wire, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	unknown := protowire.AppendTag(nil, 19001, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte{0xde, 0xad, 0x00, 0xbe, 0xef})
	wire = append(wire, unknown...)

	temp := t.TempDir()
	inputPath := filepath.Join(temp, "go-to-python.binpb")
	outputPath := filepath.Join(temp, "python-to-go.binpb")
	if err := os.WriteFile(inputPath, wire, 0o600); err != nil {
		t.Fatal(err)
	}
	generatedRoot, err := filepath.Abs(filepath.Join("gen", "python"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join([]string{
		"import pathlib, sys",
		"sys.path.insert(0, sys.argv[1])",
		"from astrorder.sessiond.v1 import sessiond_pb2 as pb",
		"message = pb.InteropFixture()",
		"message.ParseFromString(pathlib.Path(sys.argv[2]).read_bytes())",
		"assert message.present.HasField('optional_text') and message.present.optional_text == ''",
		"assert message.present.HasField('optional_count') and message.present.optional_count == 0",
		"assert message.present.HasField('enabled') and message.present.enabled is False",
		"assert message.present.nullable_text.WhichOneof('state') == 'null'",
		"assert not message.absent.HasField('optional_text')",
		"assert not message.absent.HasField('optional_count')",
		"assert not message.absent.HasField('enabled')",
		"assert not message.absent.HasField('nullable_text')",
		"assert message.signed_above_javascript == 9007199254740993",
		"assert message.unsigned_value == 18446744073709551615",
		"assert message.native.utf8_json == b'{\"request_id\":9007199254740993,\"native_unknown\":{\"unsigned\":18446744073709551615}}'",
		"assert message.terminal.data == bytes([0, 255, 128, 226, 130, 172, 13, 10])",
		"message.present.optional_text = 'modified-by-python'",
		"message.native.sha256 = 'python-preserved-native-bytes'",
		"message.terminal.chunk_seq += 1",
		"pathlib.Path(sys.argv[3]).write_bytes(message.SerializeToString())",
	}, ";")
	cmd := exec.Command(python, "-c", script, filepath.ToSlash(generatedRoot), filepath.ToSlash(inputPath), filepath.ToSlash(outputPath))
	cmd.Env = toolEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python protobuf subprocess failed: %v\n%s", err, output)
	}
	returned, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var got pb.InteropFixture
	if err := proto.Unmarshal(returned, &got); err != nil {
		t.Fatal(err)
	}
	if got.Present.OptionalText == nil || *got.Present.OptionalText != "modified-by-python" {
		t.Fatalf("Python modification was not decoded by Go: %#v", got.Present.OptionalText)
	}
	if got.Present.OptionalCount == nil || *got.Present.OptionalCount != 0 || got.Present.Enabled == nil || *got.Present.Enabled {
		t.Fatalf("present zero/false changed: %+v", got.Present)
	}
	if got.Absent.OptionalText != nil || got.Absent.OptionalCount != nil || got.Absent.Enabled != nil || got.Absent.NullableText != nil {
		t.Fatalf("absent fields became present: %+v", got.Absent)
	}
	if got.SignedAboveJavascript != 9_007_199_254_740_993 || got.UnsignedValue != 18_446_744_073_709_551_615 {
		t.Fatalf("cross-language integer precision changed: signed=%d unsigned=%d", got.SignedAboveJavascript, got.UnsignedValue)
	}
	if !bytes.Equal(got.Native.Utf8Json, nativeBytes) || got.Native.Sha256 == nil || *got.Native.Sha256 != "python-preserved-native-bytes" {
		t.Fatalf("native extension bytes/marker changed: %+v", got.Native)
	}
	if !bytes.Equal(got.Terminal.Data, terminalBytes) || got.Terminal.ChunkSeq != 9_007_199_254_740_995 {
		t.Fatalf("binary terminal chunk changed: %+v", got.Terminal)
	}
	if !bytes.Equal(got.ProtoReflect().GetUnknown(), unknown) {
		t.Fatalf("unknown protobuf fields were not preserved on %s: got=%x want=%x", runtime.GOOS, got.ProtoReflect().GetUnknown(), unknown)
	}
}
