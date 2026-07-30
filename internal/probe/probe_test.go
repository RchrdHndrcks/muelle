package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// The variable is written by hand in a compose file, so every shape someone
// would reasonably reach for has to work. The path alone is the common case:
// most containers publish one port and repeating it is noise.
func TestParseAcceptsEveryShapeOfSpec(t *testing.T) {
	cases := map[string]Spec{
		"/health":                      {Scheme: "http", Port: 0, Path: "/health"},
		"8080/health":                  {Scheme: "http", Port: 8080, Path: "/health"},
		"8080":                         {Scheme: "http", Port: 8080, Path: "/"},
		"http://localhost:8080/health": {Scheme: "http", Port: 8080, Path: "/health"},
		"https://localhost:8443/livez": {Scheme: "https", Port: 8443, Path: "/livez"},
		"8080/health?verbose=1":        {Scheme: "http", Port: 8080, Path: "/health?verbose=1"},
		"/api/v1/health":               {Scheme: "http", Port: 0, Path: "/api/v1/health"},
	}

	for value, want := range cases {
		got, err := Parse(value)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", value, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %+v, want %+v", value, got, want)
		}
	}
}

// A misspelled value must be reported rather than silently probing something
// else: a health indicator that is wrong is worse than none at all.
func TestParseRejectsWhatItCannotUnderstand(t *testing.T) {
	for _, value := range []string{"", "   ", "healthz", "99999/health", "-1/x", "ftp://host/f"} {
		if spec, err := Parse(value); err == nil {
			t.Errorf("%q: got %+v, want an error", value, spec)
		}
	}
}

// Probing through the published port is the only route that works everywhere,
// including Docker Desktop, where the bridge network is inside a VM the host
// cannot reach.
func TestResolvePrefersThePublishedPort(t *testing.T) {
	container := docker.Container{
		Ports: []docker.Port{{PrivatePort: 8080, PublicPort: 32768, Type: "tcp"}},
	}

	url, ok := Resolve(Spec{Scheme: "http", Port: 8080, Path: "/health"}, container, "172.17.0.2")

	if !ok || url != "http://127.0.0.1:32768/health" {
		t.Errorf("got %q (ok=%v), want the published port on the loopback", url, ok)
	}
}

// A service with no published port is still reachable from a muelle running on
// the same Linux host as the daemon, which is what muelle is for.
func TestResolveFallsBackToTheContainerAddress(t *testing.T) {
	container := docker.Container{Ports: []docker.Port{{PrivatePort: 9000, Type: "tcp"}}}

	url, ok := Resolve(Spec{Scheme: "http", Port: 9000, Path: "/health"}, container, "172.17.0.2")

	if !ok || url != "http://172.17.0.2:9000/health" {
		t.Errorf("got %q (ok=%v), want the container's own address", url, ok)
	}
}

// Naming the port is optional because most containers have exactly one, and
// guessing right saves the user from repeating themselves.
func TestResolveInfersTheOnlyPort(t *testing.T) {
	container := docker.Container{
		Ports: []docker.Port{{PrivatePort: 3000, PublicPort: 3000, Type: "tcp"}},
	}

	url, ok := Resolve(Spec{Scheme: "http", Path: "/health"}, container, "")

	if !ok || url != "http://127.0.0.1:3000/health" {
		t.Errorf("got %q (ok=%v), want the container's only port", url, ok)
	}
}

// Guessing between several ports would probe the wrong one about as often as
// the right one, and an indicator that is wrong half the time is worthless.
func TestResolveRefusesToGuessBetweenPorts(t *testing.T) {
	container := docker.Container{Ports: []docker.Port{
		{PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
		{PrivatePort: 9090, PublicPort: 9090, Type: "tcp"},
	}}

	if url, ok := Resolve(Spec{Scheme: "http", Path: "/health"}, container, ""); ok {
		t.Errorf("got %q, want no guess when the container has several ports", url)
	}
}

// UDP ports cannot answer an HTTP request, so they must not be inferred.
func TestResolveIgnoresUDPPorts(t *testing.T) {
	container := docker.Container{Ports: []docker.Port{
		{PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
		{PrivatePort: 5353, PublicPort: 5353, Type: "udp"},
	}}

	url, ok := Resolve(Spec{Scheme: "http", Path: "/health"}, container, "")

	if !ok || url != "http://127.0.0.1:8080/health" {
		t.Errorf("got %q (ok=%v), want the only TCP port", url, ok)
	}
}

func TestResolveGivesUpWithNothingToDial(t *testing.T) {
	if url, ok := Resolve(Spec{Scheme: "http", Port: 8080, Path: "/health"}, docker.Container{}, ""); ok {
		t.Errorf("got %q, want nothing when the port is neither published nor routable", url)
	}
}

// Any 2xx means the endpoint answered and said it was well. Anything else is
// the service's own way of saying it is not.
func TestProbeReadsTheStatusCode(t *testing.T) {
	cases := map[int]bool{200: true, 204: true, 301: false, 404: false, 500: false, 503: false}

	for code, healthy := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))

		result := New(time.Second).Do(context.Background(), server.URL+"/health")
		server.Close()

		if result.Healthy != healthy {
			t.Errorf("%d: got healthy=%v, want %v", code, result.Healthy, healthy)
		}
		if result.Status != code {
			t.Errorf("%d: got status %d", code, result.Status)
		}
	}
}

// A container that is starting refuses connections, which is a normal state
// and must be reported as unhealthy rather than crashing the refresh.
func TestProbeReportsAConnectionThatFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/health"
	server.Close()

	result := New(time.Second).Do(context.Background(), url)

	if result.Healthy || result.Err == nil {
		t.Errorf("got %+v, want an unhealthy result carrying the error", result)
	}
}

// A hung endpoint must not hold up the refresh cycle behind it.
func TestProbeGivesUpOnASlowEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	start := time.Now()
	result := New(50*time.Millisecond).Do(context.Background(), server.URL+"/health")

	if result.Healthy {
		t.Error("a timed-out probe is not a healthy one")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("took %v, want the probe to give up at its own timeout", elapsed)
	}
}

// A redirect to a login page is not proof of health, so the probe must report
// what the endpoint itself returned.
func TestProbeDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if result := New(time.Second).Do(context.Background(), server.URL+"/health"); result.Status != http.StatusFound {
		t.Errorf("got status %d, want the redirect itself", result.Status)
	}
}
