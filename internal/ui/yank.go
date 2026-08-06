package ui

import (
	"strconv"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Yanking copies an identifier to the clipboard of the machine the user is
// sitting at, by way of an OSC 52 escape sequence the terminal itself acts
// on. That is what makes it work over SSH: muelle runs on the server, but
// the terminal — and therefore the clipboard the sequence reaches — is
// local. The alternative, shelling out to pbcopy or xclip, would copy to
// the remote host's clipboard, which is precisely the one nobody can paste
// from.

// yank asks the terminal to copy text, reporting what was copied in the
// status bar. "what" names the thing rather than repeating its value: the
// value is often a 64-character ID, and the row it came from is already on
// screen.
func (a *App) yank(text, what string) {
	if !a.screen.Copy(text) {
		// Nothing is listening for the sequence — the dump mode writes to
		// a pipe, not a terminal — so say so rather than claiming a copy
		// that never happened.
		a.setError("cannot copy: no terminal to emit to (-dump mode)")
		return
	}
	a.setStatus("copied %s", what)
}

// yankSelection copies the natural identifier of whatever the cursor is on:
// a container's full ID, an image's tag (or ID when it has none), and the
// name of a volume, network or Compose project.
func (a *App) yankSelection() {
	switch a.view {
	case ViewContainers:
		container, ok := a.selectedContainer()
		if !ok {
			return
		}
		// The full ID, not the 12-character one the list shows: every tool
		// that accepts the short form accepts the full form, and the
		// reverse does not hold for anything keyed on the exact ID.
		a.yank(container.ID, "container ID")
	case ViewCompose:
		project, ok := a.selectedProject()
		if !ok {
			return
		}
		a.yank(project.Name, "project name")
	case ViewImages:
		image, ok := a.selectedImage()
		if !ok {
			return
		}
		if image.Dangling() {
			// An untagged image has no name to copy, but its ID is what
			// removing or retagging it needs anyway.
			a.yank(image.ShortID(), "image ID")
			return
		}
		a.yank(image.Tag(), "image tag")
	case ViewVolumes:
		volume, ok := a.selectedVolume()
		if !ok {
			return
		}
		a.yank(volume.Name, "volume name")
	case ViewNetworks:
		network, ok := a.selectedNetwork()
		if !ok {
			return
		}
		a.yank(network.Name, "network name")
	}
}

// yankChoice is one entry in the copy menu: the text to place on the
// clipboard and the word for the status line.
type yankChoice struct {
	text string
	what string
}

// openYankMenu offers a container's other copyable identifiers. Plain "y"
// answers the common case with the full ID; the menu exists for the times a
// different handle is the one being pasted somewhere — a name into a shell
// command, an image into a compose file, a port into a browser.
func (a *App) openYankMenu(container docker.Container) {
	items := []MenuItem{
		{Label: "full ID", Detail: container.ID,
			Value: yankChoice{container.ID, "container ID"}},
		{Label: "name", Detail: container.Name(),
			Value: yankChoice{container.Name(), "name"}},
	}
	if container.Image != "" {
		items = append(items, MenuItem{Label: "image", Detail: container.Image,
			Value: yankChoice{container.Image, "image"}})
	}
	// Entries that do not apply are omitted rather than greyed out: a menu
	// choice that cannot be chosen is only something to explain.
	if hostPort, ok := firstPublishedPort(container); ok {
		items = append(items, MenuItem{Label: "published port", Detail: hostPort,
			Value: yankChoice{hostPort, "port " + hostPort}})
	}
	a.overlay = NewMenu("Copy from "+container.Name(), items, func(value any) {
		choice, ok := value.(yankChoice)
		if !ok {
			return
		}
		a.yank(choice.text, choice.what)
	})
}

// firstPublishedPort renders the container's first published port as a
// pasteable host:port. Docker reports the bind address, but "0.0.0.0" and
// "::" are listen-everywhere wildcards nothing can connect to by that name,
// so they become "localhost" — the address that actually reaches the port
// from the machine the daemon runs on, which over SSH is where the user's
// commands run too.
func firstPublishedPort(container docker.Container) (string, bool) {
	for _, port := range container.Ports {
		if port.PublicPort == 0 {
			continue
		}
		host := port.IP
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "localhost"
		}
		return host + ":" + strconv.Itoa(port.PublicPort), true
	}
	return "", false
}
