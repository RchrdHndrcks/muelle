package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// yankApp is a loaded app whose screen writes to a retained buffer with the
// clipboard enabled, so a test can inspect exactly what would reach the
// terminal.
func yankApp(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	cfg := config.Default()
	cfg.ComposeDirs = nil
	cfg.Stats = false
	screen := tui.NewScreen(out, 120, 30, false)
	screen.EnableClipboard()
	app := New(cfg, fakeDaemon(t), screen, nil)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	return app, out
}

// selectContainerRow puts the cursor on the named container, wherever the
// current grouping happens to have placed it, and returns it.
func selectContainerRow(t *testing.T, app *App, name string) docker.Container {
	t.Helper()
	for i, row := range app.rows() {
		if row.Header == nil && row.Container.Name() == name {
			app.selection[ViewContainers] = i
			return row.Container
		}
	}
	t.Fatalf("no row for container %q", name)
	return docker.Container{}
}

// emitted renders a frame and returns what the terminal would receive, which
// is where a queued OSC 52 sequence surfaces.
func emitted(t *testing.T, app *App, out *bytes.Buffer) string {
	t.Helper()
	if err := app.screen.Render(nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out.String()
}

func TestYankCopiesContainerFullID(t *testing.T) {
	app, out := yankApp(t)
	container := selectContainerRow(t, app, "shop-api")

	press(app, runeKey('y'))

	if app.status.text != "copied container ID" || app.status.isError {
		t.Errorf("got status %q, want %q", app.status.text, "copied container ID")
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence(container.ID)) {
		t.Error("the next frame should carry the full container ID as an OSC 52 sequence")
	}
}

// A group heading is a row but not a container, so there is nothing to copy
// and nothing must be claimed.
func TestYankOnHeadingCopiesNothing(t *testing.T) {
	app, out := yankApp(t)
	headingAt := -1
	for i, row := range app.rows() {
		if row.Header != nil {
			headingAt = i
			break
		}
	}
	if headingAt < 0 {
		t.Fatal("expected the grouped list to carry a heading")
	}
	app.selection[ViewContainers] = headingAt

	press(app, runeKey('y'))

	if app.status.text != "" {
		t.Errorf("got status %q, want none", app.status.text)
	}
	if strings.Contains(emitted(t, app, out), "\x1b]52") {
		t.Error("no clipboard sequence should be emitted for a heading")
	}
}

func TestYankCopiesImageTag(t *testing.T) {
	app, out := yankApp(t)
	app.view = ViewImages

	press(app, runeKey('y'))

	if app.status.text != "copied image tag" {
		t.Errorf("got status %q, want %q", app.status.text, "copied image tag")
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence("mysql:8.0")) {
		t.Error("the frame should carry the image tag as an OSC 52 sequence")
	}
}

// An untagged image has no name; its ID is the only handle there is.
func TestYankFallsBackToImageIDWhenUntagged(t *testing.T) {
	app, out := yankApp(t)
	app.view = ViewImages
	app.images = []docker.Image{{ID: "sha256:feedfacefeedfacefeedface"}}
	app.selection[ViewImages] = 0

	press(app, runeKey('y'))

	if app.status.text != "copied image ID" {
		t.Errorf("got status %q, want %q", app.status.text, "copied image ID")
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence("feedfacefeed")) {
		t.Error("the frame should carry the untagged image's ID")
	}
}

func TestYankCopiesVolumeName(t *testing.T) {
	app, out := yankApp(t)
	app.view = ViewVolumes

	press(app, runeKey('y'))

	if app.status.text != "copied volume name" {
		t.Errorf("got status %q, want %q", app.status.text, "copied volume name")
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence("shop-db-data")) {
		t.Error("the frame should carry the volume name")
	}
}

func TestYankCopiesNetworkName(t *testing.T) {
	app, out := yankApp(t)
	app.view = ViewNetworks
	app.networks = []docker.Network{{Name: "shop_default", ID: "0123456789ab"}}
	app.selection[ViewNetworks] = 0

	press(app, runeKey('y'))

	if app.status.text != "copied network name" {
		t.Errorf("got status %q, want %q", app.status.text, "copied network name")
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence("shop_default")) {
		t.Error("the frame should carry the network name")
	}
}

func TestYankCopiesProjectName(t *testing.T) {
	app, out := yankApp(t)
	app.view = ViewCompose
	app.selection[ViewCompose] = 0

	press(app, runeKey('y'))

	if app.status.text != "copied project name" {
		t.Errorf("got status %q, want %q", app.status.text, "copied project name")
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence("shop")) {
		t.Error("the frame should carry the compose project name")
	}
}

func TestYankMenuOffersEveryApplicableIdentifier(t *testing.T) {
	app, out := yankApp(t)
	selectContainerRow(t, app, "shop-api")

	press(app, runeKey('Y'))

	if app.overlay == nil || app.overlay.Kind != OverlayMenu {
		t.Fatal("Y should open the copy menu")
	}
	labels := make([]string, 0, len(app.overlay.Items))
	for _, item := range app.overlay.Items {
		labels = append(labels, item.Label)
	}
	for _, want := range []string{"full ID", "name", "image", "published port"} {
		found := false
		for _, label := range labels {
			if label == want {
				found = true
			}
		}
		if !found {
			t.Errorf("menu is missing %q; got %v", want, labels)
		}
	}

	// Choosing the port copies a pasteable host:port, with the 0.0.0.0
	// wildcard the daemon reports rewritten to an address that connects.
	for i, item := range app.overlay.Items {
		if item.Label == "published port" {
			app.overlay.Selected = i
		}
	}
	press(app, typeKey(tui.KeyEnter))

	if !strings.HasPrefix(app.status.text, "copied port") {
		t.Errorf("got status %q, want it to report the copied port", app.status.text)
	}
	if !strings.Contains(emitted(t, app, out), tui.CopySequence("localhost:8080")) {
		t.Error("the frame should carry localhost:8080")
	}
}

// A container publishing nothing has no port worth offering, and the entry is
// omitted rather than greyed out.
func TestYankMenuOmitsPortWhenNothingPublished(t *testing.T) {
	app, _ := yankApp(t)
	selectContainerRow(t, app, "shop-db")

	press(app, runeKey('Y'))

	if app.overlay == nil || app.overlay.Kind != OverlayMenu {
		t.Fatal("Y should open the copy menu")
	}
	for _, item := range app.overlay.Items {
		if item.Label == "published port" {
			t.Error("a container with no published port should not offer one")
		}
	}
}

// Without a terminal behind the screen — the dump mode — there is nowhere for
// the sequence to go, and the honest answer is an error, not a claimed copy.
func TestYankWithoutTerminalReportsError(t *testing.T) {
	app := loadedApp(t)
	selectContainerRow(t, app, "shop-api")

	press(app, runeKey('y'))

	if !app.status.isError {
		t.Errorf("got status %q, want an error about the missing terminal", app.status.text)
	}
}

func TestFirstPublishedPort(t *testing.T) {
	tests := []struct {
		name  string
		ports []docker.Port
		want  string
		ok    bool
	}{
		{"wildcard IPv4 becomes localhost",
			[]docker.Port{{IP: "0.0.0.0", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"}},
			"localhost:8080", true},
		{"wildcard IPv6 becomes localhost",
			[]docker.Port{{IP: "::", PrivatePort: 8080, PublicPort: 8081, Type: "tcp"}},
			"localhost:8081", true},
		{"an explicit bind address is kept",
			[]docker.Port{{IP: "127.0.0.1", PrivatePort: 3306, PublicPort: 3307, Type: "tcp"}},
			"127.0.0.1:3307", true},
		{"exposed-only ports are skipped",
			[]docker.Port{{PrivatePort: 6379, Type: "tcp"}, {IP: "0.0.0.0", PrivatePort: 5432, PublicPort: 5432, Type: "tcp"}},
			"localhost:5432", true},
		{"nothing published",
			[]docker.Port{{PrivatePort: 6379, Type: "tcp"}},
			"", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := firstPublishedPort(docker.Container{Ports: test.ports})
			if got != test.want || ok != test.ok {
				t.Errorf("got %q, %v; want %q, %v", got, ok, test.want, test.ok)
			}
		})
	}
}
