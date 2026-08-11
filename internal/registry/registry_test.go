package registry

import "testing"

func TestParseReferenceResolvesLikeTheDaemon(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Reference
	}{
		// Bare Hub names live under the implicit library namespace, and the
		// API answers on a different host than the reference names.
		{"bare name", "redis", Reference{Host: "registry-1.docker.io", Repo: "library/redis", Tag: "latest"}},
		{"bare name with tag", "redis:7-alpine", Reference{Host: "registry-1.docker.io", Repo: "library/redis", Tag: "7-alpine"}},
		{"hub namespace", "grafana/grafana:10.2", Reference{Host: "registry-1.docker.io", Repo: "grafana/grafana", Tag: "10.2"}},
		{"other registry", "ghcr.io/owner/app:v1", Reference{Host: "ghcr.io", Repo: "owner/app", Tag: "v1"}},
		{"registry with port", "localhost:5000/app", Reference{Host: "localhost:5000", Repo: "app", Tag: "latest"}},
		{"deep path", "my.registry.example/team/app:2", Reference{Host: "my.registry.example", Repo: "team/app", Tag: "2"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseReference(test.in)
			if err != nil {
				t.Fatalf("ParseReference(%q): %v", test.in, err)
			}
			if got != test.want {
				t.Errorf("ParseReference(%q) = %+v, want %+v", test.in, got, test.want)
			}
		})
	}
}

// A first segment that could be a Hub user must not be mistaken for a
// registry: "grafana/grafana" is on Docker Hub, not on a host called grafana.
func TestParseReferenceOnlyTreatsUnambiguousHostsAsHosts(t *testing.T) {
	got, err := ParseReference("grafana/grafana")
	if err != nil {
		t.Fatalf("ParseReference: %v", err)
	}
	if got.Host != "registry-1.docker.io" || got.Repo != "grafana/grafana" {
		t.Errorf("got %+v, want a Docker Hub reference", got)
	}
}

func TestParseReferenceRejectsWhatCannotBeChecked(t *testing.T) {
	for _, in := range []string{
		"",
		"<none>:<none>",
		// Pinned by digest already; there is no tag to have moved.
		"redis@sha256:0123456789abcdef",
		"ghcr.io/",
	} {
		if ref, err := ParseReference(in); err == nil {
			t.Errorf("ParseReference(%q) = %+v, want an error", in, ref)
		}
	}
}
