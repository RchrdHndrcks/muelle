package docker

import (
	"context"
	"strings"
)

// HistoryEntry is one layer from /images/{id}/history — the record of a single
// build step and how much disk it added.
type HistoryEntry struct {
	ID string `json:"Id"`
	// Created is a Unix timestamp of when the layer was built.
	Created int64 `json:"Created"`
	// CreatedBy is the command that produced the layer, in whatever form the
	// builder recorded it. The classic builder wraps every Dockerfile
	// instruction in "/bin/sh -c" (with a "#(nop)" marker for metadata-only
	// steps); BuildKit records the instruction as written.
	CreatedBy string   `json:"CreatedBy"`
	Size      int64    `json:"Size"`
	Tags      []string `json:"Tags"`
	Comment   string   `json:"Comment"`
}

// Missing reports whether the layer's content is absent from this host, which
// is what the daemon means by the sentinel ID "<missing>": the layer was built
// elsewhere and only its metadata travelled with the image.
func (h HistoryEntry) Missing() bool {
	return h.ID == "" || strings.HasPrefix(h.ID, "<missing>")
}

// History returns an image's layer history, newest layer first — the order the
// daemon reports and the one "docker history" prints, which suits the question
// the caller is asking: the layers your own Dockerfile added sit on top of the
// base image's, and they are the ones you can do something about.
func (c *Client) History(ctx context.Context, id string) ([]HistoryEntry, error) {
	var entries []HistoryEntry
	if err := c.getJSON(ctx, "/images/"+id+"/history", nil, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
