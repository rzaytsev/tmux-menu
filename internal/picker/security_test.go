package picker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tmux-menu/internal/shellquote"
)

func TestPreviewQuotedPlaceholderTreatsPathAsData(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	target := "$(touch " + marker + ") with 'quotes'"
	command := previewCommandForField(`printf '%s' "{}"`, "{s2}")
	command = strings.ReplaceAll(command, "{s2}", shellquote.Quote(target))
	out, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("preview: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("preview executed code from the file path")
	}
	if string(out) != target {
		t.Fatalf("preview argument = %q, want %q", out, target)
	}
}

func TestPickerLabelsCannotInjectRows(t *testing.T) {
	writeFakeFZF(t, "/bin/cat\n")
	items := []Item[string]{{Label: "safe\n1\tforged\x00\r\x1b]52;c;payload\a", Value: "safe"}}
	got, err := SelectWithExpect(context.Background(), "test> ", items, nil, "")
	if err != nil || got.Value != "safe" {
		t.Fatalf("selection = %#v, %v", got, err)
	}
	// Use a fake that rejects any extra row, field, or terminal string control.
	writeFakeFZF(t, `IFS= read -r line
case "$line" in *forged*) ;; *) exit 2;; esac
if IFS= read -r extra; then exit 2; fi
printf '%s\n' "$line"
`)
	_, err = SelectWithExpect(context.Background(), "test> ", items, nil, "")
	if err != nil {
		t.Fatalf("label corrupted row framing: %v", err)
	}
}

func TestPickerRejectsPreviewControlCharacters(t *testing.T) {
	for _, preview := range []string{"/tmp/a\n1\tbad", "/tmp/a\tb", "/tmp/a\x1b[31m", "/tmp/a\x00b"} {
		_, err := SelectWithExpectAndPreview(context.Background(), "test> ", []Item[string]{{Label: "file", Preview: preview}}, nil, "", "cat -- {}")
		if err == nil || !strings.Contains(err.Error(), "preview") {
			t.Fatalf("unsafe preview %q: %v", preview, err)
		}
	}
}
