package docker

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// Containers lists containers. When all is false only running containers are
// returned, matching "docker ps" versus "docker ps -a".
//
// Results are sorted by compose project then name, so the list is stable
// across refreshes: the daemon's ordering is by creation time, which makes the
// selection cursor appear to jump when containers restart.
func (c *Client) Containers(ctx context.Context, all bool) ([]Container, error) {
	query := url.Values{}
	if all {
		query.Set("all", "1")
	}
	var containers []Container
	if err := c.getJSON(ctx, "/containers/json", query, &containers); err != nil {
		return nil, err
	}
	sort.SliceStable(containers, func(i, j int) bool {
		a, b := containers[i], containers[j]
		if a.Project() != b.Project() {
			// Standalone containers (no project) sort last.
			if a.Project() == "" {
				return false
			}
			if b.Project() == "" {
				return true
			}
			return a.Project() < b.Project()
		}
		return a.Name() < b.Name()
	})
	return containers, nil
}

// Inspect returns the full detail for one container.
func (c *Client) Inspect(ctx context.Context, id string) (ContainerDetail, error) {
	var detail ContainerDetail
	err := c.getJSON(ctx, "/containers/"+id+"/json", nil, &detail)
	return detail, err
}

// InspectRaw returns the container's inspect payload as indented JSON, for the
// inspect pager. Re-encoding through a generic map rather than reusing
// ContainerDetail keeps every field the daemon reports, not just the ones we
// model.
func (c *Client) InspectRaw(ctx context.Context, id string) ([]byte, error) {
	var payload any
	if err := c.getJSON(ctx, "/containers/"+id+"/json", nil, &payload); err != nil {
		return nil, err
	}
	return marshalIndent(payload)
}

// Start starts a stopped container.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+id+"/start", nil, nil)
}

// Stop stops a running container, allowing it timeout seconds to exit before
// it is killed.
func (c *Client) Stop(ctx context.Context, id string, timeout int) error {
	query := url.Values{"t": {strconv.Itoa(timeout)}}
	return c.post(ctx, "/containers/"+id+"/stop", query, nil)
}

// Restart restarts a container, allowing it timeout seconds to exit first.
func (c *Client) Restart(ctx context.Context, id string, timeout int) error {
	query := url.Values{"t": {strconv.Itoa(timeout)}}
	return c.post(ctx, "/containers/"+id+"/restart", query, nil)
}

// Kill sends SIGKILL to a container.
func (c *Client) Kill(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+id+"/kill", nil, nil)
}

// Pause suspends all processes in a container via the freezer cgroup.
func (c *Client) Pause(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+id+"/pause", nil, nil)
}

// Unpause resumes a paused container.
func (c *Client) Unpause(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+id+"/unpause", nil, nil)
}

// Remove deletes a container. With force it is killed first; with volumes its
// anonymous volumes are deleted too.
func (c *Client) Remove(ctx context.Context, id string, force, volumes bool) error {
	query := url.Values{}
	if force {
		query.Set("force", "1")
	}
	if volumes {
		query.Set("v", "1")
	}
	body, err := c.do(ctx, http.MethodDelete, "/containers/"+id, query, nil)
	if err != nil {
		return err
	}
	return body.Close()
}

// Images lists all images, newest first.
//
// shared-size asks the daemon to compute how much of each image's size is
// layers other images also use, which is what makes a per-image "this would
// free X" figure meaningful. It costs the daemon a walk of the layer graph, so
// it is requested only here, where the images view is on screen.
func (c *Client) Images(ctx context.Context) ([]Image, error) {
	query := url.Values{"shared-size": {"true"}}
	var images []Image
	if err := c.getJSON(ctx, "/images/json", query, &images); err != nil {
		return nil, err
	}
	sort.SliceStable(images, func(i, j int) bool {
		return images[i].Created > images[j].Created
	})
	return images, nil
}

// ImageID resolves an image reference — typically a tag like "shop/api:1.4" —
// to the ID it currently names.
//
// This is what makes "did the pull change anything" answerable: a running
// container records the ID it was created from, and after a pull the same tag
// may resolve to a different ID. Comparing the two is exact, where comparing
// pull output would mean parsing text Compose does not promise to keep stable.
func (c *Client) ImageID(ctx context.Context, reference string) (string, error) {
	var payload struct {
		ID string `json:"Id"`
	}
	if err := c.getJSON(ctx, "/images/"+reference+"/json", nil, &payload); err != nil {
		return "", err
	}
	return payload.ID, nil
}

// RemoveImage deletes an image, optionally forcing removal when containers
// still reference it.
func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	query := url.Values{}
	if force {
		query.Set("force", "1")
	}
	body, err := c.do(ctx, http.MethodDelete, "/images/"+id, query, nil)
	if err != nil {
		return err
	}
	return body.Close()
}

// PruneResult reports what a prune call reclaimed.
type PruneResult struct {
	SpaceReclaimed int64 `json:"SpaceReclaimed"`
	// Deleted counts removed objects. The daemon uses a different field
	// name per endpoint, so callers fill this in from whichever list the
	// response carried.
	Deleted int
}

// PruneImages removes dangling images.
func (c *Client) PruneImages(ctx context.Context) (PruneResult, error) {
	var payload struct {
		ImagesDeleted []struct {
			Deleted  string `json:"Deleted"`
			Untagged string `json:"Untagged"`
		} `json:"ImagesDeleted"`
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	body, err := c.do(ctx, http.MethodPost, "/images/prune", nil, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{SpaceReclaimed: payload.SpaceReclaimed, Deleted: len(payload.ImagesDeleted)}, nil
}

// Volumes lists volumes, sorted by name.
func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	var payload struct {
		Volumes []Volume `json:"Volumes"`
	}
	if err := c.getJSON(ctx, "/volumes", nil, &payload); err != nil {
		return nil, err
	}
	sort.SliceStable(payload.Volumes, func(i, j int) bool {
		return payload.Volumes[i].Name < payload.Volumes[j].Name
	})
	return payload.Volumes, nil
}

// RemoveVolume deletes a volume.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	query := url.Values{}
	if force {
		query.Set("force", "1")
	}
	body, err := c.do(ctx, http.MethodDelete, "/volumes/"+name, query, nil)
	if err != nil {
		return err
	}
	return body.Close()
}

// PruneVolumes removes volumes not referenced by any container.
func (c *Client) PruneVolumes(ctx context.Context) (PruneResult, error) {
	var payload struct {
		VolumesDeleted []string `json:"VolumesDeleted"`
		SpaceReclaimed int64    `json:"SpaceReclaimed"`
	}
	body, err := c.do(ctx, http.MethodPost, "/volumes/prune", nil, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{SpaceReclaimed: payload.SpaceReclaimed, Deleted: len(payload.VolumesDeleted)}, nil
}
