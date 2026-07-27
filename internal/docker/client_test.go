package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestResolveHostPrefersDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://192.168.1.10:2375")

	host, err := ResolveHost(func(string) bool { return true })
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if host != "tcp://192.168.1.10:2375" {
		t.Errorf("got %q, want DOCKER_HOST to win", host)
	}
}

func TestResolveHostProbesCandidatesInOrder(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	colima := filepath.Join(home, ".colima", "default", "docker.sock")

	// Only the Colima socket exists, as on the development machine.
	host, err := ResolveHost(func(path string) bool { return path == colima })
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if host != "unix://"+colima {
		t.Errorf("got %q, want unix://%s", host, colima)
	}
}

func TestResolveHostPrefersSystemSocketOverUserSockets(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("HOME", t.TempDir())

	host, err := ResolveHost(func(string) bool { return true })
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if host != "unix:///var/run/docker.sock" {
		t.Errorf("got %q, want the standard socket to take precedence", host)
	}
}

func TestNewRejectsUnsupportedScheme(t *testing.T) {
	if _, err := New("npipe:////./pipe/docker_engine"); err == nil {
		t.Fatal("expected an error for an unsupported scheme")
	}
}

// recorder collects the paths a fake daemon was asked for.
//
// It is mutex-guarded because some client methods fan out over several
// goroutines, so the handler runs concurrently.
type recorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *recorder) add(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
}

// All returns a copy of the recorded paths.
func (r *recorder) All() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

// Len returns how many requests were recorded.
func (r *recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

// First returns the first recorded path, or "" if there was none.
func (r *recorder) First() string {
	all := r.All()
	if len(all) == 0 {
		return ""
	}
	return all[0]
}

// newTestClient starts a fake daemon and returns a client pointed at it along
// with a record of the paths it was asked for.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *recorder) {
	t.Helper()
	requested := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.add(r.URL.String())
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return newForTest(server.URL), requested
}

func TestContainersDecodesAndSorts(t *testing.T) {
	payload := []map[string]any{
		{"Id": "ccc", "Names": []string{"/standalone"}, "State": "running"},
		{"Id": "bbb", "Names": []string{"/shop-web"}, "State": "running",
			"Labels": map[string]string{LabelProject: "shop"}},
		{"Id": "aaa", "Names": []string{"/shop-api"}, "State": "exited",
			"Labels": map[string]string{LabelProject: "shop"}},
	}
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(payload)
	})

	containers, err := client.Containers(context.Background(), true)
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}

	var names []string
	for _, c := range containers {
		names = append(names, c.Name())
	}
	want := []string{"shop-api", "shop-web", "standalone"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("got order %v, want %v (project first, standalone last)", names, want)
	}
	if got := requested.First(); got != "/containers/json?all=1" {
		t.Errorf("got request %q, want all=1", got)
	}
}

func TestContainersOmitsAllWhenOnlyRunningWanted(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})

	if _, err := client.Containers(context.Background(), false); err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if got := requested.First(); got != "/containers/json" {
		t.Errorf("got request %q, want no query string", got)
	}
}

func TestStopSendsTimeout(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.Stop(context.Background(), "abc", 7); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := requested.First(); got != "/containers/abc/stop?t=7" {
		t.Errorf("got request %q, want the timeout in the query", got)
	}
}

func TestAPIErrorCarriesDaemonMessage(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"container is running: stop it first"}`))
	})

	err := client.Remove(context.Background(), "abc", false, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "stop it first") {
		t.Errorf("got %q, want the daemon message preserved", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Errorf("got %#v, want an APIError carrying status 409", err)
	}
}

func TestNotModifiedIdentifies304(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})

	err := client.Start(context.Background(), "abc")
	if !NotModified(err) {
		t.Errorf("got %v, want it recognised as not-modified", err)
	}
}

func TestVolumesUnwrapsEnvelope(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Volumes":[{"Name":"zeta"},{"Name":"alpha"}]}`))
	})

	volumes, err := client.Volumes(context.Background())
	if err != nil {
		t.Fatalf("Volumes: %v", err)
	}
	if len(volumes) != 2 || volumes[0].Name != "alpha" {
		t.Errorf("got %+v, want two volumes sorted by name", volumes)
	}
}

func TestPruneVolumesReportsReclaimedSpace(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"VolumesDeleted":["a","b"],"SpaceReclaimed":2048}`))
	})

	result, err := client.PruneVolumes(context.Background())
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if result.Deleted != 2 || result.SpaceReclaimed != 2048 {
		t.Errorf("got %+v, want 2 deleted and 2048 bytes reclaimed", result)
	}
}

// TestUnixSocketTransport exercises the real dial path: a daemon listening on
// a Unix socket, reached through the same code production uses.
func TestUnixSocketTransport(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Version":"27.4.0","ApiVersion":"1.47","Os":"linux","Arch":"arm64"}`))
		})},
	}
	server.Start()
	defer server.Close()

	client, err := New("unix://" + socket)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	version, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if version.APIVersion != "1.47" {
		t.Errorf("got API version %q, want 1.47", version.APIVersion)
	}
}
