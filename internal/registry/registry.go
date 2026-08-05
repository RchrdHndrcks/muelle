// Package registry asks an image registry whether it holds a newer version of
// a tag than the one pulled locally, using the Registry HTTP API v2 and
// nothing but the standard library.
//
// A tag is a moving pointer: "redis:7-alpine" on the registry drifts away from
// the "redis:7-alpine" pulled three months ago, and nothing on the host says
// so. The daemon does record what a tag pointed at when it was pulled — the
// RepoDigests on every pulled image — and the registry will name what the tag
// points at now, in the Docker-Content-Digest header of a manifest request
// that costs a HEAD. Comparing the two answers "is there something newer to
// pull?" without downloading anything.
//
// The comparison is deliberately three-valued. A locally built image has no
// RepoDigests, a dangling image has no tag, and a registry can be unreachable
// or private; in every one of those cases the honest answer is "unknown", not
// "up to date" and certainly not "update available". An indicator that guesses
// is worse than none, because it is believed.
package registry

import (
	"errors"
	"strings"
)

// Verdict is what a check concluded about one image.
type Verdict int

const (
	// VerdictUnknown means the question could not be answered: the image has
	// no tag, was never pulled from a registry, or the registry did not say.
	VerdictUnknown Verdict = iota
	// VerdictCurrent means the registry's tag still points at what was pulled.
	VerdictCurrent
	// VerdictOutdated means the registry has moved the tag to a newer image.
	VerdictOutdated
)

// dockerHubAlias is how image references name Docker Hub, and dockerHubHost is
// where its Registry API actually answers. The two differ for historical
// reasons: "docker.io" serves the website, not manifests.
const (
	dockerHubAlias = "docker.io"
	dockerHubHost  = "registry-1.docker.io"
)

// Reference is a parsed image reference, resolved far enough to query.
type Reference struct {
	// Host is the registry to ask, as a host[:port]. For bare names this is
	// Docker Hub's API host, not the "docker.io" the reference implies.
	Host string
	// Repo is the repository path within the registry, with Docker Hub's
	// implicit "library/" prefix already applied.
	Repo string
	// Tag is the tag to resolve, defaulting to "latest".
	Tag string
}

// ParseReference resolves an image reference the way the docker CLI does.
//
// The first segment is a registry host only when it can be nothing else — it
// contains a dot or a port, or is "localhost". Everything else is a Docker Hub
// repository, and single-segment Hub names ("redis") live under the implicit
// "library/" namespace. This mirrors how the daemon resolved the name at pull
// time, which is the only way the digest comparison compares like with like.
func ParseReference(name string) (Reference, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Reference{}, errors.New("empty reference")
	}
	// A digest reference is already pinned; there is no tag to have moved.
	if strings.Contains(name, "@") {
		return Reference{}, errors.New("digest reference has no tag to check")
	}

	tag := "latest"
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		tag = name[i+1:]
		name = name[:i]
	}
	if name == "" || tag == "" || strings.Contains(name, "<none>") {
		return Reference{}, errors.New("no usable tag")
	}

	host := dockerHubAlias
	repo := name
	if i := strings.Index(name, "/"); i >= 0 {
		if first := name[:i]; strings.ContainsAny(first, ".:") || first == "localhost" {
			host, repo = first, name[i+1:]
		}
	}
	if repo == "" {
		return Reference{}, errors.New("empty repository")
	}
	if host == dockerHubAlias {
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
		host = dockerHubHost
	}
	return Reference{Host: host, Repo: repo, Tag: tag}, nil
}
