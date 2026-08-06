package autodeploy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-state.json")
	written := State{
		LastCheck: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
		Projects: map[string]Outcome{
			"shop": {
				Time:    time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
				Project: "shop",
				Changed: []string{"api"},
				Action:  ActionDeploy,
			},
		},
	}

	if err := SaveState(path, written); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	read := LoadState(path)

	if !read.LastCheck.Equal(written.LastCheck) {
		t.Errorf("got last check %v, want %v", read.LastCheck, written.LastCheck)
	}
	outcome, ok := read.Projects["shop"]
	if !ok {
		t.Fatal("the shop outcome did not survive the round trip")
	}
	if outcome.Action != ActionDeploy || len(outcome.Changed) != 1 || outcome.Changed[0] != "api" {
		t.Errorf("got %+v, want the outcome back intact", outcome)
	}
}

// The state file is a cache, not a source of truth; its absence is the normal
// first-run condition, never an error.
func TestLoadStateMissingFileYieldsEmptyState(t *testing.T) {
	state := LoadState(filepath.Join(t.TempDir(), "absent.json"))

	if state.Projects == nil {
		t.Fatal("Projects must be usable without a nil check")
	}
	if len(state.Projects) != 0 || !state.LastCheck.IsZero() {
		t.Errorf("got %+v, want an empty state", state)
	}
}

// A half-written or hand-mangled file must yield a clean state: the next
// cycle rewrites it, and the TUI must not refuse to draw over it.
func TestLoadStateCorruptFileYieldsEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-state.json")
	if err := os.WriteFile(path, []byte(`{"projects": {`), 0o644); err != nil {
		t.Fatal(err)
	}

	state := LoadState(path)

	if state.Projects == nil || len(state.Projects) != 0 {
		t.Errorf("got %+v, want an empty usable state", state)
	}
}

func TestStatePathForSitsNextToTheConfig(t *testing.T) {
	got := StatePathFor("/home/x/.config/muelle/config.json")
	want := "/home/x/.config/muelle/deploy-state.json"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if StatePathFor("") != "" {
		t.Error("no config path means nowhere to put state, not the working directory")
	}
}

// RunOnce is what the daemon loop calls each tick; it must leave the state
// file describing the cycle.
func TestRunOnceWritesTheStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-state.json")
	daemon := &fakeDocker{}
	runner := &fakeRunner{}
	deployer := newDeployer(daemon, runner)
	deployer.StatePath = path
	now := time.Date(2026, 8, 5, 15, 30, 0, 0, time.UTC)
	deployer.Now = func() time.Time { return now }

	deployer.RunOnce(t.Context())

	state := LoadState(path)
	if !state.LastCheck.Equal(now) {
		t.Errorf("got last check %v, want %v", state.LastCheck, now)
	}
	outcome, ok := state.Projects["shop"]
	if !ok {
		t.Fatal("the enrolled project's outcome was not recorded")
	}
	if outcome.Action != ActionSkip {
		t.Errorf("got action %q, want the skip recorded (no files known)", outcome.Action)
	}
}
