package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// testChecker starts a TLS registry stub and returns a checker that trusts it,
// plus the host[:port] a Reference should name to reach it. TLS rather than
// plain HTTP because the checker only ever speaks HTTPS — a test server the
// production URL scheme cannot reach would be testing a different program.
func testChecker(t *testing.T, handler http.Handler) (*Checker, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return &Checker{client: server.Client(), creds: Credentials{}},
		strings.TrimPrefix(server.URL, "https://")
}

func TestDigestComesFromAHead(t *testing.T) {
	var gotMethod, gotAccept string
	checker, host := testChecker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/team/app/manifests/1.0" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotMethod = r.Method
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Docker-Content-Digest", "sha256:aaa")
	}))

	digest, err := checker.Digest(context.Background(), Reference{Host: host, Repo: "team/app", Tag: "1.0"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if digest != "sha256:aaa" {
		t.Errorf("digest = %q, want sha256:aaa", digest)
	}
	if gotMethod != http.MethodHead {
		t.Errorf("method = %s, want HEAD — the digest lives in a header", gotMethod)
	}
	// Without the index types in Accept, a multi-platform tag resolves to one
	// platform's manifest and the digest never matches what pull recorded.
	for _, accept := range []string{
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
	} {
		if !strings.Contains(gotAccept, accept) {
			t.Errorf("Accept %q missing %q", gotAccept, accept)
		}
	}
}

// The token dance Docker Hub and ghcr.io both use: a 401 names the realm, an
// anonymous token comes back from it, and the retry carries the token.
func TestDigestFollowsABearerChallenge(t *testing.T) {
	var serverURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.URL.Query().Get("service") != "test-registry" {
				t.Errorf("service = %q", r.URL.Query().Get("service"))
			}
			if r.URL.Query().Get("scope") != "repository:team/app:pull" {
				t.Errorf("scope = %q", r.URL.Query().Get("scope"))
			}
			json.NewEncoder(w).Encode(map[string]string{"token": "anon-token"})
		case "/v2/team/app/manifests/latest":
			if r.Header.Get("Authorization") != "Bearer anon-token" {
				w.Header().Set("WWW-Authenticate",
					`Bearer realm="`+serverURL+`/token",service="test-registry",scope="repository:team/app:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Docker-Content-Digest", "sha256:bbb")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	checker, host := testChecker(t, handler)
	serverURL = "https://" + host

	digest, err := checker.Digest(context.Background(), Reference{Host: host, Repo: "team/app", Tag: "latest"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if digest != "sha256:bbb" {
		t.Errorf("digest = %q, want sha256:bbb", digest)
	}
}

// Some registries answer a HEAD but only put the digest on a GET; others
// refuse HEAD outright. Both must fall back rather than report unknown.
func TestDigestFallsBackToGet(t *testing.T) {
	tests := []struct {
		name string
		head func(w http.ResponseWriter)
	}{
		{"no digest on HEAD", func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) }},
		{"method not allowed", func(w http.ResponseWriter) { w.WriteHeader(http.StatusMethodNotAllowed) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker, host := testChecker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					test.head(w)
					return
				}
				w.Header().Set("Docker-Content-Digest", "sha256:ccc")
			}))

			digest, err := checker.Digest(context.Background(), Reference{Host: host, Repo: "app", Tag: "1"})
			if err != nil {
				t.Fatalf("Digest: %v", err)
			}
			if digest != "sha256:ccc" {
				t.Errorf("digest = %q, want the GET fallback's answer", digest)
			}
		})
	}
}

// A registry that takes Basic auth directly — no token endpoint — gets the
// stored credential on the first request.
func TestDigestSendsStoredBasicCredentials(t *testing.T) {
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
	checker, host := testChecker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expected {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:ddd")
	}))
	checker.creds = Credentials{host: {username: "user", password: "secret"}}

	digest, err := checker.Digest(context.Background(), Reference{Host: host, Repo: "app", Tag: "1"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if digest != "sha256:ddd" {
		t.Errorf("digest = %q, want sha256:ddd", digest)
	}
}

// image builds a local image record as /images/json would report it, pulled
// from the test registry.
func image(host, digest string) docker.Image {
	img := docker.Image{RepoTags: []string{host + "/app:1"}}
	if digest != "" {
		img.RepoDigests = []string{host + "/app@" + digest}
	}
	return img
}

func TestCheckComparesRegistryAgainstRepoDigests(t *testing.T) {
	checker, host := testChecker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:new")
	}))

	if got := checker.Check(context.Background(), image(host, "sha256:new")); got != VerdictCurrent {
		t.Errorf("matching digest: got %v, want VerdictCurrent", got)
	}
	if got := checker.Check(context.Background(), image(host, "sha256:old")); got != VerdictOutdated {
		t.Errorf("moved tag: got %v, want VerdictOutdated", got)
	}
}

// The cases where the honest answer is "cannot say" — and never "update
// available", because a false ↑ sends someone pulling for nothing.
func TestCheckReportsUnknownWhenItCannotKnow(t *testing.T) {
	checker, host := testChecker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	// A locally built image was never pulled, so there is nothing on record
	// to compare the registry against.
	if got := checker.Check(context.Background(), image(host, "")); got != VerdictUnknown {
		t.Errorf("no RepoDigests: got %v, want VerdictUnknown", got)
	}
	// A dangling image has no tag to resolve.
	dangling := docker.Image{RepoDigests: []string{host + "/app@sha256:x"}}
	if got := checker.Check(context.Background(), dangling); got != VerdictUnknown {
		t.Errorf("no tag: got %v, want VerdictUnknown", got)
	}
	// A registry that errors has not said the tag moved.
	if got := checker.Check(context.Background(), image(host, "sha256:x")); got != VerdictUnknown {
		t.Errorf("registry failure: got %v, want VerdictUnknown", got)
	}
}

func TestCheckAllPairsVerdictsWithReferences(t *testing.T) {
	checker, host := testChecker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:new")
	}))

	results := checker.CheckAll(context.Background(), []docker.Image{
		image(host, "sha256:new"),
		image(host, "sha256:old"),
		{}, // dangling: no tags at all
	})

	want := []Result{
		{Reference: host + "/app:1", Verdict: VerdictCurrent},
		{Reference: host + "/app:1", Verdict: VerdictOutdated},
		{Reference: "", Verdict: VerdictUnknown},
	}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d", len(results), len(want))
	}
	for i := range want {
		if results[i] != want[i] {
			t.Errorf("result %d = %+v, want %+v", i, results[i], want[i])
		}
	}
}
