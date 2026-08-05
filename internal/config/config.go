// Package config loads muelle's settings from a JSON file, falling back to
// defaults that work without any configuration at all.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds user settings. Every field has a working default, so a missing
// or partial file is not an error.
type Config struct {
	// DockerHost overrides daemon discovery. Empty means auto-detect.
	DockerHost string `json:"docker_host"`
	// ComposeDirs are scanned for Compose projects that have no
	// containers, so stopped stacks remain visible.
	ComposeDirs []string `json:"compose_dirs"`
	// BackupDir is where volume backups are written. A leading "~" expands
	// to the home directory, the same way it does in ComposeDirs, and the
	// directory is created on first use rather than at startup — most
	// sessions never take a backup.
	BackupDir string `json:"backup_dir"`
	// RefreshSeconds is how often the container list is polled.
	RefreshSeconds int `json:"refresh_seconds"`
	// LogTail is how many lines of history to load when opening logs.
	LogTail int `json:"log_tail"`
	// Stats enables the CPU and memory columns, fed by one streaming stats
	// connection per running container. The daemon serves those cheaply,
	// but they are still one open connection each, so this can be turned
	// off on a slow remote socket or a host with very many containers.
	Stats bool `json:"stats"`
	// StopTimeout is how many seconds a container is given to exit before
	// it is killed.
	StopTimeout int `json:"stop_timeout"`
	// Colour enables styled output. NO_COLOR in the environment overrides
	// this to false.
	Colour bool `json:"colour"`
	// SystemPanel shows the host metrics summary beneath the list.
	SystemPanel bool `json:"system_panel"`
	// Sort is the container list ordering, by its name in the interface:
	// project, cpu, memory, age or state. An unrecognised value falls back
	// to the default rather than refusing to start.
	Sort string `json:"sort"`
	// LogTimestamps prefixes each line in the log viewer with the time it
	// was logged, as t toggles. Stored, like the two below it, because how
	// someone reads logs is a preference like any other: pressing t on
	// every container because the last time was forgotten is the same
	// annoyance as re-sorting the list on every launch.
	LogTimestamps bool `json:"log_timestamps"`
	// LogWrap breaks long lines across rows rather than clipping them, as w
	// toggles.
	LogWrap bool `json:"log_wrap"`
	// LogFormat renders structured lines as their parts rather than as the
	// container wrote them, as F toggles.
	LogFormat bool `json:"log_format"`
	// Editor opens a project's compose file or .env. Empty — the default —
	// leaves the choice to $VISUAL and $EDITOR, which is where a user has
	// already made it.
	Editor string `json:"editor"`
	// GroupContainers gathers the container list under one heading per
	// application, as G toggles.
	GroupContainers bool `json:"group_containers"`
	// CollapsedGroups are the applications folded away, remembered for the
	// same reason the log viewer's settings are: the bucket of stray
	// containers is collapsed once and should stay that way.
	CollapsedGroups []string `json:"collapsed_groups"`
	// HealthProbe asks containers that set MUELLE_HEALTH whether they are
	// well, over HTTP, from the host. Reading that variable needs one
	// inspect per container — once each, since a container's environment
	// cannot change without it being recreated under a new ID.
	HealthProbe bool `json:"health_probe"`
	// AutoDeploy configures unattended deployments, applied only by the
	// headless daemon (muelle -deploy). The TUI reads and edits this but
	// never deploys from it, so exactly one process ever acts on it.
	AutoDeploy AutoDeploy `json:"auto_deploy"`
}

// AutoDeploy names the Compose projects the headless daemon keeps current and
// how often it checks. It lives in the shared configuration file because the
// TUI is where projects are enrolled (the W key) while the daemon is what acts
// on the enrolment; a single file is what keeps the two views of "which
// projects" from drifting apart.
type AutoDeploy struct {
	// Projects are the Compose project names to deploy automatically.
	// Empty means the daemon has nothing to do.
	Projects []string `json:"projects"`
	// IntervalMinutes is how often the daemon checks for newer images,
	// clamped to 1–1440. Pulls are cheap when nothing changed — the
	// registry answers "already up to date" — but they are still network
	// round trips, so the floor is a minute, not a second.
	IntervalMinutes int `json:"interval_minutes"`
	// Prune removes dangling images after a deploy. Off by default: it is
	// the one part of the cycle that deletes something, and deleting the
	// previous image also deletes the fastest way to roll back to it.
	Prune bool `json:"prune"`
}

// Interval returns the check period as a duration, clamped to a sane range so
// a hand-edited file can neither hammer a registry nor look permanently idle.
func (a AutoDeploy) Interval() time.Duration {
	minutes := min(max(a.IntervalMinutes, 1), 1440)
	return time.Duration(minutes) * time.Minute
}

// Enabled reports whether a project is enrolled for automatic deployment.
func (a AutoDeploy) Enabled(name string) bool {
	for _, project := range a.Projects {
		if project == name {
			return true
		}
	}
	return false
}

// SetEnabled enrols or withdraws a project. It sets rather than toggles so the
// caller's intent survives being replayed against a config file someone edited
// meanwhile: "enable shop" means enabled, whatever the file said.
func (a *AutoDeploy) SetEnabled(name string, on bool) {
	if on {
		if !a.Enabled(name) {
			a.Projects = append(a.Projects, name)
		}
		return
	}
	kept := a.Projects[:0]
	for _, project := range a.Projects {
		if project != name {
			kept = append(kept, project)
		}
	}
	a.Projects = kept
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		ComposeDirs:     []string{"~/deployments"},
		BackupDir:       "~/muelle-backups",
		RefreshSeconds:  3,
		LogTail:         500,
		Stats:           true,
		StopTimeout:     10,
		Colour:          true,
		SystemPanel:     true,
		Sort:            "project",
		LogWrap:         true,
		LogFormat:       true,
		HealthProbe:     true,
		GroupContainers: true,
		AutoDeploy:      AutoDeploy{IntervalMinutes: 15},
	}
}

// RefreshInterval returns the polling period as a duration, clamped to a sane
// range so a hand-edited file cannot make the app hammer the daemon or appear
// frozen.
func (c Config) RefreshInterval() time.Duration {
	seconds := min(max(c.RefreshSeconds, 1), 60)
	return time.Duration(seconds) * time.Second
}

// Path returns the configuration file location: $MUELLE_CONFIG when set,
// otherwise a muelle directory under the user's config directory
// (~/.config on Linux, ~/Library/Application Support on macOS).
func Path() (string, error) {
	if path := os.Getenv("MUELLE_CONFIG"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locate config directory: %w", err)
	}
	return filepath.Join(dir, "muelle", "config.json"), nil
}

// Load reads the configuration from path, returning defaults when the file
// does not exist.
//
// Unknown fields are ignored and absent fields keep their default, so a config
// written by an older or newer version still loads. A malformed file is an
// error rather than a silent fallback: quietly ignoring a typo would leave the
// user wondering why their setting had no effect.
func Load(path string) (Config, error) {
	config := Default()

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return Default(), fmt.Errorf("config: parse %s: %w", path, err)
	}
	return config, nil
}

// Save writes the configuration to path, creating parent directories.
func Save(path string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create directory: %w", err)
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// Update applies a change to the stored configuration.
//
// It re-reads the file rather than writing back an in-memory copy, so a
// setting changed from inside the application does not silently discard an
// edit someone made to the file meanwhile. Only the field the caller touches
// moves.
func Update(path string, change func(*Config)) error {
	config, err := Load(path)
	if err != nil {
		// A file that cannot be parsed should not be flattened by a
		// preference change; the user's text is worth more than the
		// preference.
		return err
	}
	change(&config)
	return Save(path, config)
}

// LoadOrCreate loads the configuration, writing a default file when none
// exists so there is something to edit.
//
// A failure to write is not fatal: a read-only home directory should not stop
// the app from running with defaults.
func LoadOrCreate() (Config, string, error) {
	path, err := Path()
	if err != nil {
		return Default(), "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		config := Default()
		if writeErr := Save(path, config); writeErr != nil {
			return config, path, nil
		}
		return config, path, nil
	}
	config, err := Load(path)
	return config, path, err
}
