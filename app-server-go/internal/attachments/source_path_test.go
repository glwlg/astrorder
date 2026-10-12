package attachments

import "testing"

func TestSourcePathBaseUnderstandsWindowsAndPOSIXSeparators(t *testing.T) {
	for _, value := range []string{`C:\private\note.txt`, `C:/private/note.txt`, `/home/fixture/note.txt`} {
		if got := baseAnyPath(value); got != "note.txt" || !isAbsoluteAnyPath(value) {
			t.Fatalf("source identity changed: path=%q base=%q", value, got)
		}
	}
}
