package ui

import (
	"os"
	"path/filepath"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// backupTimeFormat stamps each archive with when it was taken, ordered from
// year down to second so a directory of backups lists chronologically.
const backupTimeFormat = "20060102-150405"

// BackupFilename names the archive for a volume backed up at the given time.
func BackupFilename(volume string, at time.Time) string {
	return volume + "-" + at.Format(backupTimeFormat) + ".tgz"
}

// BackupArgv builds the docker run argv that archives a volume's contents.
//
// The volume is mounted read-only, because a backup must not be able to alter
// the thing it is preserving — tar never writes to its input, but the mount
// flag makes that a guarantee rather than a habit. The work runs in a
// short-lived alpine container since the daemon's volumes are not directly
// readable from the host: alpine is small, almost always already pulled, and
// carries a tar that understands gzip.
func BackupArgv(volume, backupDir, filename string) []string {
	return []string{
		"docker", "run", "--rm",
		"-v", volume + ":/data:ro",
		"-v", backupDir + ":/backup",
		"alpine", "tar", "czf", "/backup/" + filename,
		"-C", "/data", ".",
	}
}

// backupVolume asks for confirmation, then archives a volume into the
// configured backup directory.
func (a *App) backupVolume(volume docker.Volume) {
	// Same requirement as exec, reported the same way: the archive runs
	// through the docker CLI, not the API.
	if !a.dockerCLI {
		a.setError("%v", ErrDockerCLIMissing)
		return
	}
	configured := a.config.BackupDir
	if configured == "" {
		// A file that predates the setting, or sets it empty, should
		// behave like an absent one rather than resolving "" to the
		// working directory.
		configured = config.Default().BackupDir
	}
	dir, err := compose.ExpandPath(configured)
	if err != nil {
		a.setError("backup: %v", err)
		return
	}
	// The name is fixed now rather than when the command runs, so the path
	// the confirmation shows is exactly the file that gets written.
	filename := BackupFilename(volume.Name, time.Now())
	destination := filepath.Join(dir, filename)
	// Not marked destructive: a backup writes one new file and touches
	// nothing else, so Enter may confirm it.
	a.overlay = NewConfirm("Back up volume", "Write "+destination+"?", false, func(any) {
		a.runBackup(volume.Name, dir, filename)
	})
}

// runBackup suspends the TUI and runs the archive command, leaving its output
// on screen until the user acknowledges it — tar's complaints about files it
// could not read are worth the same look Compose output gets.
func (a *App) runBackup(volume, dir, filename string) {
	if a.runner == nil {
		a.setError("volume backup is unavailable in this mode")
		return
	}
	// The directory must exist before docker mounts it. Left to the daemon,
	// a missing path would be created root-owned, which is exactly the kind
	// of surprise a backup directory should not spring later.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.setError("backup: %v", err)
		return
	}
	if err := a.runner.Run(BackupArgv(volume, dir, filename), true); err != nil {
		a.setError("backup: %v", err)
		return
	}
	a.setStatus("backup written to %s", filepath.Join(dir, filename))
}
