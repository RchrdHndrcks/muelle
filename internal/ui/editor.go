package ui

import (
	"os"
	"strings"
)

// defaultEditor is what a server with nothing configured gets. vi is required
// by POSIX and present on every machine muelle is meant to run on.
const defaultEditor = "vi"

// EditorArgv builds the command that opens path for editing.
//
// The configured editor wins, then VISUAL, then EDITOR, then vi. VISUAL comes
// first among the variables because it names the full-screen editor, which is
// what the terminal is being handed over for; EDITOR may be a line editor.
//
// The value is split on whitespace so an editor configured with flags works.
// That is not a nicety: a graphical editor is usable here only as "code
// --wait", since without it the process returns before the file is touched
// and muelle sees an unchanged file.
func EditorArgv(configured, path string) []string {
	editor := firstNonEmpty(configured, os.Getenv("VISUAL"), os.Getenv("EDITOR"), defaultEditor)
	return append(strings.Fields(editor), path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
