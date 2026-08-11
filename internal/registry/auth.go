package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Credentials maps a registry host to the username and password stored for it.
type Credentials map[string]credential

type credential struct {
	username string
	password string
}

// DefaultConfigPath is where the docker CLI keeps its login state, honouring
// the same override the CLI does.
func DefaultConfigPath() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker", "config.json")
}

// LoadCredentials reads plain auth entries from a docker config.json.
//
// Only inline "auths" entries are read. Credential helpers and credsStore
// would mean shelling out to an external program per lookup, and a host using
// them keeps no secret in the file to read — so they are silently skipped and
// their registries are simply checked anonymously. A missing or malformed file
// is treated the same way: this feature must degrade to anonymous access, not
// to an error, because most public images need no credentials at all.
func LoadCredentials(path string) Credentials {
	creds := make(Credentials)
	if path == "" {
		return creds
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return creds
	}
	var file struct {
		Auths map[string]struct {
			Auth     string `json:"auth"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return creds
	}
	for key, entry := range file.Auths {
		username, password := entry.Username, entry.Password
		if entry.Auth != "" {
			if user, pass, ok := decodeAuth(entry.Auth); ok {
				username, password = user, pass
			}
		}
		if username == "" && password == "" {
			continue
		}
		creds[normalizeHost(key)] = credential{username: username, password: password}
	}
	return creds
}

// decodeAuth unpacks the base64 "user:password" the CLI writes.
func decodeAuth(auth string) (user, pass string, ok bool) {
	decoded, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		// Some tools write the unpadded form; accept it rather than losing
		// the credential over a formatting difference.
		decoded, err = base64.RawStdEncoding.DecodeString(auth)
		if err != nil {
			return "", "", false
		}
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	return user, pass, found
}

// normalizeHost reduces the many ways config.json spells a registry to a bare
// host, so "https://index.docker.io/v1/" and "index.docker.io" are one key.
func normalizeHost(key string) string {
	key = strings.TrimPrefix(key, "https://")
	key = strings.TrimPrefix(key, "http://")
	if i := strings.Index(key, "/"); i >= 0 {
		key = key[:i]
	}
	return key
}

// For returns the stored credential for a registry host, if any.
//
// Docker Hub is the special case: logins land in the file under the legacy
// "index.docker.io" key, while manifests are fetched from a different host
// entirely, so the aliases are tried too.
func (c Credentials) For(host string) (user, pass string, ok bool) {
	candidates := []string{host}
	if host == dockerHubHost {
		candidates = append(candidates, "index.docker.io", dockerHubAlias)
	}
	for _, candidate := range candidates {
		if cred, found := c[candidate]; found {
			return cred.username, cred.password, true
		}
	}
	return "", "", false
}

// parseChallenge reads the parameters out of a WWW-Authenticate header,
// for example:
//
//	Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/redis:pull"
//
// The scheme is returned lower-cased; params keys likewise. The parsing is
// deliberately forgiving — a challenge this code cannot read just means the
// check reports unknown, so there is nothing to gain from strictness.
func parseChallenge(header string) (scheme string, params map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
	params = make(map[string]string)
	for _, part := range splitChallengeParams(rest) {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		params[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return strings.ToLower(scheme), params
}

// splitChallengeParams splits on commas that sit outside quoted values. A
// scope like "repository:a:pull,push" carries a comma that a plain Split would
// cut in half.
func splitChallengeParams(s string) []string {
	var (
		parts  []string
		start  int
		quoted bool
	)
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// fetchToken performs the Bearer half of the token flow: ask the realm the
// 401 named for a token covering the scope it named. Without credentials this
// yields the anonymous token that public images on Docker Hub and ghcr.io are
// served with; with them, the token carries the login.
func (c *Checker) fetchToken(ctx context.Context, params map[string]string, user, pass string) (string, error) {
	realm := params["realm"]
	if realm == "" {
		return "", errNoRealm
	}
	query := url.Values{}
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	if scope := params["scope"]; scope != "" {
		query.Set("scope", scope)
	}
	target := realm
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &statusError{status: resp.StatusCode, url: realm}
	}

	// Two field names for the same thing: the distribution spec says "token",
	// OAuth-shaped services say "access_token", and some send both.
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", errNoToken
}
