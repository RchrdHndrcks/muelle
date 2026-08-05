package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamSample is a daemon stats frame with 4 CPUs, so a container delta of
// n against a system delta of 100 reads as n*4 percent.
func streamSample(prevCPU, curCPU, prevSys, curSys uint64) string {
	return fmt.Sprintf(`{
		"cpu_stats":    {"cpu_usage": {"total_usage": %d}, "system_cpu_usage": %d, "online_cpus": 4},
		"precpu_stats": {"cpu_usage": {"total_usage": %d}, "system_cpu_usage": %d},
		"memory_stats": {"usage": 2048, "limit": 4096}
	}`, curCPU, curSys, prevCPU, prevSys)
}

// firstStreamSample is the opening frame of a stream, which the daemon sends
// with an empty precpu block because there is no earlier sample yet.
const firstStreamSample = `{
	"cpu_stats":    {"cpu_usage": {"total_usage": 10}, "system_cpu_usage": 100, "online_cpus": 4},
	"precpu_stats": {"cpu_usage": {"total_usage": 0}, "system_cpu_usage": 0},
	"memory_stats": {"usage": 1024, "limit": 4096}
}`

// statsRecorder collects reported samples so tests can wait for them without
// racing the stream goroutines.
type statsRecorder struct {
	mu      sync.Mutex
	samples []Stat
	ids     []string
}

func (r *statsRecorder) report(id string, stat Stat) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, stat)
	r.ids = append(r.ids, id)
}

func (r *statsRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.samples)
}

func (r *statsRecorder) sample(i int) Stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.samples[i]
}

// waitFor polls until the condition holds or the deadline passes. The streams
// deliver over real connections, so tests wait on outcomes rather than sleep.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// containerID pulls the {id} out of /containers/{id}/stats.
func containerID(path string) string {
	trimmed := strings.TrimPrefix(path, "/containers/")
	return strings.TrimSuffix(trimmed, "/stats")
}

// A stream delivers samples as the daemon flushes them, and the CPU
// percentage comes from the precpu baseline each frame carries — the first
// frame of a stream has none, so it must read 0 rather than a made-up figure.
func TestStreamerReportsSamplesAsTheyArrive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stream") != "true" {
			t.Errorf("live streams must ask for stream=true, got query %q", r.URL.RawQuery)
		}
		flusher := w.(http.Flusher)
		// Flushing after every frame is what makes this a stream rather
		// than one response body: the client must decode each sample
		// before the next exists.
		for _, frame := range []string{firstStreamSample, streamSample(10, 12, 100, 200), streamSample(12, 16, 200, 300)} {
			fmt.Fprintln(w, frame)
			flusher.Flush()
		}
		// Hold the connection open like the daemon does; only the client
		// going away ends it.
		<-r.Context().Done()
	}))
	defer server.Close()

	recorder := &statsRecorder{}
	streamer := NewStatsStreamer(newForTest(server.URL), recorder.report)
	defer streamer.Close()

	streamer.Sync(context.Background(), []string{"abc123"})
	waitFor(t, "three samples", func() bool { return recorder.count() >= 3 })

	if got := recorder.sample(0).CPUPercent; got != 0 {
		t.Errorf("first sample has no precpu baseline, want 0%% CPU, got %v", got)
	}
	// Delta 2 over system delta 100, times 4 CPUs.
	if got := recorder.sample(1).CPUPercent; got != 8.0 {
		t.Errorf("got %v%% CPU, want 8%%", got)
	}
	// Delta 4 over system delta 100, times 4 CPUs.
	if got := recorder.sample(2).CPUPercent; got != 16.0 {
		t.Errorf("got %v%% CPU, want 16%%", got)
	}
	if got := recorder.sample(1).MemUsage; got != 2048 {
		t.Errorf("got memory %d, want 2048", got)
	}
}

// Sync must open streams for containers that appear and close them for
// containers that vanish, without touching the ones still running.
func TestSyncFollowsTheRunningSet(t *testing.T) {
	var (
		mu     sync.Mutex
		opened = map[string]int{}
		closed = map[string]int{}
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := containerID(r.URL.Path)
		mu.Lock()
		opened[id]++
		mu.Unlock()

		fmt.Fprintln(w, firstStreamSample)
		w.(http.Flusher).Flush()
		<-r.Context().Done()

		mu.Lock()
		closed[id]++
		mu.Unlock()
	}))
	defer server.Close()

	counts := func(m map[string]int, id string) int {
		mu.Lock()
		defer mu.Unlock()
		return m[id]
	}

	recorder := &statsRecorder{}
	streamer := NewStatsStreamer(newForTest(server.URL), recorder.report)
	defer streamer.Close()

	streamer.Sync(context.Background(), []string{"aaa", "bbb"})
	waitFor(t, "both streams to connect", func() bool {
		return counts(opened, "aaa") == 1 && counts(opened, "bbb") == 1
	})

	// bbb stops: its stream must be torn down, and aaa's left alone.
	streamer.Sync(context.Background(), []string{"aaa"})
	waitFor(t, "bbb's connection to close", func() bool { return counts(closed, "bbb") == 1 })
	if got := counts(closed, "aaa"); got != 0 {
		t.Errorf("aaa is still running, its stream should stay open (closed %d times)", got)
	}
	if got := counts(opened, "aaa"); got != 1 {
		t.Errorf("aaa was connected %d times, want its original stream kept", got)
	}
}

// A stream the daemon ends — a restart, a dropped proxy — must be reopened by
// the next Sync while the container still runs, or the column freezes on its
// last reading for the rest of the session.
func TestSyncReopensAStreamThatDied(t *testing.T) {
	var mu sync.Mutex
	opened := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		opened++
		mu.Unlock()
		// One sample, then the daemon hangs up.
		fmt.Fprintln(w, firstStreamSample)
	}))
	defer server.Close()

	recorder := &statsRecorder{}
	streamer := NewStatsStreamer(newForTest(server.URL), recorder.report)
	defer streamer.Close()

	streamer.Sync(context.Background(), []string{"aaa"})
	waitFor(t, "the first sample", func() bool { return recorder.count() >= 1 })

	// The goroutine notices the closed body and finishes; the next Sync sees
	// the ended stream and replaces it.
	waitFor(t, "the second connection", func() bool {
		streamer.Sync(context.Background(), []string{"aaa"})
		mu.Lock()
		defer mu.Unlock()
		return opened >= 2
	})
}

// Cancelling the context every stream derives from must end them all: that is
// what ties stream lifetime to the app's own shutdown.
func TestCancellingTheContextClosesEveryStream(t *testing.T) {
	var mu sync.Mutex
	closed := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, firstStreamSample)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		mu.Lock()
		closed++
		mu.Unlock()
	}))
	defer server.Close()

	recorder := &statsRecorder{}
	streamer := NewStatsStreamer(newForTest(server.URL), recorder.report)

	ctx, cancel := context.WithCancel(context.Background())
	streamer.Sync(ctx, []string{"aaa", "bbb", "ccc"})
	waitFor(t, "all streams to report", func() bool { return recorder.count() >= 3 })

	cancel()
	waitFor(t, "every connection to close", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return closed == 3
	})

	// Close afterwards must be safe and find nothing left to wait on for
	// long: the goroutines are already gone or on their way out.
	streamer.Close()
}

// Close blocks until the stream goroutines are gone, so nothing can still be
// decoding into the callback after the app has shut down.
func TestCloseWaitsForStreamsToFinish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, firstStreamSample)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	recorder := &statsRecorder{}
	streamer := NewStatsStreamer(newForTest(server.URL), recorder.report)

	streamer.Sync(context.Background(), []string{"aaa", "bbb"})
	waitFor(t, "both streams to report", func() bool { return recorder.count() >= 2 })

	streamer.Close()
	// After Close returns no more samples may arrive; the count must be
	// stable. Any straggler would also trip the race detector.
	settled := recorder.count()
	time.Sleep(20 * time.Millisecond)
	if got := recorder.count(); got != settled {
		t.Errorf("a sample arrived after Close: %d then %d", settled, got)
	}
}
