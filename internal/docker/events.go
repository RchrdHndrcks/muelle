package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Event is one entry from the daemon's event stream.
//
// The daemon copies the container's labels into Actor.Attributes, so an event
// says which Compose service it belongs to without anything having to be
// inspected. That is what makes it possible to follow a deployment: the
// containers involved are destroyed and created under different IDs, but they
// arrive under the same project and service throughout.
type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	TimeNano int64 `json:"timeNano"`
}

// Project returns the Compose project the container belongs to, or "".
func (e Event) Project() string { return e.Actor.Attributes[LabelProject] }

// Service returns the Compose service name, or "".
func (e Event) Service() string { return e.Actor.Attributes[LabelService] }

// Name returns the container's name.
func (e Event) Name() string { return e.Actor.Attributes["name"] }

// healthPrefix is how the daemon spells a health report: an action with an
// argument rather than a plain verb.
const healthPrefix = "health_status: "

// HealthStatus reports the healthcheck result an event carries, if it carries
// one. A start or a die does not.
func (e Event) HealthStatus() (Health, bool) {
	status, found := strings.CutPrefix(e.Action, healthPrefix)
	if !found {
		return HealthNone, false
	}
	switch Health(status) {
	case HealthHealthy:
		return HealthHealthy, true
	case HealthUnhealthy:
		return HealthUnhealthy, true
	case HealthStarting:
		return HealthStarting, true
	default:
		return HealthNone, false
	}
}

// eventBuffer is how many events can be waiting to be read.
//
// Generous, because the daemon emits a burst of them during a deployment and
// the reader is a UI that may be mid-frame. Full means the oldest are still
// delivered, just later; nothing is dropped.
const eventBuffer = 64

// EventStream is a live feed of container events.
type EventStream struct {
	events chan Event
	body   io.ReadCloser

	mu  sync.Mutex
	err error
}

// Events streams container lifecycle events from the daemon.
//
// The connection stays open until the context is cancelled or Close is called.
// Only container events are asked for: the daemon reports images, volumes and
// networks on the same feed, and filtering at the source costs one parameter.
func (c *Client) Events(ctx context.Context) (*EventStream, error) {
	query := url.Values{}
	query.Set("filters", `{"type":["container"]}`)

	body, err := c.do(ctx, http.MethodGet, "/events", query, nil)
	if err != nil {
		return nil, err
	}

	stream := &EventStream{events: make(chan Event, eventBuffer), body: body}
	go stream.read(ctx)
	return stream, nil
}

// read decodes the stream until it ends, then closes the channel.
func (s *EventStream) read(ctx context.Context) {
	defer close(s.events)
	defer s.body.Close()

	scanner := bufio.NewScanner(s.body)
	// Events are small, but an Actor with many labels is not tiny; the
	// default 64KiB line limit would truncate one and end the stream.
	scanner.Buffer(make([]byte, 0, 8<<10), 256<<10)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			// One malformed line is not a reason to stop watching. The
			// daemon has been known to emit fields muelle does not model,
			// and the next event is usually fine.
			continue
		}
		select {
		case s.events <- event:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}
}

// Events returns the channel events arrive on. It is closed when the stream
// ends.
func (s *EventStream) Events() <-chan Event { return s.events }

// Close ends the stream.
func (s *EventStream) Close() error { return s.body.Close() }

// Err reports why the stream ended, if it ended badly. A stream ended by its
// context reports nothing: that was the caller's doing.
func (s *EventStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
