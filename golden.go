package kitchen

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var updateGolden = flag.Bool("kitchen.update", false, "rewrite the golden files kitchen tests compare against")

// ExpectGolden compares text with the file at path, which -kitchen.update
// rewrites. Keep goldens under testdata, which the toolchain ignores.
func (k *Kitchen) ExpectGolden(path, got string) {
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			k.tb.Errorf("kitchen: create %s: %v", filepath.Dir(path), err)
			return
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			k.tb.Errorf("kitchen: write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		k.tb.Errorf("kitchen: %v; rerun with -kitchen.update to write it", err)
		return
	}
	if string(want) != got {
		k.tb.Errorf("kitchen: %s is out of date, %s", path, firstDifference(string(want), got))
	}
}

// ExpectTranscript compares the user's whole conversation with a golden file,
// so a change in wording or layout shows up as a diff rather than as a dozen
// broken assertions.
func (u *User) ExpectTranscript(path string) {
	u.kitchen.ExpectGolden(path, u.Transcript())
}

func firstDifference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(wantLines), len(gotLines)) {
		w, g := lineAt(wantLines, i), lineAt(gotLines, i)
		if w != g {
			return fmt.Sprintf("at line %d:\n  want %q\n   got %q", i+1, w, g)
		}
	}
	return "though every line matches"
}

func lineAt(lines []string, i int) string {
	if i >= len(lines) {
		return ""
	}
	return lines[i]
}
