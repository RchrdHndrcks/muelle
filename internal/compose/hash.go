package compose

import (
	"errors"
	"os/exec"
	"sort"
	"strings"
)

// ServiceHashes asks Compose what each of the project's services hashes to
// with the files as they are now, by running "config --hash '*'" and reading
// its output.
//
// Run captured rather than through the terminal handover: the command is
// non-interactive and its output is for muelle to read, not for the user to
// watch, so suspending the interface for it would be a flicker for nothing.
// Stdout only — Compose writes warnings (orphaned containers, unset
// variables) to stderr, and folding those into the parse would corrupt it.
func (b Binary) ServiceHashes(project Project) (map[string]string, error) {
	argv := b.Command(project, ActionConfigHash)
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, err
	}
	hashes := ParseServiceHashes(string(out))
	if len(hashes) == 0 {
		// A Compose that does not understand --hash can exit zero having
		// printed nothing usable. Report that as the failure it is, so the
		// caller falls back instead of previewing an empty project.
		return nil, errors.New("compose reported no service hashes")
	}
	return hashes, nil
}

// ParseServiceHashes parses "config --hash" output: one "<service> <hash>"
// line per service.
//
// Lines in any other shape are skipped rather than failing the whole read.
// The preview this feeds is best-effort by design, and a stray diagnostic
// line must not cost the user the hashes printed around it.
func ParseServiceHashes(output string) map[string]string {
	hashes := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		hashes[fields[0]] = fields[1]
	}
	return hashes
}

// UpPreview is what "up -d" would do to each of a project's services.
type UpPreview struct {
	// Recreate are the services up will replace or create: their files no
	// longer hash to what their containers were created from, or they have
	// no container at all.
	Recreate []string
	// Unchanged are the services up will leave as they are.
	Unchanged []string
}

// PreviewUp classifies a project's services by comparing the hash stamped on
// each container against what the files hash to now.
//
// This is the same comparison Compose makes when "up" runs, read from the
// same two places — the config-hash label and the rendered files — so the
// prediction and the action agree. Services are taken from the hash output
// rather than the containers, because that is the set up will act on: a
// service added to the file has no container yet, and one removed from it is
// not up's to touch.
func PreviewUp(project Project, hashes map[string]string) UpPreview {
	var preview UpPreview
	for service, hash := range hashes {
		if upToDate(project, service, hash) {
			preview.Unchanged = append(preview.Unchanged, service)
			continue
		}
		preview.Recreate = append(preview.Recreate, service)
	}
	sort.Strings(preview.Recreate)
	sort.Strings(preview.Unchanged)
	return preview
}

// upToDate reports whether a service has at least one container and every one
// of them was created from the given hash. A container with no hash label —
// created by a Compose too old to stamp it — counts as divergent, which is
// also how Compose itself treats it.
func upToDate(project Project, service, hash string) bool {
	found := false
	for _, container := range project.Containers {
		if container.Service() != service {
			continue
		}
		found = true
		if container.ConfigHash() != hash {
			return false
		}
	}
	return found
}
