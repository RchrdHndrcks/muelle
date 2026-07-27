// Package compose reconstructs Docker Compose projects from the containers
// the daemon reports, and builds the CLI commands that act on them.
//
// No YAML is parsed. Compose stamps each container it creates with labels
// naming the project, the service, the working directory and the config files,
// so grouping the container list by those labels recovers the project
// structure exactly as Compose sees it.
package compose

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Project is a Compose project and its containers.
type Project struct {
	Name string
	// WorkingDir is the directory Compose was invoked from. Empty for a
	// project discovered only on disk with no containers yet.
	WorkingDir string
	// ConfigFiles are the compose files that define the project.
	ConfigFiles []string
	// Containers belonging to the project, sorted by service name.
	Containers []docker.Container
	// OnDisk marks projects found by scanning directories rather than
	// derived from running containers.
	OnDisk bool
}

// Running counts the project's containers that are currently up.
func (p Project) Running() int {
	count := 0
	for _, c := range p.Containers {
		if c.Running() {
			count++
		}
	}
	return count
}

// Status summarises the project's state for display.
func (p Project) Status() string {
	switch {
	case len(p.Containers) == 0:
		return "stopped"
	case p.Running() == len(p.Containers):
		return "running"
	case p.Running() == 0:
		return "exited"
	default:
		return "partial"
	}
}

// Services returns the distinct service names in the project.
func (p Project) Services() []string {
	seen := make(map[string]bool)
	var services []string
	for _, c := range p.Containers {
		service := c.Service()
		if service == "" || seen[service] {
			continue
		}
		seen[service] = true
		services = append(services, service)
	}
	sort.Strings(services)
	return services
}

// FromContainers groups containers into projects by their Compose labels.
// Containers with no project label are ignored: they are standalone and belong
// in the container view, not this one.
func FromContainers(containers []docker.Container) []Project {
	byName := make(map[string]*Project)

	for _, container := range containers {
		name := container.Project()
		if name == "" {
			continue
		}
		project, ok := byName[name]
		if !ok {
			project = &Project{Name: name}
			byName[name] = project
		}
		project.Containers = append(project.Containers, container)

		// Every container of a project carries the same paths, but a
		// project can outlive an edit to its files, so take the first
		// non-empty value we see and keep it.
		if project.WorkingDir == "" {
			project.WorkingDir = container.Labels[docker.LabelWorkingDir]
		}
		if len(project.ConfigFiles) == 0 {
			if files := container.Labels[docker.LabelConfigFiles]; files != "" {
				project.ConfigFiles = splitConfigFiles(files)
			}
		}
	}

	projects := make([]Project, 0, len(byName))
	for _, project := range byName {
		sort.SliceStable(project.Containers, func(i, j int) bool {
			a, b := project.Containers[i], project.Containers[j]
			if a.Service() != b.Service() {
				return a.Service() < b.Service()
			}
			return a.Name() < b.Name()
		})
		projects = append(projects, *project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects
}

// composeFileNames are the file names Compose recognises, in the order it
// prefers them.
var composeFileNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// Discover scans directories for Compose projects that have no containers, so
// a fully stopped stack is still visible and can be started.
//
// Each root is scanned one level deep: the root itself, then each immediate
// subdirectory. That covers both a directory that is a project and a
// directory holding several projects, without walking an entire home
// directory.
func Discover(roots []string) []Project {
	var projects []Project
	seen := make(map[string]bool)

	for _, root := range roots {
		expanded, err := ExpandPath(root)
		if err != nil {
			continue
		}
		for _, dir := range append([]string{expanded}, subdirectories(expanded)...) {
			if seen[dir] {
				continue
			}
			file, ok := findComposeFile(dir)
			if !ok {
				continue
			}
			seen[dir] = true
			projects = append(projects, Project{
				Name:        filepath.Base(dir),
				WorkingDir:  dir,
				ConfigFiles: []string{file},
				OnDisk:      true,
			})
		}
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects
}

// Merge combines projects derived from containers with those found on disk.
//
// Label-derived projects win: they describe what the daemon actually has,
// including the exact config files Compose was invoked with, which may differ
// from what is on disk now. A disk project only contributes when no running
// project claims the name, and it can fill in a missing working directory.
func Merge(fromContainers, fromDisk []Project) []Project {
	merged := make([]Project, len(fromContainers))
	copy(merged, fromContainers)

	byName := make(map[string]int, len(merged))
	for i, project := range merged {
		byName[project.Name] = i
	}

	for _, disk := range fromDisk {
		index, exists := byName[disk.Name]
		if !exists {
			merged = append(merged, disk)
			continue
		}
		// Containers created outside a project directory (or by an older
		// Compose) can lack the path labels; the disk scan can supply
		// them.
		if merged[index].WorkingDir == "" {
			merged[index].WorkingDir = disk.WorkingDir
		}
		if len(merged[index].ConfigFiles) == 0 {
			merged[index].ConfigFiles = disk.ConfigFiles
		}
	}

	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	return merged
}

// splitConfigFiles parses the comma-separated config file label.
func splitConfigFiles(value string) []string {
	var files []string
	for _, file := range strings.Split(value, ",") {
		if file = strings.TrimSpace(file); file != "" {
			files = append(files, file)
		}
	}
	return files
}

// findComposeFile returns the Compose file in dir, if there is one.
func findComposeFile(dir string) (string, bool) {
	for _, name := range composeFileNames {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

// subdirectories lists the immediate subdirectories of dir, skipping hidden
// ones.
func subdirectories(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(dir, entry.Name()))
	}
	return dirs
}

// ExpandPath resolves a leading "~" to the user's home directory and returns
// an absolute path.
func ExpandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	return filepath.Abs(path)
}
