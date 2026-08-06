package compose

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// The "*" is passed to Compose as an argument, never through a shell, so the
// argv carries it literally.
func TestCommandBuildsTheHashRequest(t *testing.T) {
	got := pluginBinary.Command(Project{Name: "engi"}, ActionConfigHash)

	want := []string{"docker", "compose", "-p", "engi", "config", "--hash", "*"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseServiceHashesReadsOneServicePerLine(t *testing.T) {
	hashes := ParseServiceHashes("api 0d4c2071\ndb 66a7dfd2\n")

	if len(hashes) != 2 || hashes["api"] != "0d4c2071" || hashes["db"] != "66a7dfd2" {
		t.Errorf("got %v, want api and db with their hashes", hashes)
	}
}

// A stray diagnostic line must not cost the caller the hashes printed around
// it: the preview this feeds is best-effort, and partial is better than none.
func TestParseServiceHashesSkipsLinesInAnyOtherShape(t *testing.T) {
	hashes := ParseServiceHashes("api 0d4c2071\n\nsome warning about the project\ndb 66a7dfd2")

	if len(hashes) != 2 || hashes["api"] != "0d4c2071" || hashes["db"] != "66a7dfd2" {
		t.Errorf("got %v, want only the well-formed lines", hashes)
	}
}

// PreviewUp must reproduce the decision up itself will make: a differing hash
// or a missing container means recreation, a matching hash means the service
// is left alone.
func TestPreviewUpClassifiesServices(t *testing.T) {
	project := Project{Containers: []docker.Container{
		hashedContainer("api", "old-hash"),
		hashedContainer("db", "db-hash"),
	}}
	hashes := map[string]string{
		"api":    "new-hash", // container exists but was built from other files
		"db":     "db-hash",  // container matches the files
		"worker": "w-hash",   // no container at all
	}

	preview := PreviewUp(project, hashes)

	if !slices.Equal(preview.Recreate, []string{"api", "worker"}) {
		t.Errorf("recreate = %v, want the divergent and the containerless service", preview.Recreate)
	}
	if !slices.Equal(preview.Unchanged, []string{"db"}) {
		t.Errorf("unchanged = %v, want the matching service", preview.Unchanged)
	}
}

// A service scaled to several replicas is recreated if any one of them
// diverges, because that is what up will do to it.
func TestPreviewUpRecreatesWhenAnyReplicaDiverges(t *testing.T) {
	project := Project{Containers: []docker.Container{
		hashedContainer("api", "current"),
		hashedContainer("api", "stale"),
	}}

	preview := PreviewUp(project, map[string]string{"api": "current"})

	if !slices.Equal(preview.Recreate, []string{"api"}) {
		t.Errorf("recreate = %v, want the partly stale service", preview.Recreate)
	}
}

// Containers stamped by a Compose too old to carry the hash label have an
// empty hash, which can never match — the honest reading, and Compose's own.
func TestPreviewUpTreatsAMissingLabelAsDivergent(t *testing.T) {
	project := Project{Containers: []docker.Container{{
		Labels: map[string]string{docker.LabelService: "api"},
	}}}

	preview := PreviewUp(project, map[string]string{"api": "h"})

	if !slices.Equal(preview.Recreate, []string{"api"}) {
		t.Errorf("recreate = %v, want the unlabelled service", preview.Recreate)
	}
}

func TestServiceHashesRunsComposeCaptured(t *testing.T) {
	binary := scriptBinary(t, "echo 'warning: unset variable' >&2\n"+
		"printf 'api 0d4c2071\\ndb 66a7dfd2\\n'\nexit 0\n")

	hashes, err := binary.ServiceHashes(Project{Name: "shop"})
	if err != nil {
		t.Fatalf("ServiceHashes: %v", err)
	}
	// The stderr warning must not have leaked into the parse.
	if len(hashes) != 2 || hashes["api"] != "0d4c2071" || hashes["db"] != "66a7dfd2" {
		t.Errorf("got %v, want the two services from stdout only", hashes)
	}
}

// The two ways the command can come back empty-handed — a non-zero exit and a
// zero exit with nothing usable printed — must both surface as errors, so the
// caller falls back to running up directly.
func TestServiceHashesReportsFailure(t *testing.T) {
	cases := map[string]string{
		"command fails":  "echo 'unknown flag: --hash' >&2\nexit 1\n",
		"nothing usable": "exit 0\n",
	}

	for name, body := range cases {
		if _, err := scriptBinary(t, body).ServiceHashes(Project{Name: "shop"}); err == nil {
			t.Errorf("%s: got nil, want an error", name)
		}
	}
}

// hashedContainer builds a container labelled the way Compose stamps one.
func hashedContainer(service, hash string) docker.Container {
	return docker.Container{Labels: map[string]string{
		docker.LabelService:    service,
		docker.LabelConfigHash: hash,
	}}
}

// scriptBinary stands a shell script in for Compose, so the captured run is
// exercised for real without Compose or a daemon being installed.
func scriptBinary(t *testing.T, body string) Binary {
	t.Helper()
	script := filepath.Join(t.TempDir(), "compose")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Binary{script}
}
