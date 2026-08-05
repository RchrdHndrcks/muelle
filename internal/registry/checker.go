package registry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// requestTimeout bounds every registry round trip. Checks run in the
// background and their results can wait, but a registry that has stopped
// answering must not hold a worker slot for minutes.
const requestTimeout = 10 * time.Second

// concurrency bounds how many images are checked at once. Checking a host's
// whole image list against Docker Hub with unbounded fan-out is the shape of
// request pattern rate limits exist for.
const concurrency = 4

// acceptManifests lists every manifest media type a tag can resolve to today:
// the two multi-platform indexes and the two single-image manifests, in both
// their Docker and OCI spellings. Asking for all four means the digest we get
// back is the digest of whatever the registry actually serves for the tag —
// which is what the daemon recorded in RepoDigests at pull time.
const acceptManifests = "application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.index.v1+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json"

var (
	errNoRealm  = errors.New("registry: bearer challenge names no realm")
	errNoToken  = errors.New("registry: token endpoint returned no token")
	errNoDigest = errors.New("registry: response carries no Docker-Content-Digest")
)

// statusError reports an unexpected registry status code.
type statusError struct {
	status int
	url    string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("registry: %s: unexpected status %d", e.url, e.status)
}

// Checker resolves tags against their registries and compares the answer with
// what is on disk. It is safe for concurrent use.
type Checker struct {
	client *http.Client
	creds  Credentials
}

// NewChecker builds a checker that authenticates from the docker CLI's stored
// logins, where plain ones exist, and anonymously everywhere else.
func NewChecker() *Checker {
	return &Checker{
		client: &http.Client{Timeout: requestTimeout},
		creds:  LoadCredentials(DefaultConfigPath()),
	}
}

// Digest returns what the registry's tag currently points at, as the
// Docker-Content-Digest of its manifest.
//
// The request is a HEAD — the digest lives in a header, and the manifest body
// behind it is of no use here — with a GET fallback for registries that
// answer HEAD without the header or refuse the method outright. Registries
// are spoken to over HTTPS only: a digest fetched over plaintext could be
// anyone's, and an "update available" marker must not be spoofable by the
// network.
func (c *Checker) Digest(ctx context.Context, ref Reference) (string, error) {
	digest, err := c.manifestDigest(ctx, ref, http.MethodHead)
	if err == nil {
		return digest, nil
	}
	// Only the cases a GET can actually cure are retried; a 401 or a network
	// failure would just fail the same way twice.
	var status *statusError
	if errors.Is(err, errNoDigest) ||
		(errors.As(err, &status) && status.status == http.StatusMethodNotAllowed) {
		return c.manifestDigest(ctx, ref, http.MethodGet)
	}
	return "", err
}

// manifestDigest performs one manifest request, following at most one
// authentication round trip.
//
// The flow is the generic one every v2 registry implements: try the request —
// with Basic credentials when the config file holds some for this host — and
// if the registry answers 401 naming a Bearer realm, fetch a token from it
// (anonymous unless credentials exist) and try once more. Docker Hub via
// auth.docker.io and ghcr.io are both just instances of this.
func (c *Checker) manifestDigest(ctx context.Context, ref Reference, method string) (string, error) {
	target := "https://" + ref.Host + "/v2/" + ref.Repo + "/manifests/" + ref.Tag
	user, pass, hasCreds := c.creds.For(ref.Host)

	authorization := ""
	if hasCreds {
		authorization = "Basic " + basicToken(user, pass)
	}
	digest, challenge, err := c.attempt(ctx, method, target, authorization)
	if err == nil || challenge == "" {
		return digest, err
	}

	scheme, params := parseChallenge(challenge)
	if scheme != "bearer" {
		return "", err
	}
	token, tokenErr := c.fetchToken(ctx, params, user, pass)
	if tokenErr != nil {
		return "", tokenErr
	}
	digest, _, err = c.attempt(ctx, method, target, "Bearer "+token)
	return digest, err
}

// attempt performs one manifest request. On a 401 it returns the
// WWW-Authenticate challenge alongside the error, so the caller can decide
// whether a token would help.
func (c *Checker) attempt(ctx context.Context, method, target, authorization string) (digest, challenge string, err error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", acceptManifests)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "", resp.Header.Get("WWW-Authenticate"), &statusError{status: resp.StatusCode, url: target}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", "", &statusError{status: resp.StatusCode, url: target}
	}
	digest = resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", "", errNoDigest
	}
	return digest, "", nil
}

// basicToken encodes credentials for an Authorization header.
func basicToken(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// Check answers whether the registry holds a newer image than the local one.
//
// Unknown is the answer whenever the comparison cannot honestly be made: the
// image carries no tag (a dangling or locally built image), no RepoDigests
// (built locally, so there is nothing it was pulled as), or the registry
// could not be asked. Only a digest actually fetched and actually different
// earns Outdated.
func (c *Checker) Check(ctx context.Context, image docker.Image) Verdict {
	local := localDigests(image)
	if len(local) == 0 {
		return VerdictUnknown
	}
	ref, err := ParseReference(image.Tag())
	if err != nil {
		return VerdictUnknown
	}
	remote, err := c.Digest(ctx, ref)
	if err != nil {
		return VerdictUnknown
	}
	if local[remote] {
		return VerdictCurrent
	}
	return VerdictOutdated
}

// localDigests collects the manifest digests the image was pulled as, from
// its RepoDigests ("redis@sha256:..." entries). An image pulled from several
// registries matches if any of them still serves the same content.
func localDigests(image docker.Image) map[string]bool {
	digests := make(map[string]bool, len(image.RepoDigests))
	for _, entry := range image.RepoDigests {
		if _, digest, found := strings.Cut(entry, "@"); found && digest != "" {
			digests[digest] = true
		}
	}
	return digests
}

// Result pairs an image's display reference with its verdict. The reference
// is empty for images that have none to display.
type Result struct {
	Reference string
	Verdict   Verdict
}

// CheckAll checks every image, a bounded few at a time, and returns one
// result per image in the given order.
func (c *Checker) CheckAll(ctx context.Context, images []docker.Image) []Result {
	results := make([]Result, len(images))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for i, image := range images {
		reference := ""
		if !image.Dangling() {
			reference = image.Tag()
		}
		results[i] = Result{Reference: reference}

		wg.Add(1)
		go func(i int, image docker.Image) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			results[i].Verdict = c.Check(ctx, image)
		}(i, image)
	}
	wg.Wait()
	return results
}
