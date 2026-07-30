// Package probe asks a container whether it is well, over HTTP, from outside.
//
// Docker's own HEALTHCHECK runs the test inside the container, which means the
// image has to carry something that can make an HTTP request. Images built on
// scratch or a distroless base carry no curl, no wget and no shell, so they
// cannot have a healthcheck at all — and those are exactly the images where
// "is it actually serving?" is hardest to answer by looking.
//
// A container opts in by naming its endpoint in an environment variable. muelle
// makes the request itself, from the host, needing nothing inside the image.
package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// EnvVar is the environment variable a container sets to be probed.
const EnvVar = "MUELLE_HEALTH"

// Spec is where a container says its health endpoint lives.
type Spec struct {
	// Scheme is http or https.
	Scheme string
	// Port is the port inside the container. Zero means the container has
	// only one and muelle should use it.
	Port int
	// Path includes any query string, and always starts with a slash.
	Path string
}

// maxPort is the highest number a TCP port can be.
const maxPort = 65535

// Parse reads the value of the environment variable.
//
// Four shapes are accepted, in the order someone would reach for them:
// "/health" (the container has one port), "8080/health", "8080", and a full
// URL. Anything else is an error rather than a guess: an indicator that is
// quietly wrong is worse than no indicator, because it is believed.
func Parse(value string) (Spec, error) {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return Spec{}, errors.New("empty")

	case strings.Contains(value, "://"):
		return parseURL(value)

	case strings.HasPrefix(value, "/"):
		return Spec{Scheme: "http", Path: value}, nil
	}

	port, rest, hasPath := strings.Cut(value, "/")
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > maxPort {
		return Spec{}, fmt.Errorf(
			"%q is not a path (/health), a port (8080), a port and path (8080/health) or a URL", value)
	}
	spec := Spec{Scheme: "http", Port: number, Path: "/"}
	if hasPath {
		spec.Path = "/" + rest
	}
	return spec, nil
}

func parseURL(value string) (Spec, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return Spec{}, fmt.Errorf("%q is not a URL: %w", value, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Spec{}, fmt.Errorf("%q: only http and https can be probed", value)
	}
	spec := Spec{Scheme: parsed.Scheme, Path: parsed.RequestURI()}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > maxPort {
			return Spec{}, fmt.Errorf("%q: %s is not a port", value, port)
		}
		spec.Port = number
	}
	return spec, nil
}

// Resolve turns a spec into the URL muelle should request, or reports that the
// container cannot be reached.
//
// The published port on the loopback comes first because it is the only route
// that works everywhere. The container's own address on the bridge network is
// the fallback, and it works only where the host shares a network namespace
// with the daemon — a Linux server, which is what muelle is for, but not
// Docker Desktop, where the bridge lives inside a VM.
func Resolve(spec Spec, container docker.Container, address string) (string, bool) {
	port := spec.Port
	if port == 0 {
		only, ok := onlyTCPPort(container)
		if !ok {
			return "", false
		}
		port = only
	}

	for _, published := range container.Ports {
		if published.PrivatePort == port && published.PublicPort != 0 {
			return spec.Scheme + "://" + net.JoinHostPort("127.0.0.1",
				strconv.Itoa(published.PublicPort)) + spec.Path, true
		}
	}
	if address == "" {
		return "", false
	}
	return spec.Scheme + "://" + net.JoinHostPort(address, strconv.Itoa(port)) + spec.Path, true
}

// onlyTCPPort returns the container's port when it has exactly one.
//
// Guessing between several would be right about as often as it was wrong. UDP
// ports are not counted: nothing there can answer an HTTP request.
func onlyTCPPort(container docker.Container) (int, bool) {
	found := 0
	seen := make(map[int]bool, len(container.Ports))
	for _, port := range container.Ports {
		if port.Type != "tcp" || seen[port.PrivatePort] {
			continue
		}
		seen[port.PrivatePort] = true
		found = port.PrivatePort
	}
	if len(seen) != 1 {
		return 0, false
	}
	return found, true
}

// Result is what a probe found.
type Result struct {
	// Healthy is true when the endpoint answered with a 2xx.
	Healthy bool
	// Status is the code it answered with, or zero when it did not answer.
	Status int
	// Err is why there was no answer: refused, timed out, no route.
	Err error
}

// Prober makes the requests.
type Prober struct {
	client *http.Client
}

// New builds a prober that gives up after timeout.
//
// Redirects are not followed. A health endpoint that redirects to a login page
// has not said it is well, and treating the 200 at the end of that chain as
// proof of health would be exactly wrong.
func New(timeout time.Duration) Prober {
	return Prober{client: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Do requests the URL and reports what came back.
//
// The request is a GET rather than a HEAD: a handler written for a browser or
// for Docker's own healthcheck may not implement HEAD, and answering 405 to a
// probe the service considers healthy would be a false alarm.
func (p Prober) Do(ctx context.Context, target string) Result {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{Err: err}
	}
	response, err := p.client.Do(request)
	if err != nil {
		return Result{Err: err}
	}
	defer response.Body.Close()

	return Result{
		Healthy: response.StatusCode >= 200 && response.StatusCode < 300,
		Status:  response.StatusCode,
	}
}
