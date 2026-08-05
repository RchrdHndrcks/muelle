package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// Pressing u must not run up blind: the hashes are fetched off the loop, and
// the overlay that arrives says which services the up will touch.
func TestUpShowsWhatItWouldRecreateBeforeRunning(t *testing.T) {
	app := hashApp(t, "printf 'api new-hash\\ndb db-hash\\n'\nexit 0\n")

	app.handleComposeKey(context.Background(), runeKey('u'))
	applyNextEvent(t, app)

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("u must raise a confirmation describing the change")
	}
	prompt := app.overlay.Prompt
	if !strings.Contains(prompt, "Will recreate") || !strings.Contains(prompt, "  api") {
		t.Errorf("got %q, want the divergent service listed for recreation", prompt)
	}
	if !strings.Contains(prompt, "Left as they are") || !strings.Contains(prompt, "  db") {
		t.Errorf("got %q, want the matching service listed as untouched", prompt)
	}
	if upRan(app) {
		t.Error("up must not run before the user confirms")
	}
}

// Enter is the confirmation and Esc the way out; only the first runs up.
func TestUpPreviewEnterConfirmsAndEscCancels(t *testing.T) {
	confirm := func(app *App) { app.overlay.HandleKey(typeKey(tui.KeyEnter)) }
	cancel := func(app *App) { app.overlay.HandleKey(typeKey(tui.KeyEscape)) }

	cases := map[string]struct {
		answer  func(*App)
		wantRun bool
	}{
		"enter runs up":    {confirm, true},
		"esc runs nothing": {cancel, false},
	}

	for name, c := range cases {
		app := hashApp(t, "printf 'api new-hash\\n'\nexit 0\n")
		app.handleComposeKey(context.Background(), runeKey('u'))
		applyNextEvent(t, app)
		if app.overlay == nil {
			t.Fatalf("%s: no preview overlay appeared", name)
		}

		c.answer(app)

		if got := upRan(app); got != c.wantRun {
			t.Errorf("%s: up ran = %v, want %v", name, got, c.wantRun)
		}
	}
}

// The preview must never become the obstacle: a Compose that cannot answer
// --hash — too old, wrong flags, missing files — means up runs directly,
// exactly as the key behaved before the preview existed.
func TestUpRunsDirectlyWhenTheHashesCannotBeHad(t *testing.T) {
	app := hashApp(t, "echo 'unknown flag: --hash' >&2\nexit 1\n")

	app.handleComposeKey(context.Background(), runeKey('u'))
	applyNextEvent(t, app)

	if app.overlay != nil {
		t.Fatal("a failed preview must not raise an overlay")
	}
	if !upRan(app) {
		t.Error("up must have run despite the preview failing")
	}
}

// The same event applied directly, because the fallback decision is the
// event's own rather than the subprocess's.
func TestUpPreviewedFallsBackOnError(t *testing.T) {
	app := hashApp(t, "exit 0\n")

	upPreviewed{project: app.projects[0], err: errors.New("boom")}.apply(app)

	if app.overlay != nil {
		t.Fatal("an errored preview must not raise an overlay")
	}
	if !upRan(app) {
		t.Error("up must have run as the fallback")
	}
}

// Where up cannot run at all, the preview must not add a wait before the
// report the user was always going to get.
func TestUpReportsAMissingComposeWithoutPreviewing(t *testing.T) {
	app := hashApp(t, "exit 0\n")
	app.composeBinary = nil
	app.view = ViewCompose

	app.handleComposeKey(context.Background(), runeKey('u'))

	if !app.status.isError || !strings.Contains(app.status.text, "docker-compose") {
		t.Errorf("got %q, want the missing-compose report, immediately", app.status.text)
	}
}

// hashApp builds an app whose shop project has one divergent and one matching
// container, and whose compose binary is a script answering "config --hash"
// with the given body. Every other invocation records itself, so a test can
// tell whether up actually ran.
func hashApp(t *testing.T, hashBody string) *App {
	t.Helper()
	app := newTestApp(t)
	app.runner = &Runner{}
	app.view = ViewCompose

	dir := t.TempDir()
	marker := filepath.Join(dir, "up-ran")
	script := filepath.Join(dir, "compose")
	body := "#!/bin/sh\ncase \"$*\" in\n*--hash*)\n" + hashBody +
		";;\n*)\necho ran > '" + marker + "'\n;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	app.composeBinary = compose.Binary{script}

	app.projects = []compose.Project{{
		Name:        "shop",
		WorkingDir:  dir,
		ConfigFiles: []string{filepath.Join(dir, "docker-compose.yml")},
		Containers: []docker.Container{
			{ID: "a1", Names: []string{"/shop-api"}, State: "running", Labels: map[string]string{
				docker.LabelProject:    "shop",
				docker.LabelService:    "api",
				docker.LabelConfigHash: "old-hash",
			}},
			{ID: "b2", Names: []string{"/shop-db"}, State: "running", Labels: map[string]string{
				docker.LabelProject:    "shop",
				docker.LabelService:    "db",
				docker.LabelConfigHash: "db-hash",
			}},
		},
	}}
	return app
}

// upRan reports whether the fake compose binary was invoked with anything
// other than the hash request — which, for these apps, is only ever up.
func upRan(app *App) bool {
	_, err := os.Stat(filepath.Join(app.projects[0].WorkingDir, "up-ran"))
	return err == nil
}

// applyNextEvent waits for the asynchronous hash fetch to post its event and
// applies it, standing in for the loop the tests do not run.
func applyNextEvent(t *testing.T, app *App) {
	t.Helper()
	select {
	case ev := <-app.events:
		ev.apply(app)
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived from the preview")
	}
}
