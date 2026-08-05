package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupFilenameStampsTheMoment(t *testing.T) {
	at := time.Date(2026, 8, 5, 9, 4, 5, 0, time.UTC)
	got := BackupFilename("shop-db-data", at)
	want := "shop-db-data-20260805-090405.tgz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBackupArgvMountsTheVolumeReadOnly(t *testing.T) {
	got := BackupArgv("shop-db-data", "/backups", "shop-db-data-20260805-090405.tgz")
	want := []string{
		"docker", "run", "--rm",
		"-v", "shop-db-data:/data:ro",
		"-v", "/backups:/backup",
		"alpine", "tar", "czf", "/backup/shop-db-data-20260805-090405.tgz",
		"-C", "/data", ".",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d words %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The confirmation must show where the file will land, with "~" already
// expanded — a path the user can verify is the point of asking.
func TestBackupKeyOpensConfirmationWithDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := loadedApp(t)
	app.SetView(ViewVolumes)
	app.dockerCLI = true

	press(app, runeKey('b'))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("b should open a confirmation before anything runs")
	}
	if app.overlay.Danger {
		t.Error("a backup writes one new file and destroys nothing; Enter should confirm it")
	}
	dir := filepath.Join(home, "muelle-backups")
	if !strings.Contains(app.overlay.Prompt, dir) {
		t.Errorf("prompt %q should name the expanded backup directory %q", app.overlay.Prompt, dir)
	}
	if !strings.Contains(app.overlay.Prompt, "shop-db-data-") || !strings.Contains(app.overlay.Prompt, ".tgz") {
		t.Errorf("prompt %q should name the timestamped archive for the volume", app.overlay.Prompt)
	}
}

// Without the docker CLI, the archive command cannot run; say so instead of
// asking a question whose answer would only fail.
func TestBackupReportsMissingDockerCLI(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewVolumes)
	app.dockerCLI = false

	press(app, runeKey('b'))

	if app.overlay != nil {
		t.Error("no confirmation should open without the CLI")
	}
	if !app.status.isError {
		t.Error("the refusal should be explained in the status bar")
	}
}
