package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
)

// A preference is usually the last thing someone changes before quitting, and
// Go does not wait for goroutines when main returns. Writing in the background
// lost the change better than nine times out of ten; the write must therefore
// be complete by the time the writer returns.
func TestPreferenceWriteCompletesBeforeReturning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write := preferenceWriter(path)

	if err := write(func(c *config.Config) { c.Sort = "age" }); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read immediately, with nothing to wait on: exactly what the next
	// launch does after the process has gone.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("nothing was written by the time the writer returned: %v", err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if saved.Sort != "age" {
		t.Errorf("got %q, want the change on disk", saved.Sort)
	}
}

// A write that cannot happen must say so rather than appearing to succeed.
func TestPreferenceWriteReportsFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make directory read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	write := preferenceWriter(filepath.Join(dir, "sub", "config.json"))

	if err := write(func(c *config.Config) { c.Sort = "cpu" }); err == nil {
		t.Error("expected an error when the file cannot be written")
	}
}

// Nowhere to write means nothing to call, as in the headless dump mode.
func TestPreferenceWriterAbsentWithoutAPath(t *testing.T) {
	if preferenceWriter("") != nil {
		t.Error("want no writer when there is no configuration path")
	}
}

func TestShortBuildVersionCondensesPseudoVersions(t *testing.T) {
	cases := map[string]string{
		"v1.0.0":                                   "v1.0.0",
		"v0.0.0-20260727202728-830ab25510ff":       "830ab25",
		"v0.0.0-20260727202728-830ab25510ff+dirty": "830ab25+dirty",
		"devel-830ab25":                            "830ab25",
	}

	for full, want := range cases {
		version = full
		if got := shortBuildVersion(); got != want {
			t.Errorf("shortBuildVersion() for %q = %q, want %q", full, got, want)
		}
	}
	version = ""
}
