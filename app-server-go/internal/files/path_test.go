package files

import (
	"path/filepath"
	"testing"
)

func TestFileParentPreservesTargetPathSyntax(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"/home/fixture/workspace/ExampleProject/app.go", "/home/fixture/workspace/ExampleProject"},
		{"/home/fixture/中文 空格/app.go", "/home/fixture/中文 空格"},
		{"/app.go", "/"},
		{"//server/share/app.go", "//server/share"},
		{filepath.Join(t.TempDir(), "app.go"), ""},
	} {
		want := test.want
		if want == "" {
			want = filepath.Dir(test.input)
		}
		if got := fileParent(test.input); got != want {
			t.Errorf("fileParent(%q)=%q want %q", test.input, got, want)
		}
	}
}
