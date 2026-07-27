package ui

import (
	"testing"
	"time"
)

func TestFormatBytesUsesBinaryUnits(t *testing.T) {
	cases := []struct {
		bytes uint64
		want  string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KiB"},
		{1536, "1.5KiB"},
		{1048576, "1.0MiB"},
		{402653184, "384MiB"},
		{1073741824, "1.0GiB"},
	}

	for _, tc := range cases {
		if got := FormatBytes(tc.bytes); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// Precision drops as magnitude grows so the column stays narrow.
func TestFormatBytesDropsDecimalAboveHundred(t *testing.T) {
	if got := FormatBytes(157286400); got != "150MiB" {
		t.Errorf("got %q, want %q", got, "150MiB")
	}
}

func TestFormatDurationUsesOneUnit(t *testing.T) {
	cases := []struct {
		duration time.Duration
		want     string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
		{800 * 24 * time.Hour, "2y"},
	}

	for _, tc := range cases {
		if got := FormatDuration(tc.duration); got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.duration, got, tc.want)
		}
	}
}

func TestFormatAgeHandlesMissingTimestamp(t *testing.T) {
	if got := FormatAge(0); got != "-" {
		t.Errorf("got %q, want %q for an absent timestamp", got, "-")
	}
}

func TestFormatAgeFromUnixTime(t *testing.T) {
	twoHoursAgo := time.Now().Add(-2 * time.Hour).Unix()

	if got := FormatAge(twoHoursAgo); got != "2h" {
		t.Errorf("got %q, want %q", got, "2h")
	}
}

// Stats arrive asynchronously, so a missing reading is normal rather than an
// error.
func TestFormatPercentMarksUnknownReadings(t *testing.T) {
	if got := FormatPercent(0, false); got != "-" {
		t.Errorf("got %q, want %q when there is no sample", got, "-")
	}
	if got := FormatPercent(12.34, true); got != "12.3%" {
		t.Errorf("got %q, want %q", got, "12.3%")
	}
}

func TestShortenImageDropsRegistryPrefixFirst(t *testing.T) {
	got := ShortenImage("registry.example.com/team/service:1.2.3", 20)

	if got != "service:1.2.3" {
		t.Errorf("got %q, want the registry and namespace dropped but the tag kept", got)
	}
}

func TestShortenImageLeavesShortReferenceAlone(t *testing.T) {
	if got := ShortenImage("mysql:8.0", 20); got != "mysql:8.0" {
		t.Errorf("got %q, want it unchanged", got)
	}
}

func TestShortenImageDropsDigestWhenStillTooLong(t *testing.T) {
	got := ShortenImage("service@sha256:abcdef0123456789abcdef0123456789", 20)

	if got != "service" {
		t.Errorf("got %q, want the unreadable digest dropped", got)
	}
}

func TestStateLabelShortensRunning(t *testing.T) {
	cases := map[string]string{
		"running":    "up",
		"exited":     "exited",
		"restarting": "restart",
		"paused":     "paused",
		"weird":      "weird",
	}

	for state, want := range cases {
		if got := StateLabel(state); got != want {
			t.Errorf("StateLabel(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestPluralAgreesWithCount(t *testing.T) {
	if got := Plural(1, "service", "services"); got != "1 service" {
		t.Errorf("got %q, want %q", got, "1 service")
	}
	if got := Plural(3, "service", "services"); got != "3 services" {
		t.Errorf("got %q, want %q", got, "3 services")
	}
	if got := Plural(0, "service", "services"); got != "0 services" {
		t.Errorf("got %q, want %q", got, "0 services")
	}
}
