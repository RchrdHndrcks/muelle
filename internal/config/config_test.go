package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	config, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing file should not be an error: %v", err)
	}
	if config.LogTail != Default().LogTail {
		t.Errorf("got %+v, want defaults", config)
	}
}

// A partial file must keep defaults for everything it does not mention, so
// setting one option does not silently zero the rest.
func TestLoadPartialFileKeepsDefaultsForAbsentFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"log_tail": 42}`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.LogTail != 42 {
		t.Errorf("got log_tail %d, want 42", config.LogTail)
	}
	if config.RefreshSeconds != Default().RefreshSeconds {
		t.Errorf("got refresh %d, want the default retained", config.RefreshSeconds)
	}
	if !config.Stats {
		t.Error("stats should keep its default of true")
	}
}

// Ignoring a typo would leave the user wondering why their setting did
// nothing.
func TestLoadMalformedFileReportsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"log_tail": }`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	if config.LogTail != Default().LogTail {
		t.Error("a failed load should still yield usable defaults")
	}
}

func TestLoadIgnoresUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"log_tail": 10, "from_a_newer_version": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err != nil {
		t.Errorf("unknown fields should be ignored, got %v", err)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Config{
		DockerHost:     "tcp://10.0.0.5:2375",
		ComposeDirs:    []string{"~/stacks"},
		RefreshSeconds: 5,
		LogTail:        100,
		Stats:          false,
		StopTimeout:    20,
		Colour:         true,
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.DockerHost != want.DockerHost || got.RefreshSeconds != want.RefreshSeconds || got.Stats {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A hand-edited file must not be able to make the app hammer the daemon or
// look frozen.
func TestRefreshIntervalClampsOutOfRangeValues(t *testing.T) {
	cases := []struct {
		seconds int
		want    time.Duration
	}{
		{0, time.Second},
		{-5, time.Second},
		{3, 3 * time.Second},
		{600, 60 * time.Second},
	}

	for _, tc := range cases {
		if got := (Config{RefreshSeconds: tc.seconds}).RefreshInterval(); got != tc.want {
			t.Errorf("RefreshSeconds=%d gave %v, want %v", tc.seconds, got, tc.want)
		}
	}
}

func TestPathHonoursEnvironmentOverride(t *testing.T) {
	t.Setenv("MUELLE_CONFIG", "/custom/muelle.json")

	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != "/custom/muelle.json" {
		t.Errorf("got %q, want the override", got)
	}
}

func TestLoadOrCreateWritesDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "muelle", "config.json")
	t.Setenv("MUELLE_CONFIG", path)

	config, gotPath, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if gotPath != path {
		t.Errorf("got path %q, want %q", gotPath, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected a default config to be written: %v", err)
	}
	if config.LogTail != Default().LogTail {
		t.Errorf("got %+v, want defaults", config)
	}
}

// A read-only home directory should not stop the app from starting.
func TestLoadOrCreateToleratesUnwritableLocation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make directory read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	t.Setenv("MUELLE_CONFIG", filepath.Join(dir, "sub", "config.json"))

	config, _, err := LoadOrCreate()
	if err != nil {
		t.Errorf("got %v, want defaults rather than a failure", err)
	}
	if config.RefreshSeconds != Default().RefreshSeconds {
		t.Error("expected usable defaults")
	}
}

// A setting changed from inside the application must not discard an edit made
// to the file meanwhile; the user's text is worth more than the preference.
func TestUpdateLeavesOtherSettingsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"log_tail": 42, "docker_host": "tcp://elsewhere:2375"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, func(c *Config) { c.Sort = "cpu" }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Sort != "cpu" {
		t.Errorf("got sort %q, want the change applied", got.Sort)
	}
	if got.LogTail != 42 || got.DockerHost != "tcp://elsewhere:2375" {
		t.Errorf("got %+v, want the other settings preserved", got)
	}
}

// Flattening a file that cannot be parsed would destroy whatever the user was
// in the middle of writing.
func TestUpdateRefusesToOverwriteAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"log_tail": }`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, func(c *Config) { c.Sort = "cpu" }); err == nil {
		t.Error("expected an error rather than a silent overwrite")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("got %q, want the file left exactly as it was", after)
	}
}

func TestUpdateCreatesTheFileWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	if err := Update(path, func(c *Config) { c.Sort = "memory" }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Sort != "memory" {
		t.Errorf("got %q, want the setting recorded", got.Sort)
	}
	if got.RefreshSeconds != Default().RefreshSeconds {
		t.Errorf("got %+v, want defaults for everything else", got)
	}
}

func TestDefaultSortIsTheProjectGrouping(t *testing.T) {
	if got := Default().Sort; got != "project" {
		t.Errorf("got %q, want the grouping that makes the list read as stacks", got)
	}
}
