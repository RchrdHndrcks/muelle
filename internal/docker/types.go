package docker

import (
	"sort"
	"strconv"
	"strings"
)

// Compose labels stamped onto every container created by docker compose. They
// are the whole basis of the Compose view: grouping by them reconstructs a
// project without parsing any YAML.
const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelContainerNo = "com.docker.compose.container-number"
	// LabelConfigHash is the hash of the service configuration the container
	// was created from. Comparing it against what the files hash to now is
	// how Compose decides — and muelle predicts — what "up" will recreate.
	LabelConfigHash = "com.docker.compose.config-hash"
)

// Version is the subset of /version that we display.
type Version struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
	OS         string `json:"Os"`
	Arch       string `json:"Arch"`
}

// Port is a published or exposed container port.
type Port struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// Container is an entry from /containers/json.
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Command string            `json:"Command"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Ports   []Port            `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
}

// Name returns the container's primary name without the leading slash the API
// includes. Falls back to a short ID when a container somehow has no name.
func (c Container) Name() string {
	if len(c.Names) == 0 {
		return c.ShortID()
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// ShortID returns the conventional 12-character container ID.
func (c Container) ShortID() string {
	if len(c.ID) > 12 {
		return c.ID[:12]
	}
	return c.ID
}

// Project returns the compose project this container belongs to, or "" for
// containers started outside compose.
func (c Container) Project() string { return c.Labels[LabelProject] }

// Service returns the compose service name, or "" when not a compose
// container.
func (c Container) Service() string { return c.Labels[LabelService] }

// ConfigHash returns the hash of the service configuration this container was
// created from, or "" when Compose did not create it or was too old to stamp
// the label.
func (c Container) ConfigHash() string { return c.Labels[LabelConfigHash] }

// Running reports whether the container is currently executing. Docker's
// "running" state is distinct from "paused", "restarting", "created",
// "exited", "removing" and "dead".
func (c Container) Running() bool { return c.State == "running" }

// PortSummary renders published ports compactly, for example
// "3307->3306/tcp". Ports without a public binding are shown as
// "3306/tcp". Duplicate entries (the API reports IPv4 and IPv6 bindings
// separately) collapse into one.
func (c Container) PortSummary() string {
	seen := make(map[string]bool, len(c.Ports))
	var out []string
	for _, p := range c.Ports {
		var s string
		if p.PublicPort != 0 {
			s = strconv.Itoa(p.PublicPort) + "->" + strconv.Itoa(p.PrivatePort) + "/" + p.Type
		} else {
			s = strconv.Itoa(p.PrivatePort) + "/" + p.Type
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ContainerDetail is the subset of /containers/{id}/json we use. Config.Env in
// particular powers credential-aware quick commands.
type ContainerDetail struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Path    string `json:"Path"`
	Args    []string
	State   struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Hostname   string            `json:"Hostname"`
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		WorkingDir string            `json:"WorkingDir"`
		Labels     map[string]string `json:"Labels"`
		Tty        bool              `json:"Tty"`
	} `json:"Config"`
	// RestartCount is how many times the daemon has restarted this
	// container. It is only available from inspect, never from the list.
	RestartCount int `json:"RestartCount"`
	HostConfig   struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		Mode        string `json:"Mode"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
			Gateway   string `json:"Gateway"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Env returns the container's environment as a map. Values containing "=" are
// preserved intact (only the first separator splits).
func (d ContainerDetail) Env() map[string]string {
	env := make(map[string]string, len(d.Config.Env))
	for _, entry := range d.Config.Env {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		env[key] = value
	}
	return env
}

// Image is an entry from /images/json.
type Image struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     int64    `json:"Created"`
	Size        int64    `json:"Size"`
	// SharedSize is the portion of Size made up of layers other images also
	// use. It is -1 unless the request asked the daemon to compute it.
	SharedSize int64 `json:"SharedSize"`
	// Containers is how many containers reference the image, but the list
	// endpoint reports -1 for every image — the daemon only computes it for
	// /system/df. Usage is derived from the container list instead; see
	// ImageUsage.
	Containers int64 `json:"Containers"`
}

// Reclaimable returns roughly how much removing this image would free: its own
// layers, excluding those other images still need.
//
// Falls back to the full size when the daemon did not compute the shared
// portion, which overstates rather than understates — the honest direction for
// a figure offered before a deletion.
func (i Image) Reclaimable() int64 {
	if i.SharedSize < 0 {
		return i.Size
	}
	return i.Size - i.SharedSize
}

// ImageUsage counts how many of the given containers reference each image.
//
// The list endpoint reports Containers as -1 for every image, so this is the
// only way to know what is in use without the expensive /system/df call. It
// must be given every container, stopped ones included: an image referenced by
// a stopped container cannot be removed either, and treating it as unused
// would offer the user a deletion that fails.
func ImageUsage(containers []Container) map[string]int {
	usage := make(map[string]int, len(containers))
	for _, container := range containers {
		if container.ImageID != "" {
			usage[container.ImageID]++
		}
	}
	return usage
}

// Tag returns the image's display name: its first repo tag, or "<none>:<none>"
// for dangling images.
func (i Image) Tag() string {
	for _, t := range i.RepoTags {
		if t != "" && t != "<none>:<none>" {
			return t
		}
	}
	return "<none>:<none>"
}

// ShortID returns the image ID without the "sha256:" prefix, truncated.
func (i Image) ShortID() string {
	id := strings.TrimPrefix(i.ID, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Dangling reports whether the image has no usable tag, making it a candidate
// for pruning.
func (i Image) Dangling() bool { return i.Tag() == "<none>:<none>" }

// Volume is an entry from /volumes.
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
	Scope      string            `json:"Scope"`
	UsageData  *struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	} `json:"UsageData"`
}

// Project returns the compose project that created this volume, if any.
func (v Volume) Project() string { return v.Labels[LabelProject] }
