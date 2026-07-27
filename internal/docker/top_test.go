package docker

import (
	"context"
	"net/http"
	"testing"
)

func TestTopDecodesProcessTable(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"Titles": ["UID","PID","PPID","C","STIME","TTY","TIME","CMD"],
			"Processes": [
				["999","1722060","1722040","0","Jul15","?","02:52:32","mysqld"],
				["999","1722100","1722060","0","Jul15","?","00:00:01","mysqld --daemon"]
			]
		}`))
	})

	processes, err := client.Top(context.Background(), "abc", "")
	if err != nil {
		t.Fatalf("Top: %v", err)
	}

	if len(processes.Titles) != 8 {
		t.Errorf("got %d titles, want 8", len(processes.Titles))
	}
	if len(processes.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(processes.Rows))
	}
	if processes.Rows[0][7] != "mysqld" {
		t.Errorf("got %q, want the command", processes.Rows[0][7])
	}
	if got := requested.First(); got != "/containers/abc/top" {
		t.Errorf("got request %q, want no ps_args by default", got)
	}
}

func TestTopPassesPsArgsWhenGiven(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Titles":[],"Processes":[]}`))
	})

	if _, err := client.Top(context.Background(), "abc", "aux"); err != nil {
		t.Fatalf("Top: %v", err)
	}
	if got := requested.First(); got != "/containers/abc/top?ps_args=aux" {
		t.Errorf("got %q, want the ps arguments forwarded", got)
	}
}

// Column names depend on the ps flags and the host's ps, so anything reading a
// specific column has to handle absence.
func TestColumnLocatesTitlesAndReportsAbsence(t *testing.T) {
	processes := Processes{Titles: []string{"PID", "USER", "COMMAND"}}

	if got := processes.Column("COMMAND"); got != 2 {
		t.Errorf("got %d, want index 2", got)
	}
	if got := processes.Column("CMD"); got != -1 {
		t.Errorf("got %d, want -1 for a column the daemon did not report", got)
	}
}

func TestTopReportsDaemonErrorForStoppedContainer(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"container abc is not running"}`))
	})

	_, err := client.Top(context.Background(), "abc", "")

	if err == nil {
		t.Fatal("expected an error for a stopped container")
	}
}
