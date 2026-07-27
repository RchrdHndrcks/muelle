package docker

import "testing"

func TestContainerNameStripsLeadingSlash(t *testing.T) {
	c := Container{ID: "abcdef1234567890", Names: []string{"/shop-mysql"}}

	if got := c.Name(); got != "shop-mysql" {
		t.Errorf("got %q, want %q", got, "shop-mysql")
	}
}

func TestContainerNameFallsBackToShortID(t *testing.T) {
	c := Container{ID: "abcdef1234567890"}

	if got := c.Name(); got != "abcdef123456" {
		t.Errorf("got %q, want the 12-char id", got)
	}
}

// The daemon reports IPv4 and IPv6 bindings of the same port separately, which
// would otherwise render as a duplicated, over-long column.
func TestPortSummaryCollapsesDuplicateBindings(t *testing.T) {
	c := Container{Ports: []Port{
		{IP: "0.0.0.0", PrivatePort: 3306, PublicPort: 3307, Type: "tcp"},
		{IP: "::", PrivatePort: 3306, PublicPort: 3307, Type: "tcp"},
	}}

	if got := c.PortSummary(); got != "3307->3306/tcp" {
		t.Errorf("got %q, want a single entry", got)
	}
}

func TestPortSummaryShowsUnpublishedPortsAlone(t *testing.T) {
	c := Container{Ports: []Port{{PrivatePort: 6379, Type: "tcp"}}}

	if got := c.PortSummary(); got != "6379/tcp" {
		t.Errorf("got %q, want %q", got, "6379/tcp")
	}
}

func TestPortSummaryEmptyWhenNoPorts(t *testing.T) {
	if got := (Container{}).PortSummary(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestEnvSplitsOnFirstSeparatorOnly(t *testing.T) {
	var d ContainerDetail
	d.Config.Env = []string{
		"MYSQL_ROOT_PASSWORD=p=ss=word",
		"PATH=/usr/bin",
		"MALFORMED",
	}

	env := d.Env()
	if got := env["MYSQL_ROOT_PASSWORD"]; got != "p=ss=word" {
		t.Errorf("got %q, want the value's own '=' preserved", got)
	}
	if _, ok := env["MALFORMED"]; ok {
		t.Error("entries without '=' should be skipped")
	}
}

func TestImageTagPrefersRealTagOverDangling(t *testing.T) {
	i := Image{RepoTags: []string{"<none>:<none>", "mysql:8.0"}}

	if got := i.Tag(); got != "mysql:8.0" {
		t.Errorf("got %q, want the real tag", got)
	}
	if i.Dangling() {
		t.Error("an image with a real tag is not dangling")
	}
}

func TestImageDanglingWhenNoTags(t *testing.T) {
	if !(Image{}).Dangling() {
		t.Error("an image with no repo tags is dangling")
	}
}

func TestImageShortIDStripsAlgorithmPrefix(t *testing.T) {
	i := Image{ID: "sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99f"}

	if got := i.ShortID(); got != "7dcddc01f13b" {
		t.Errorf("got %q, want %q", got, "7dcddc01f13b")
	}
}

func TestRunningDistinguishesPausedFromRunning(t *testing.T) {
	if (Container{State: "paused"}).Running() {
		t.Error("a paused container is not running")
	}
	if !(Container{State: "running"}).Running() {
		t.Error("a running container is running")
	}
}
