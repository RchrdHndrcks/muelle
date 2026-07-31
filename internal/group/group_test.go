package group

import (
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// A Compose project is the user having already said which containers belong
// together, so nothing muelle infers is allowed to override it.
func TestAComposeProjectIsAGroup(t *testing.T) {
	groups := Build([]docker.Container{
		named("shop-db", "shop", "db"),
		named("shop-api", "shop", "api"),
	})

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want one: %v", len(groups), names(groups))
	}
	if groups[0].Name != "shop" || groups[0].Source != FromCompose {
		t.Errorf("got %+v, want the compose project", groups[0])
	}
	if len(groups[0].Containers) != 2 {
		t.Errorf("got %d containers in the group", len(groups[0].Containers))
	}
}

// The case this feature exists for: containers started with "docker run" that
// are obviously one application, and that Docker itself knows nothing about.
func TestContainersSharingAPrefixAreAGroup(t *testing.T) {
	groups := Build([]docker.Container{
		standalone("shop-db"),
		standalone("shop-db-replica"),
	})

	if len(groups) != 1 {
		t.Fatalf("got %v, want them grouped under shop", names(groups))
	}
	if groups[0].Name != "shop" || groups[0].Source != FromPrefix {
		t.Errorf("got %+v, want the shared prefix", groups[0])
	}
}

// One container with a hyphen in its name is not an application. Without this
// rule a host full of one-off containers becomes a list of one-member groups,
// which is the flat list again with more lines in it.
func TestASingleContainerIsNotAGroup(t *testing.T) {
	groups := Build([]docker.Container{
		standalone("shop-db"),
		standalone("something-else"),
	})

	if len(groups) != 1 || groups[0].Source != Ungrouped {
		t.Fatalf("got %v, want both left ungrouped", names(groups))
	}
	if len(groups[0].Containers) != 2 {
		t.Errorf("got %d ungrouped, want both", len(groups[0].Containers))
	}
}

// Docker's own generated names use underscores, so they never share a prefix
// and fall into the ungrouped bucket without needing a rule of their own.
func TestGeneratedNamesDoNotFormGroups(t *testing.T) {
	groups := Build([]docker.Container{
		standalone("agitated_gagarin"),
		standalone("agitated_lovelace"),
	})

	if len(groups) != 1 || groups[0].Source != Ungrouped {
		t.Errorf("got %v, want them ungrouped", names(groups))
	}
}

// A standalone container named for an existing project belongs to it. This is
// how a database someone started by hand joins the stack it serves rather than
// becoming a second group with the same name.
func TestAPrefixJoinsTheComposeProjectOfTheSameName(t *testing.T) {
	groups := Build([]docker.Container{
		named("shop-db", "shop", "db"),
		named("shop-api", "shop", "api"),
		standalone("shop-cache"),
	})

	if len(groups) != 1 {
		t.Fatalf("got %v, want one group", names(groups))
	}
	if len(groups[0].Containers) != 3 || groups[0].Source != FromCompose {
		t.Errorf("got %+v, want the standalone container joined in", groups[0])
	}
}

// The ungrouped bucket is where the eye goes last, and on a working host it is
// mostly debris.
func TestUngroupedComesLast(t *testing.T) {
	groups := Build([]docker.Container{
		standalone("odd-one"),
		standalone("blog-web"),
		standalone("blog-cache"),
		named("shop-db", "shop", "db"),
		named("shop-api", "shop", "api"),
	})

	got := names(groups)
	want := []string{"blog", "shop", ungroupedName}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Nothing on the host is not an error, and neither is a host with nothing
// worth grouping.
func TestNoContainersProducesNoGroups(t *testing.T) {
	if groups := Build(nil); len(groups) != 0 {
		t.Errorf("got %v, want nothing", names(groups))
	}
}

// A container the daemon reports without a name still has to land somewhere.
func TestAnUnnamedContainerIsUngrouped(t *testing.T) {
	groups := Build([]docker.Container{{ID: "abcdef123456"}})

	if len(groups) != 1 || groups[0].Source != Ungrouped {
		t.Errorf("got %v, want it ungrouped", names(groups))
	}
}

func named(name, project, service string) docker.Container {
	return docker.Container{
		ID:    name,
		Names: []string{"/" + name},
		Labels: map[string]string{
			docker.LabelProject: project,
			docker.LabelService: service,
		},
	}
}

func standalone(name string) docker.Container {
	return docker.Container{ID: name, Names: []string{"/" + name}}
}

func names(groups []Group) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Name)
	}
	return out
}

// Under a heading that already says "shop", every row repeating it spends the
// widest column on the word the eye has just read.
func TestShortenDropsTheGroupsPrefix(t *testing.T) {
	cases := map[string]struct{ name, group, want string }{
		"compose service":       {"shop-db", "shop", "db"},
		"deeper name":           {"shop-db-replica", "shop", "db-replica"},
		"compose numbering":     {"shop-db-1", "shop", "db-1"},
		"not the group's name":  {"legacy-cart", "shop", "legacy-cart"},
		"name is the group":     {"shop", "shop", "shop"},
		"prefix without hyphen": {"shopfront", "shop", "shopfront"},
		"no group":              {"shop-db", "", "shop-db"},
	}

	for label, c := range cases {
		if got := Shorten(c.name, c.group); got != c.want {
			t.Errorf("%s: got %q, want %q", label, got, c.want)
		}
	}
}
