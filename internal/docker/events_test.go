package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// The daemon streams one JSON object per line and never closes the connection,
// which is what makes it a stream rather than a list.
func TestEventsYieldsEachLineAsItArrives(t *testing.T) {
	client, _ := eventServer(t,
		`{"Type":"container","Action":"die","Actor":{"ID":"abc","Attributes":{"name":"shop-api"}},"timeNano":1}`,
		`{"Type":"container","Action":"start","Actor":{"ID":"def","Attributes":{"name":"shop-api"}},"timeNano":2}`,
	)

	stream, err := client.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	first, second := next(t, stream), next(t, stream)

	if first.Action != "die" || first.Actor.ID != "abc" {
		t.Errorf("got %+v, want the die event", first)
	}
	if second.Action != "start" || second.Actor.ID != "def" {
		t.Errorf("got %+v, want the start event", second)
	}
}

// Compose stamps its labels on the container, and the daemon copies them into
// the event's attributes. That is what makes it possible to know which service
// is being replaced without inspecting anything.
func TestEventsCarriesTheComposeLabels(t *testing.T) {
	// One line: the daemon's feed is newline-delimited JSON, so an event
	// split across lines is not an event.
	client, _ := eventServer(t, `{"Type":"container","Action":"start","Actor":{"ID":"abc",`+
		`"Attributes":{"name":"shop-api-1","com.docker.compose.project":"shop",`+
		`"com.docker.compose.service":"api"}},"timeNano":1}`)

	stream, err := client.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	event := next(t, stream)

	if event.Project() != "shop" || event.Service() != "api" {
		t.Errorf("got project %q service %q, want shop/api", event.Project(), event.Service())
	}
	if event.Name() != "shop-api-1" {
		t.Errorf("got name %q", event.Name())
	}
}

// Health arrives as "health_status: healthy", which is an action with an
// argument rather than a plain verb.
func TestEventsReadsHealthStatus(t *testing.T) {
	client, _ := eventServer(t,
		`{"Type":"container","Action":"health_status: healthy","Actor":{"ID":"abc"},"timeNano":1}`,
		`{"Type":"container","Action":"health_status: unhealthy","Actor":{"ID":"abc"},"timeNano":2}`,
	)

	stream, err := client.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	healthy, unhealthy := next(t, stream), next(t, stream)

	if status, ok := healthy.HealthStatus(); !ok || status != HealthHealthy {
		t.Errorf("got %q (ok=%v), want healthy", status, ok)
	}
	if status, ok := unhealthy.HealthStatus(); !ok || status != HealthUnhealthy {
		t.Errorf("got %q (ok=%v), want unhealthy", status, ok)
	}
}

// A plain lifecycle event is not a health report, and must not be mistaken for
// one.
func TestEventsReportsNoHealthForOtherActions(t *testing.T) {
	client, _ := eventServer(t, `{"Type":"container","Action":"start","Actor":{"ID":"abc"},"timeNano":1}`)

	stream, err := client.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if _, ok := next(t, stream).HealthStatus(); ok {
		t.Error("a start is not a health report")
	}
}

// The daemon emits events for images, volumes and networks too. Asking it to
// filter is one flag and saves decoding everything that happens on a busy host.
func TestEventsAsksTheDaemonForContainersOnly(t *testing.T) {
	client, requested := eventServer(t, `{"Type":"container","Action":"start","Actor":{"ID":"a"},"timeNano":1}`)

	stream, err := client.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	next(t, stream)

	if filters := (<-requested).Get("filters"); filters != `{"type":["container"]}` {
		t.Errorf("got filters %q, want containers only", filters)
	}
}

// Closing the stream must end it, so switching off the feature or quitting does
// not leave a request open against the daemon.
func TestEventsStopsWhenTheContextIsCancelled(t *testing.T) {
	client, _ := eventServer(t, `{"Type":"container","Action":"start","Actor":{"ID":"a"},"timeNano":1}`)
	ctx, cancel := context.WithCancel(context.Background())

	stream, err := client.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next(t, stream)
	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-stream.Events():
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the stream stayed open after its context was cancelled")
		}
	}
}

// next reads one event or fails the test.
func next(t *testing.T, stream *EventStream) Event {
	t.Helper()
	select {
	case event, open := <-stream.Events():
		if !open {
			t.Fatal("the stream closed early")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}
	}
}

// eventServer serves the given lines and then holds the connection open, the
// way the daemon does. The channel reports the query it was asked for.
func eventServer(t *testing.T, lines ...string) (*Client, chan url.Values) {
	t.Helper()
	requested := make(chan url.Values, 1)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- r.URL.Query():
		default:
		}
		flusher, _ := w.(http.Flusher)
		for _, line := range lines {
			w.Write([]byte(line + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		// The daemon does not end the stream, and neither does this.
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client, requested
}
