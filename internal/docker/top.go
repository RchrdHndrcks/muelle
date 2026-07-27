package docker

import (
	"context"
	"net/http"
	"net/url"
)

// Processes is the process table inside a container, the result of
// "docker top".
type Processes struct {
	// Titles are the column headings, which vary with the ps arguments
	// used, so callers must not assume a fixed set.
	Titles []string `json:"Titles"`
	// Rows holds one entry per process, aligned with Titles.
	Rows [][]string `json:"Processes"`
}

// Column returns the index of the named column, or -1 when the daemon did not
// report it. Column names depend on the ps flags and the host's ps
// implementation, so anything reading a specific column must handle absence.
func (p Processes) Column(title string) int {
	for i, t := range p.Titles {
		if t == title {
			return i
		}
	}
	return -1
}

// Top lists the processes running inside a container.
//
// psArgs is passed to the host's ps. Empty means the daemon's default, which
// is "-ef" on Linux; anything else risks differing between hosts.
//
// This only works on a running container: there is no process table for a
// stopped one, and the daemon returns an error rather than an empty list.
func (c *Client) Top(ctx context.Context, id, psArgs string) (Processes, error) {
	query := url.Values{}
	if psArgs != "" {
		query.Set("ps_args", psArgs)
	}
	body, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/top", query, nil)
	if err != nil {
		return Processes{}, err
	}
	defer body.Close()

	var processes Processes
	if err := decodeJSON(body, &processes); err != nil {
		return Processes{}, err
	}
	return processes, nil
}
