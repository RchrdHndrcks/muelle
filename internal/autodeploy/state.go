package autodeploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State is what the daemon knows after its last cycle, persisted so the TUI
// can show it. The file is the whole interface between the two processes:
// they share no socket and no lock, the daemon only writes and the TUI only
// reads, so a torn read at worst shows one stale refresh tick.
type State struct {
	// LastCheck is when the daemon last completed a cycle, whatever it
	// decided. A daemon that is running but finding nothing to do is
	// distinguishable from one that stopped only by this moving.
	LastCheck time.Time `json:"last_check"`
	// Projects holds the most recent outcome per project name.
	Projects map[string]Outcome `json:"projects"`
}

// StatePathFor returns where the state file lives: next to the configuration
// file, because that is the one location both the daemon and the TUI already
// agree on without any further configuration.
func StatePathFor(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "deploy-state.json")
}

// LoadState reads the state file, tolerantly: a missing, unreadable or
// corrupt file yields an empty state rather than an error. The file is a
// cache of what the daemon last did, not a source of truth — the next cycle
// rewrites it — so nothing useful could be done with the error anyway, and
// the TUI must not refuse to draw over it.
func LoadState(path string) State {
	state := State{Projects: make(map[string]Outcome)}
	if path == "" {
		return state
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		// Half-written or hand-mangled: start clean rather than trust it.
		return State{Projects: make(map[string]Outcome)}
	}
	if state.Projects == nil {
		state.Projects = make(map[string]Outcome)
	}
	return state
}

// SaveState writes the state file, creating parent directories. Written whole
// each cycle; at a few hundred bytes there is nothing to update in place.
func SaveState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
