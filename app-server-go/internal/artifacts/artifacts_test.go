package artifacts

import (
	"reflect"
	"testing"
)

func TestCollectPathsFromProseAndPreserveEncodedNames(t *testing.T) {
	text := "[文档](</home/fixture/project/docs/%E6%96%B9%E6%A1%88.md:12>)\n" +
		"代码 `src/main.go:45:2`，图片 ![图](output/result.png)\n" +
		"文件 C:\\work\\report.md 和 /home/fixture/project/output/chart.svg。\n" +
		"[网站](https://example.org/docs/index.html) https://example.org/not-file.js\n" +
		"[邮箱](mailto:user@example.test) [脚本](javascript:payload.pdf) `go test ./...`\n" +
		"```go\nx := \"/not/an/output.go\"\n```\n" +
		"[含空格](<docs/design plan.md>)"
	got := Candidates(text)
	want := []string{"/home/fixture/project/docs/%E6%96%B9%E6%A1%88.md:12", "src/main.go:45:2", "output/result.png", `C:\work\report.md`, "/home/fixture/project/output/chart.svg", "docs/design plan.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%q want=%q", got, want)
	}
	for raw, want := range map[string]string{`C:\Work\Report.md:12:2`: "c:/Work/Report.md", "file:///home/a/file.md#L5": "/home/a/file.md", "./docs/../docs/a.md": "docs/a.md"} {
		if got := Normalize(raw); got != want {
			t.Fatalf("normalize %q=%q want=%q", raw, got, want)
		}
	}
}

func TestReferenceDoesNotPublishAndResolvesEncodedAliases(t *testing.T) {
	first := File{GroupID: "g", AgentID: "linux-dev", SessionID: "dev", ConnectionID: "ssh-dev", Workspace: "/home/fixture/project", MemberID: "dev", MemberName: "开发"}
	file := Reference(first, "docs/方案.md")
	if file.Path != "/home/fixture/project/docs/方案.md" || file.ContentKey != "" || len(file.Aliases) != 1 {
		t.Fatalf("reference=%+v", file)
	}
	directory := Directory{Items: []File{file}}
	if Find(directory, "docs/%E6%96%B9%E6%A1%88.md#L3") == nil {
		t.Fatal("encoded alias not found")
	}
	if Reference(first, file.Path).ID != file.ID {
		t.Fatal("relative and absolute paths changed identity")
	}
}

func TestExpressionsAndCommandsAreNotFileCandidates(t *testing.T) {
	for _, text := range []string{"`$.data.records[*]`", "`$.contractId`", "$.data.records[*]", "$.contractId", "`--output=result.png`", "`go test ./...`", "`*.md`"} {
		if got := Candidates(text); len(got) != 0 {
			t.Fatalf("expression became file: %q -> %v", text, got)
		}
	}
}
