package compose

import (
	"os"
	"path/filepath"
	"testing"
)

// A project built with several -f flags is defined by all of them, and the one
// the user needs to edit is as likely to be the override as the base.
func TestFilesListsEveryConfigFileInOrder(t *testing.T) {
	project := Project{
		ConfigFiles: []string{"/srv/shop/base.yml", "/srv/shop/prod.yml"},
	}

	files := Files(project)

	if len(files) != 2 {
		t.Fatalf("got %d files, want both config files: %v", len(files), files)
	}
	if files[0].Path != "/srv/shop/base.yml" || files[1].Path != "/srv/shop/prod.yml" {
		t.Errorf("got %v, want the config files in the order Compose was given them", files)
	}
}

// The .env carries the values the compose file interpolates, so editing a
// stack's configuration usually means editing it rather than the YAML.
func TestFilesIncludesTheEnvFileWhenThereIsOne(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker-compose.yml")
	write(t, compose, "services: {}\n")
	write(t, filepath.Join(dir, ".env"), "PORT=8080\n")

	files := Files(Project{WorkingDir: dir, ConfigFiles: []string{compose}})

	if len(files) != 2 {
		t.Fatalf("got %d files, want the compose file and the .env: %v", len(files), files)
	}
	if files[1].Name != ".env" {
		t.Errorf("got %q, want the .env listed after the compose file", files[1].Name)
	}
}

// Most projects have no .env, and offering to edit a file that does not exist
// would open an empty buffer that Compose never reads.
func TestFilesOmitsTheEnvFileWhenThereIsNone(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker-compose.yml")
	write(t, compose, "services: {}\n")

	files := Files(Project{WorkingDir: dir, ConfigFiles: []string{compose}})

	if len(files) != 1 {
		t.Errorf("got %v, want only the compose file", files)
	}
}

// Containers created by an older Compose can lack the path labels entirely.
// There is nothing to edit, and saying so is the caller's job, not a panic's.
func TestFilesReturnsNothingForAProjectWithNoKnownPaths(t *testing.T) {
	if files := Files(Project{Name: "mystery"}); len(files) != 0 {
		t.Errorf("got %v, want nothing for a project with no known files", files)
	}
}

// A directory named .env is not a file to edit.
func TestFilesIgnoresADirectoryNamedEnv(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker-compose.yml")
	write(t, compose, "services: {}\n")
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o755); err != nil {
		t.Fatal(err)
	}

	if files := Files(Project{WorkingDir: dir, ConfigFiles: []string{compose}}); len(files) != 1 {
		t.Errorf("got %v, want only the compose file", files)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
