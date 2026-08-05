package registry

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// writeConfig drops a docker config.json into a temp dir and returns its path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadCredentialsReadsPlainAuths(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("hubuser:hubpass"))
	path := writeConfig(t, `{
		"auths": {
			"https://index.docker.io/v1/": {"auth": "`+auth+`"},
			"ghcr.io": {"username": "ghuser", "password": "ghpass"}
		}
	}`)

	creds := LoadCredentials(path)

	// Docker Hub logins land under the legacy index key, while manifests are
	// fetched from registry-1; the lookup has to bridge that gap.
	user, pass, ok := creds.For("registry-1.docker.io")
	if !ok || user != "hubuser" || pass != "hubpass" {
		t.Errorf("For(registry-1.docker.io) = %q/%q/%v, want the index.docker.io entry", user, pass, ok)
	}
	user, pass, ok = creds.For("ghcr.io")
	if !ok || user != "ghuser" || pass != "ghpass" {
		t.Errorf("For(ghcr.io) = %q/%q/%v, want the plain entry", user, pass, ok)
	}
	if _, _, ok := creds.For("unknown.example"); ok {
		t.Error("a host with no entry should have no credentials")
	}
}

// A helper-managed registry keeps no secret in the file; the entry must be
// skipped silently and the registry checked anonymously, not failed.
func TestLoadCredentialsSkipsCredentialHelpers(t *testing.T) {
	path := writeConfig(t, `{
		"credsStore": "desktop",
		"credHelpers": {"ghcr.io": "gh"},
		"auths": {"ghcr.io": {}}
	}`)

	creds := LoadCredentials(path)

	if _, _, ok := creds.For("ghcr.io"); ok {
		t.Error("an empty auths entry (helper-managed) should yield no credentials")
	}
}

// Anonymous access must survive there being no config file at all: most
// public images need no login, and most servers have never run docker login.
func TestLoadCredentialsToleratesMissingOrBrokenFiles(t *testing.T) {
	if got := LoadCredentials(filepath.Join(t.TempDir(), "absent.json")); len(got) != 0 {
		t.Errorf("missing file: got %v, want no credentials", got)
	}
	if got := LoadCredentials(writeConfig(t, "{not json")); len(got) != 0 {
		t.Errorf("malformed file: got %v, want no credentials", got)
	}
}

func TestParseChallengeReadsABearerHeader(t *testing.T) {
	scheme, params := parseChallenge(
		`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/redis:pull"`)

	if scheme != "bearer" {
		t.Errorf("scheme = %q, want bearer", scheme)
	}
	if params["realm"] != "https://auth.docker.io/token" {
		t.Errorf("realm = %q", params["realm"])
	}
	if params["service"] != "registry.docker.io" {
		t.Errorf("service = %q", params["service"])
	}
	if params["scope"] != "repository:library/redis:pull" {
		t.Errorf("scope = %q", params["scope"])
	}
}

// A scope can carry a comma inside its quotes; splitting on every comma would
// cut it in half and request a token for a scope the registry never named.
func TestParseChallengeKeepsQuotedCommasIntact(t *testing.T) {
	_, params := parseChallenge(`Bearer realm="https://r/token",scope="repository:app:pull,push"`)

	if params["scope"] != "repository:app:pull,push" {
		t.Errorf("scope = %q, want the comma preserved", params["scope"])
	}
}
