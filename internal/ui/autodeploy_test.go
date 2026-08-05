package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/autodeploy"
	"github.com/RchrdHndrcks/muelle/internal/config"
)

// W must enrol the selected project and persist the change, because the
// daemon that acts on it is a separate process reading the same file.
func TestWTogglesAutoDeployAndPersists(t *testing.T) {
	app := loadedApp(t)
	stored := config.Default()
	app.SetPreferenceWriter(func(change func(*config.Config)) error {
		change(&stored)
		return nil
	})
	app.SetView(ViewCompose)

	press(app, runeKey('W'))

	if !app.config.AutoDeploy.Enabled("shop") {
		t.Error("W should enrol the selected project in memory")
	}
	if !stored.AutoDeploy.Enabled("shop") {
		t.Error("W should persist the enrolment for the daemon to read")
	}

	press(app, runeKey('W'))

	if app.config.AutoDeploy.Enabled("shop") || stored.AutoDeploy.Enabled("shop") {
		t.Error("a second W should withdraw the project everywhere")
	}
}

// The AUTO column must say nothing for a project that is not enrolled: a
// marker that appears on everything marks nothing.
func TestComposeViewOmitsAutoMarkerWhenNotEnrolled(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewCompose)

	if frame := frameText(app); strings.Contains(frame, "auto ") {
		t.Errorf("frame carries an auto marker with nothing enrolled:\n%s", frame)
	}
}

// An enrolled project the daemon has not reported on yet shows a bare marker:
// there is no outcome to celebrate or worry about.
func TestComposeViewMarksEnrolledProject(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewCompose)
	app.config.AutoDeploy.SetEnabled("shop", true)

	if frame := frameText(app); !strings.Contains(frame, "auto") {
		t.Errorf("frame is missing the enrolment marker:\n%s", frame)
	}
}

// Once the daemon has reported, the column carries its verdict and how old it
// is — the two facts that say whether the automation is working.
func TestComposeViewShowsLastOutcomeAndAge(t *testing.T) {
	cases := map[string]struct {
		outcome autodeploy.Outcome
		want    string
	}{
		"success": {
			outcome: autodeploy.Outcome{
				Time:    time.Now().Add(-12 * time.Minute),
				Project: "shop",
				Action:  autodeploy.ActionDeploy,
			},
			want: "auto ok 12m",
		},
		"failure": {
			outcome: autodeploy.Outcome{
				Time:    time.Now().Add(-3 * time.Minute),
				Project: "shop",
				Action:  autodeploy.ActionNone,
				Error:   "pull: exit status 1",
			},
			want: "auto fail 3m",
		},
		"fresh": {
			outcome: autodeploy.Outcome{
				Time:    time.Now(),
				Project: "shop",
				Action:  autodeploy.ActionNone,
			},
			want: "auto ok now",
		},
	}

	for name, c := range cases {
		app := loadedApp(t)
		app.SetView(ViewCompose)
		app.config.AutoDeploy.SetEnabled("shop", true)
		deployStateLoaded{state: autodeploy.State{
			Projects: map[string]autodeploy.Outcome{"shop": c.outcome},
		}}.apply(app)

		if frame := frameText(app); !strings.Contains(frame, c.want) {
			t.Errorf("%s: frame is missing %q:\n%s", name, c.want, frame)
		}
	}
}

// The state file belongs to another process and can be missing or torn at any
// moment; the view must shrug, not error.
func TestDeployStateRefreshToleratesAMissingFile(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewCompose)
	app.config.AutoDeploy.SetEnabled("shop", true)
	app.SetDeployStatePath(t.TempDir() + "/absent.json")

	deployStateLoaded{state: autodeploy.LoadState(app.deployStatePath)}.apply(app)

	if frame := frameText(app); !strings.Contains(frame, "auto") {
		t.Errorf("an unreadable state file must still leave the enrolment visible:\n%s", frame)
	}
}
