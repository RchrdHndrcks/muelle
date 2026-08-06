package ui

import (
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/compose"
)

// previewUp works out what "up -d" would do to a project before running it,
// so what the user confirms is the change rather than the command.
//
// Best-effort by design. The hashes come from a subprocess that can fail for
// reasons saying nothing about whether up itself would work — a Compose too
// old for --hash, files moved since the containers were created — and every
// one of those paths runs up directly, exactly as the key did before the
// preview existed. A preview that could block the action would be a new
// obstacle, and the point of it is to be a courtesy.
func (a *App) previewUp(project compose.Project) {
	// The same preconditions runCompose checks. Where one is missing, going
	// straight there produces the right report without a pointless wait.
	if a.runner == nil || !a.composeBinary.Available() ||
		(len(project.ConfigFiles) == 0 && project.WorkingDir == "") {
		a.runCompose(project, compose.ActionUp)
		return
	}

	a.setStatus("checking what up would change in %s", project.Name)
	// Copied out of the model before the goroutine starts: the loop owns the
	// App, and the subprocess must not read state the loop may be replacing.
	binary := a.composeBinary
	go func() {
		hashes, err := binary.ServiceHashes(project)
		a.post(upPreviewed{project: project, hashes: hashes, err: err})
	}()
}

// upPreviewed carries the hash comparison for a project, or the reason it
// could not be made.
type upPreviewed struct {
	project compose.Project
	hashes  map[string]string
	err     error
}

func (e upPreviewed) apply(a *App) {
	if e.err != nil {
		// The fallback, not a failure: up decides for itself what to
		// recreate, and it can do that without muelle having predicted it.
		a.runCompose(e.project, compose.ActionUp)
		return
	}
	preview := compose.PreviewUp(e.project, e.hashes)
	// Not danger-styled: up is the ordinary action here, so Enter confirms
	// and Esc cancels rather than demanding an explicit "y".
	a.overlay = NewConfirm("Compose up: "+e.project.Name,
		upPreviewPrompt(preview), false,
		func(any) { a.runCompose(e.project, compose.ActionUp) })
}

// upPreviewPrompt renders the comparison as the confirmation's body.
func upPreviewPrompt(preview compose.UpPreview) string {
	var lines []string
	if len(preview.Recreate) == 0 {
		lines = append(lines, "Every service matches its files; nothing will be recreated.")
	} else {
		lines = append(lines, "Will recreate (files changed, or no container):")
		for _, service := range preview.Recreate {
			lines = append(lines, "  "+service)
		}
	}
	if len(preview.Unchanged) > 0 {
		lines = append(lines, "", "Left as they are:")
		for _, service := range preview.Unchanged {
			lines = append(lines, "  "+service)
		}
	}
	return strings.Join(append(lines, "", "Run up -d?"), "\n")
}
