package compose

import (
	"os"
	"path/filepath"
)

// File is a file that defines a project and can be edited.
type File struct {
	// Path is absolute, as Compose reports it.
	Path string
	// Name is what the menu shows: the base name, which is what the user
	// thinks of the file as.
	Name string
}

// envFileName is the environment file Compose reads from the project
// directory. Files declared with env_file: are deliberately not offered:
// Compose resolves them into the rendered configuration and names them in no
// label, so finding them would mean parsing YAML — which this package exists
// to avoid.
const envFileName = ".env"

// Files lists the files that define a project, in the order they matter: the
// config files Compose was invoked with, then the project's .env if it has
// one.
//
// A project whose paths are unknown — containers created by a Compose old
// enough not to have stamped the labels — yields nothing. Reporting that is
// the caller's job.
func Files(p Project) []File {
	files := make([]File, 0, len(p.ConfigFiles)+1)
	for _, path := range p.ConfigFiles {
		files = append(files, File{Path: path, Name: filepath.Base(path)})
	}
	if p.WorkingDir == "" {
		return files
	}
	env := filepath.Join(p.WorkingDir, envFileName)
	if info, err := os.Stat(env); err == nil && !info.IsDir() {
		files = append(files, File{Path: env, Name: envFileName})
	}
	return files
}
