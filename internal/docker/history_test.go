package docker

import (
	"context"
	"net/http"
	"testing"
)

func TestHistoryDecodesLayersInDaemonOrder(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"Id":"sha256:top","Created":1722000000,"CreatedBy":"/bin/sh -c #(nop)  CMD [\"mysqld\"]","Size":0,"Tags":["mysql:8.0"],"Comment":""},
			{"Id":"<missing>","Created":1721000000,"CreatedBy":"/bin/sh -c apt-get update","Size":104857600,"Tags":null,"Comment":"buildkit.dockerfile.v0"}
		]`))
	})

	entries, err := client.History(context.Background(), "sha256:abc")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	if got := requested.First(); got != "/images/sha256:abc/history" {
		t.Errorf("got request %q, want the history endpoint for the image", got)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// The daemon reports newest first and the order must survive decoding:
	// it is what puts the caller's own layers on top of the base image's.
	if entries[0].CreatedBy != `/bin/sh -c #(nop)  CMD ["mysqld"]` {
		t.Errorf("got %q first, want the newest layer", entries[0].CreatedBy)
	}
	if entries[1].Size != 104857600 {
		t.Errorf("got size %d, want the layer's byte count", entries[1].Size)
	}
	if entries[1].Comment != "buildkit.dockerfile.v0" {
		t.Errorf("got comment %q, want it decoded", entries[1].Comment)
	}
}

// Layers built elsewhere travel with only their metadata, which the daemon
// reports with the sentinel ID "<missing>".
func TestHistoryEntryReportsMissingLayers(t *testing.T) {
	if !(HistoryEntry{ID: "<missing>"}).Missing() {
		t.Error("the daemon's sentinel ID should read as missing")
	}
	if !(HistoryEntry{}).Missing() {
		t.Error("an absent ID should read as missing too")
	}
	if (HistoryEntry{ID: "sha256:abc"}).Missing() {
		t.Error("a real layer ID should not read as missing")
	}
}

func TestHistoryReportsDaemonErrorForUnknownImage(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"no such image: nope"}`))
	})

	_, err := client.History(context.Background(), "nope")

	if err == nil {
		t.Fatal("expected an error for an image the daemon does not have")
	}
}
