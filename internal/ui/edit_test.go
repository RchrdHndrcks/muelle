package ui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/compose"
)

// A container's environment and image are fixed when it is created, so
// restarting one cannot pick up an edited compose file. Where recreating is
// possible, the interface has to offer it — otherwise the user presses
// restart, sees nothing change, and has no way to find out why.
func TestRestartOffersRecreateForAComposeContainer(t *testing.T) {
	app := composeApp(t)
	app.filter = "shop-api"

	app.handleContainerKey(context.Background(), runeKey('r'))

	if app.overlay == nil {
		t.Fatal("a compose container must be offered restart or recreate")
	}
	labels := itemLabels(app.overlay)
	if len(labels) != 2 || !strings.Contains(labels[0], "restart") || !strings.Contains(labels[1], "recreate") {
		t.Errorf("got %v, want restart and recreate", labels)
	}
}

// A menu that always appears is friction. It earns its place only when there
// are genuinely two things to choose between.
func TestRestartActsAtOnceWhenRecreatingIsImpossible(t *testing.T) {
	cases := map[string]func(*App){
		"container not managed by compose": func(a *App) { a.filter = "standalone" },
		"compose not installed":            func(a *App) { a.composeBinary = nil },
		"project files unknown":            func(a *App) { a.projects = nil },
		"no runner to hand the terminal":   func(a *App) { a.runner = nil },
	}

	for name, breakIt := range cases {
		app := composeApp(t)
		app.filter = "shop-api"
		breakIt(app)

		app.handleContainerKey(context.Background(), runeKey('r'))

		if app.overlay != nil {
			t.Errorf("%s: got a menu, want the restart to happen directly", name)
		}
	}
}

// A project built with several -f flags is defined by all of them, and the
// .env holds the values they interpolate.
func TestEditListsEveryFileOfTheProject(t *testing.T) {
	app := composeApp(t)
	app.view = ViewCompose

	app.handleComposeKey(context.Background(), runeKey('e'))

	if app.overlay == nil {
		t.Fatal("editing must offer the project's files")
	}
	labels := itemLabels(app.overlay)
	if len(labels) != 2 || labels[0] != "docker-compose.yml" || labels[1] != ".env" {
		t.Errorf("got %v, want the compose file and the .env", labels)
	}
}

// Containers created by a Compose old enough not to stamp the path labels
// leave muelle with nothing to open. Saying so beats an empty buffer.
func TestEditReportsAProjectWithNoKnownFiles(t *testing.T) {
	app := composeApp(t)
	app.view = ViewCompose
	app.projects = []compose.Project{{Name: "shop"}}

	app.handleComposeKey(context.Background(), runeKey('e'))

	if app.overlay != nil {
		t.Fatal("there is nothing to offer")
	}
	if !app.status.isError || !strings.Contains(app.status.text, "shop") {
		t.Errorf("got %q, want an error naming the project", app.status.text)
	}
}

// Opening a file to read it is not a reason to recreate half a stack.
func TestAnUnchangedFileOffersNothing(t *testing.T) {
	app := composeApp(t)
	file := compose.File{Path: filepath.Join(app.projects[0].WorkingDir, ".env"), Name: ".env"}
	before, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatal(err)
	}

	app.reviewEdit(app.projects[0], file, before)

	if app.overlay != nil {
		t.Fatal("an unchanged file must not offer to apply anything")
	}
	if app.status.isError || !strings.Contains(app.status.text, "unchanged") {
		t.Errorf("got %q, want it reported as unchanged", app.status.text)
	}
}

// The point of the whole feature: an edit is worth nothing until it is
// applied, and the user should not have to know which command applies it.
func TestAChangedFileOffersToApplyIt(t *testing.T) {
	app := composeApp(t)
	stubCompose(t, app, 0, "")
	file := compose.File{Path: filepath.Join(app.projects[0].WorkingDir, ".env"), Name: ".env"}

	app.reviewEdit(app.projects[0], file, []byte("PORT=1\n"))

	if app.overlay == nil {
		t.Fatal("a changed file must offer to apply the change")
	}
	labels := itemLabels(app.overlay)
	if len(labels) != 2 || !strings.Contains(labels[0], "apply") || !strings.Contains(labels[1], "force") {
		t.Errorf("got %v, want apply and force", labels)
	}
}

// Validation renders the configuration without touching a container, so a
// typo costs milliseconds instead of a stack that is already half down.
func TestAnInvalidConfigOffersToGoBackToTheEditor(t *testing.T) {
	app := composeApp(t)
	stubCompose(t, app, 1, "services.api.ports: invalid port 80800")
	file := compose.File{Path: filepath.Join(app.projects[0].WorkingDir, ".env"), Name: ".env"}

	app.reviewEdit(app.projects[0], file, []byte("PORT=1\n"))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("an invalid configuration must be reported before anything is applied")
	}
	if !strings.Contains(app.overlay.Prompt, "invalid port 80800") {
		t.Errorf("got %q, want Compose's own diagnostic", app.overlay.Prompt)
	}
}

// Editing is useful even where Compose is absent, but muelle must not pretend
// it can apply what it cannot.
func TestAChangedFileIsReportedWhenComposeIsMissing(t *testing.T) {
	app := composeApp(t)
	app.composeBinary = nil
	file := compose.File{Path: filepath.Join(app.projects[0].WorkingDir, ".env"), Name: ".env"}

	app.reviewEdit(app.projects[0], file, []byte("PORT=1\n"))

	if app.overlay != nil {
		t.Fatal("nothing can be applied without compose")
	}
	if !strings.Contains(app.status.text, "compose") {
		t.Errorf("got %q, want it to say why the change cannot be applied", app.status.text)
	}
}

func TestADeletedFileIsReported(t *testing.T) {
	app := composeApp(t)
	file := compose.File{Path: filepath.Join(app.projects[0].WorkingDir, "gone.yml"), Name: "gone.yml"}

	app.reviewEdit(app.projects[0], file, []byte("services: {}\n"))

	if app.overlay != nil {
		t.Fatal("a missing file cannot be applied")
	}
	if !app.status.isError {
		t.Errorf("got %q, want an error", app.status.text)
	}
}

// composeApp builds an app whose shop project has real files on disk and a
// Compose binary available.
func composeApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.runner = &Runner{}
	app.composeBinary = compose.Binary{"docker", "compose"}
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	dir := t.TempDir()
	composeFile := filepath.Join(dir, "docker-compose.yml")
	writeFile(t, composeFile, "services: {}\n")
	writeFile(t, filepath.Join(dir, ".env"), "PORT=8080\n")

	app.projects = []compose.Project{{
		Name:        "shop",
		WorkingDir:  dir,
		ConfigFiles: []string{composeFile},
		Containers:  app.containers[:1],
	}}
	return app
}

// stubCompose replaces the compose binary with a script that answers the way
// the test needs, so validation runs for real without a daemon.
func stubCompose(t *testing.T, app *App, exit int, output string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "compose")
	body := "#!/bin/sh\n"
	if output != "" {
		body += "echo '" + output + "' >&2\n"
	}
	body += "exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	app.composeBinary = compose.Binary{script}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itemLabels(overlay *Overlay) []string {
	labels := make([]string, 0, len(overlay.Items))
	for _, item := range overlay.Items {
		labels = append(labels, item.Label)
	}
	return labels
}
