package kitchen

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestClockStartsNonZeroAndAdvances(t *testing.T) {
	k := New(t)
	if k.Clock().Now().IsZero() {
		t.Fatal("clock started at the zero time, which Telegram reads as no date at all")
	}

	before := k.Clock().Now()
	k.Clock().Advance(time.Hour)
	if got := k.Clock().Now().Sub(before); got != time.Hour {
		t.Errorf("advanced by %v, want an hour", got)
	}
}

// Wall-clock reads would make the toolbox's own behavior non-reproducible.
func TestNoWallClockReads(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(source), "time.Now(") {
			t.Errorf("%s reads the wall clock; take the time from the kitchen's clock", name)
		}
	}
}
