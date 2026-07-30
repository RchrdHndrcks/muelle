package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// The environment is fixed when a container is created and a recreated
// container has a new ID, so one inspect per ID is not an optimisation — it is
// the whole truth, for as long as that container exists.
func TestSweepInspectsAContainerOnlyOnce(t *testing.T) {
	server, port := healthServer(t, http.StatusOK)
	inspector := &countingInspector{env: EnvVar + "=/health"}
	containers := []docker.Container{publishing("api", port)}
	watcher := NewWatcher(time.Second)

	watcher.Sweep(context.Background(), inspector, containers)
	watcher.Sweep(context.Background(), inspector, containers)
	server.Close()

	if inspector.calls() != 1 {
		t.Errorf("inspected %d times, want once for the life of the container", inspector.calls())
	}
}

func TestSweepReportsWhatTheEndpointAnswered(t *testing.T) {
	server, port := healthServer(t, http.StatusOK)
	defer server.Close()
	inspector := &countingInspector{env: EnvVar + "=/health"}

	results := NewWatcher(time.Second).Sweep(context.Background(), inspector,
		[]docker.Container{publishing("api", port)})

	result, found := results["api"]
	if !found {
		t.Fatal("a container that opts in must be probed")
	}
	if !result.Healthy || result.Status != http.StatusOK {
		t.Errorf("got %+v, want a healthy 200", result)
	}
}

// Almost no container opts in, so the common path has to cost one inspect and
// nothing else — no request to a service that never asked to be probed.
func TestSweepLeavesAContainerThatDidNotOptInAlone(t *testing.T) {
	inspector := &countingInspector{env: "PATH=/usr/bin"}
	containers := []docker.Container{publishing("api", 8080)}
	watcher := NewWatcher(time.Second)

	watcher.Sweep(context.Background(), inspector, containers)
	results := watcher.Sweep(context.Background(), inspector, containers)

	if len(results) != 0 {
		t.Errorf("got %v, want no results", results)
	}
	if inspector.calls() != 1 {
		t.Errorf("inspected %d times, want the answer remembered", inspector.calls())
	}
}

// A value with a typo in it must be reported once and then left alone, rather
// than re-inspected forever or probed at a guessed address.
func TestSweepDoesNotRetryAnUnreadableSpec(t *testing.T) {
	inspector := &countingInspector{env: EnvVar + "=healthz"}
	containers := []docker.Container{publishing("api", 8080)}
	watcher := NewWatcher(time.Second)

	watcher.Sweep(context.Background(), inspector, containers)
	watcher.Sweep(context.Background(), inspector, containers)

	if inspector.calls() != 1 {
		t.Errorf("inspected %d times, want the bad value remembered", inspector.calls())
	}
}

// A container's address on the bridge can change when it restarts, which would
// otherwise leave the indicator stuck at red until muelle was restarted. A
// probe that could not connect drops what was cached so the next sweep looks
// again; one that answered 503 does not, because there is nothing stale about
// it.
func TestSweepLooksAgainAfterAProbeCannotConnect(t *testing.T) {
	inspector := &countingInspector{env: EnvVar + "=/health"}
	// Nothing is listening on this port, so the probe cannot connect.
	containers := []docker.Container{publishing("api", 1)}
	watcher := NewWatcher(50 * time.Millisecond)

	watcher.Sweep(context.Background(), inspector, containers)
	watcher.Sweep(context.Background(), inspector, containers)

	if inspector.calls() != 2 {
		t.Errorf("inspected %d times, want a second look after the connection failed", inspector.calls())
	}
}

func TestSweepKeepsWhatItKnowsWhenTheServiceAnswersBadly(t *testing.T) {
	server, port := healthServer(t, http.StatusServiceUnavailable)
	defer server.Close()
	inspector := &countingInspector{env: EnvVar + "=/health"}
	containers := []docker.Container{publishing("api", port)}
	watcher := NewWatcher(time.Second)

	watcher.Sweep(context.Background(), inspector, containers)
	results := watcher.Sweep(context.Background(), inspector, containers)

	if inspector.calls() != 1 {
		t.Errorf("inspected %d times: a 503 is an answer, not a stale address", inspector.calls())
	}
	if results["api"].Healthy || results["api"].Status != http.StatusServiceUnavailable {
		t.Errorf("got %+v, want the 503 reported", results["api"])
	}
}

// A container that is not running cannot answer, and asking would only produce
// a connection error the state column already knows about.
func TestSweepSkipsAContainerThatIsNotRunning(t *testing.T) {
	inspector := &countingInspector{env: EnvVar + "=/health"}
	stopped := publishing("api", 8080)
	stopped.State = "exited"

	results := NewWatcher(time.Second).Sweep(context.Background(), inspector, []docker.Container{stopped})

	if len(results) != 0 || inspector.calls() != 0 {
		t.Errorf("got %v after %d inspects, want a stopped container left alone", results, inspector.calls())
	}
}

// Containers come and go all day. What muelle remembers about them must not.
func TestSweepForgetsContainersThatAreGone(t *testing.T) {
	inspector := &countingInspector{env: EnvVar + "=/health"}
	watcher := NewWatcher(50 * time.Millisecond)

	watcher.Sweep(context.Background(), inspector, []docker.Container{publishing("api", 1)})
	watcher.Sweep(context.Background(), inspector, nil)

	if remembered := watcher.known(); remembered != 0 {
		t.Errorf("still remembering %d containers, want none", remembered)
	}
}

// healthServer starts a server answering with the given status and returns the
// port it listens on.
func healthServer(t *testing.T, status int) (*httptest.Server, int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	_, port, err := splitPort(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return server, port
}

func splitPort(rawURL string) (string, int, error) {
	host := strings.TrimPrefix(rawURL, "http://")
	index := strings.LastIndex(host, ":")
	if index < 0 {
		return "", 0, errors.New("no port in " + rawURL)
	}
	port, err := strconv.Atoi(host[index+1:])
	return host[:index], port, err
}

// publishing builds a running container publishing one port on the loopback.
func publishing(id string, port int) docker.Container {
	return docker.Container{
		ID:    id,
		Names: []string{"/" + id},
		State: "running",
		Ports: []docker.Port{{PrivatePort: port, PublicPort: port, Type: "tcp"}},
	}
}

// countingInspector answers every inspect with the same environment and counts
// how many times it was asked.
type countingInspector struct {
	env string

	mu sync.Mutex
	n  int
}

func (i *countingInspector) Inspect(_ context.Context, _ string) (docker.ContainerDetail, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++

	var detail docker.ContainerDetail
	detail.Config.Env = []string{i.env}
	return detail, nil
}

func (i *countingInspector) calls() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.n
}
