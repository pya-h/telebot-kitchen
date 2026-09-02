package kitchen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenMatches(t *testing.T) {
	k := New(t)
	path := filepath.Join(t.TempDir(), "transcript.md")
	if err := os.WriteFile(path, []byte("**Ada:** hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	k.ExpectGolden(path, "**Ada:** hi\n")
}

func TestGoldenReportsTheFirstDifferingLine(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	path := filepath.Join(t.TempDir(), "transcript.md")
	if err := os.WriteFile(path, []byte("**Ada:** hi\n**Bot:** menu\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	k.ExpectGolden(path, "**Ada:** hi\n**Bot:** welcome\n")

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "at line 2") {
		t.Fatalf("errors = %v, want the line that differs", errs)
	}
	if !strings.Contains(errs[0], `want "**Bot:** menu"`) || !strings.Contains(errs[0], `got "**Bot:** welcome"`) {
		t.Errorf("error = %q, want both sides of the difference", errs[0])
	}
}

func TestGoldenSaysHowToCreateAMissingFile(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb).ExpectGolden(filepath.Join(t.TempDir(), "absent.md"), "anything")

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "-kitchen.update") {
		t.Errorf("errors = %v, want the switch that writes it named", errs)
	}
}

func TestGoldenIsRewrittenOnDemand(t *testing.T) {
	defer func(was bool) { *updateGolden = was }(*updateGolden)
	*updateGolden = true

	k := New(t)
	path := filepath.Join(t.TempDir(), "nested", "transcript.md")
	k.ExpectGolden(path, "**Ada:** hi\n")

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(written) != "**Ada:** hi\n" {
		t.Errorf("written = %q, want the text it was given", written)
	}
}
