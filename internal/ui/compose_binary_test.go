package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/compose"
)

// The menu shows the command each entry runs, and that text is also what the
// user would copy into a terminal. Naming a binary they do not have makes it
// wrong in both roles.
func TestComposeMenuNamesTheDetectedBinary(t *testing.T) {
	app := newTestApp(t)
	app.composeBinary = compose.Binary{"docker-compose"}

	app.openComposeMenu(compose.Project{Name: "shop", WorkingDir: "/srv/shop"})

	var details []string
	for _, item := range app.overlay.Items {
		details = append(details, item.Detail)
	}
	joined := strings.Join(details, "\n")

	if !strings.Contains(joined, "docker-compose ") {
		t.Errorf("got %q, want the standalone binary named", joined)
	}
	if strings.Contains(joined, "docker compose ") {
		t.Errorf("got %q, want no reference to the plugin the machine lacks", joined)
	}
}

// A machine with the docker CLI but no Compose used to fail only once an
// action was chosen, with the CLI's own "unknown command" — which reads as a
// muelle bug rather than a missing install.
func TestComposeActionRefusedWhenNoBinaryDetected(t *testing.T) {
	app := newTestApp(t)
	app.runner = &Runner{}
	app.dockerCLI = true
	app.composeBinary = nil

	app.runCompose(compose.Project{
		Name:        "shop",
		ConfigFiles: []string{"/srv/shop/compose.yaml"},
	}, compose.ActionUp)

	if !app.status.isError {
		t.Fatalf("got status %q, want an error", app.status.text)
	}
	if !strings.Contains(app.status.text, "docker-compose") {
		t.Errorf("got %q, want it to name both forms of Compose", app.status.text)
	}
}
