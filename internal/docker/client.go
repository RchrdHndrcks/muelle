// Package docker is a minimal client for the Docker Engine API, speaking HTTP
// over a Unix socket (or TCP) with nothing but the standard library.
//
// Only the endpoints muelle needs are implemented. Requests are sent
// unversioned (for example "GET /containers/json"), which the daemon serves
// with its most recent API version; every field decoded here has been stable
// since API 1.24.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Client talks to a single Docker daemon.
type Client struct {
	// baseURL is the scheme://host prefix for every request. For Unix
	// sockets the host is a placeholder, since the dialer ignores it.
	baseURL string
	http    *http.Client
	// host is the original endpoint (for example
	// "unix:///var/run/docker.sock"), kept for error messages.
	host string
}

// SocketCandidates lists the well-known daemon socket paths, probed in order
// when DOCKER_HOST is unset. Docker Desktop, Colima and rootless installs all
// put the socket somewhere other than /var/run.
func SocketCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	paths := []string{"/var/run/docker.sock"}
	if home != "" {
		paths = append(paths,
			filepath.Join(home, ".docker", "run", "docker.sock"),
			filepath.Join(home, ".colima", "default", "docker.sock"),
			filepath.Join(home, ".rd", "docker.sock"),
		)
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		paths = append(paths, filepath.Join(dir, "docker.sock"))
	}
	return paths
}

// ResolveHost determines which daemon endpoint to use. DOCKER_HOST wins if
// set; otherwise the well-known socket paths are probed, and finally the
// docker CLI is asked for the current context's endpoint. exists reports
// whether a socket path is usable and is injected so the lookup can be tested
// without touching the filesystem.
func ResolveHost(exists func(path string) bool) (string, error) {
	if h := strings.TrimSpace(os.Getenv("DOCKER_HOST")); h != "" {
		return h, nil
	}
	candidates := SocketCandidates()
	for _, path := range candidates {
		if exists(path) {
			return "unix://" + path, nil
		}
	}
	if h := hostFromCLIContext(); h != "" {
		return h, nil
	}
	return "", fmt.Errorf("no docker daemon found: set DOCKER_HOST or start docker (probed %s)", strings.Join(candidates, ", "))
}

// hostFromCLIContext asks the docker CLI where the active context points. It
// is the last resort: custom contexts store the endpoint in the CLI's own
// config, so probing paths cannot find them. Returns "" when the CLI is
// absent or reports nothing usable.
func hostFromCLIContext() string {
	if _, err := exec.LookPath("docker"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "context", "inspect",
		"--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SocketExists reports whether path is an existing socket (or at least an
// existing file we can try to dial). It is the production implementation of
// ResolveHost's exists argument.
func SocketExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

// New connects to the daemon at host, which may be a unix://, tcp://, http://
// or bare-path endpoint. An empty host triggers ResolveHost.
func New(host string) (*Client, error) {
	if host == "" {
		resolved, err := ResolveHost(SocketExists)
		if err != nil {
			return nil, err
		}
		host = resolved
	}
	transport := &http.Transport{
		// Keeping this low matters for log/stat streams, which hold a
		// connection open for as long as the view is on screen.
		MaxIdleConns:    8,
		IdleConnTimeout: 30 * time.Second,
	}
	base := ""

	switch {
	case strings.HasPrefix(host, "unix://"), strings.HasPrefix(host, "/"):
		path := strings.TrimPrefix(host, "unix://")
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", path)
		}
		// The host in the URL is never resolved (the dialer ignores it)
		// but net/http requires something syntactically valid.
		base = "http://docker"
	case strings.HasPrefix(host, "tcp://"):
		base = "http://" + strings.TrimPrefix(host, "tcp://")
	case strings.HasPrefix(host, "http://"), strings.HasPrefix(host, "https://"):
		base = host
	default:
		return nil, fmt.Errorf("unsupported docker host %q", host)
	}

	return &Client{
		baseURL: base,
		host:    host,
		// No client-wide timeout: streaming endpoints must be able to
		// stay open indefinitely. Per-request deadlines come from the
		// context passed by callers.
		http: &http.Client{Transport: transport},
	}, nil
}

// newForTest builds a Client aimed at an arbitrary base URL, for httptest.
func newForTest(baseURL string) *Client {
	return &Client{baseURL: baseURL, host: baseURL, http: &http.Client{}}
}

// Host returns the endpoint this client was configured with.
func (c *Client) Host() string { return c.host }

// APIError is returned for any non-2xx daemon response. It carries the status
// code so callers can distinguish "already stopped" (304) from real failures.
type APIError struct {
	Status  int
	Message string
	Path    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("docker: %s: unexpected status %d", e.Path, e.Status)
	}
	return fmt.Sprintf("docker: %s: %s", e.Path, e.Message)
}

// NotModified reports whether err is a 304, which the daemon uses to mean the
// container was already in the requested state. Callers generally treat this
// as success.
func NotModified(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotModified
}

// do issues a request and returns the raw response body for 2xx replies. The
// caller owns the returned ReadCloser.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader) (io.ReadCloser, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, &APIError{
			Status:  resp.StatusCode,
			Message: decodeErrorMessage(resp.Body),
			Path:    path,
		}
	}
	return resp.Body, nil
}

// decodeErrorMessage pulls the human-readable message out of a daemon error
// body, which is {"message": "..."} in the normal case.
func decodeErrorMessage(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, 8<<10))
	if err != nil || len(raw) == 0 {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	return strings.TrimSpace(string(raw))
}

// getJSON performs a GET and decodes the JSON response into out.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return fmt.Errorf("docker: %s: decode: %w", path, err)
	}
	return nil
}

// post performs a POST whose response body is discarded, the shape of every
// lifecycle action.
func (c *Client) post(ctx context.Context, path string, query url.Values, payload any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	resp, err := c.do(ctx, http.MethodPost, path, query, body)
	if err != nil {
		return err
	}
	defer resp.Close()
	_, err = io.Copy(io.Discard, resp)
	return err
}

// decodeJSON decodes r into out, used where the caller already owns the
// response body.
func decodeJSON(r io.Reader, out any) error {
	if err := json.NewDecoder(r).Decode(out); err != nil {
		return fmt.Errorf("docker: decode: %w", err)
	}
	return nil
}

// marshalIndent renders a decoded payload as human-readable JSON for the
// inspect pager.
func marshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// Ping verifies the daemon is reachable and returns its reported version.
func (c *Client) Ping(ctx context.Context) (Version, error) {
	var v Version
	err := c.getJSON(ctx, "/version", nil, &v)
	return v, err
}
