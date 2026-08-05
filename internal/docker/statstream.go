package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// StatsStreamer maintains one persistent stats stream per running container.
//
// The one-shot stats endpoint answers in about a second, because the daemon
// must gather two samples before it has a CPU delta to report — and it does
// that from scratch on every request. Held open instead, the same endpoint
// pushes a fresh sample every second with the previous one already embedded
// as its "precpu" block: the per-refresh latency disappears, the CPU maths is
// unchanged, and the daemon does strictly less work than being asked to start
// over each cycle.
//
// The streamer is not safe for concurrent use, deliberately: the app's event
// loop owns it the same way it owns the rest of the model, reconciling it
// against each refreshed container list. The goroutine behind each stream
// touches none of the streamer's state — it only decodes samples and hands
// them to report, which is expected to deliver them back to the owning
// goroutine (muelle posts them on the app's event channel).
type StatsStreamer struct {
	client *Client
	report func(id string, stat Stat)
	// streams is the handle per streamed container, keyed by container ID.
	streams map[string]*statStream
}

// statStream is what the streamer keeps per container: a way to stop the
// goroutine, and a way to notice that it has already stopped on its own.
type statStream struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewStatsStreamer creates a streamer that reports every decoded sample
// through the given callback. No streams are open until the first Sync.
func NewStatsStreamer(client *Client, report func(id string, stat Stat)) *StatsStreamer {
	return &StatsStreamer{
		client:  client,
		report:  report,
		streams: make(map[string]*statStream),
	}
}

// Sync reconciles the open streams against the containers currently running.
// Newly running containers get a stream, stopped or vanished ones lose
// theirs, and a stream that ended on its own — a daemon restart, a dropped
// proxy connection — is reopened, so one bad connection cannot freeze a
// column for the rest of the session.
//
// Every stream derives from ctx, so cancelling it ends all of them; that is
// what ties their lifetime to the app's.
func (m *StatsStreamer) Sync(ctx context.Context, running []string) {
	wanted := make(map[string]bool, len(running))
	for _, id := range running {
		wanted[id] = true
	}

	for id, stream := range m.streams {
		ended := false
		select {
		case <-stream.done:
			ended = true
		default:
		}
		if wanted[id] && !ended {
			continue
		}
		stream.cancel()
		delete(m.streams, id)
	}

	for _, id := range running {
		if _, open := m.streams[id]; open {
			continue
		}
		m.streams[id] = m.open(ctx, id)
	}
}

// Close ends every stream and waits for their goroutines to finish, so that
// after shutdown nothing is still decoding into the report callback. All the
// cancellations are issued before any waiting starts, so the teardown takes
// one connection's round trip rather than one per container.
func (m *StatsStreamer) Close() {
	for _, stream := range m.streams {
		stream.cancel()
	}
	for _, stream := range m.streams {
		<-stream.done
	}
	clear(m.streams)
}

// open starts one container's stream in its own goroutine.
func (m *StatsStreamer) open(ctx context.Context, id string) *statStream {
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &statStream{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		// Releases the context on the exit paths cancel never reaches:
		// a stream the daemon closed ended without anyone cancelling it.
		defer cancel()
		m.stream(streamCtx, id)
	}()
	return stream
}

// stream holds one container's stats connection open and reports each sample
// as it arrives.
//
// stream=true asks the daemon to push a sample every second for as long as
// the connection lasts. Every sample after the first carries its predecessor
// as the "precpu" block, which is exactly the baseline the CPU percentage
// needs; the first sample of a fresh stream has none, and Calculate reads
// that as "no delta yet" rather than inventing a figure.
//
// Cancelling ctx closes the underlying connection, which unblocks the decoder
// mid-read; the deferred Close releases it on every other exit path. Errors
// are not reported anywhere, deliberately: a stream dying because its
// container stopped is the normal end of its life, and one that died for any
// other reason is reopened by the next Sync.
func (m *StatsStreamer) stream(ctx context.Context, id string) {
	query := url.Values{"stream": {"true"}}
	body, err := m.client.do(ctx, http.MethodGet, "/containers/"+id+"/stats", query, nil)
	if err != nil {
		return
	}
	defer body.Close()

	decoder := json.NewDecoder(body)
	for {
		var sample StatSample
		if err := decoder.Decode(&sample); err != nil {
			return
		}
		// A sample can be mid-decode when the stream is cancelled; dropping
		// it here keeps a torn-down stream from reporting anything more.
		if ctx.Err() != nil {
			return
		}
		m.report(id, sample.Calculate())
	}
}
