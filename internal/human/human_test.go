package human

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m"},
		{time.Hour + 5*time.Minute, "1h 5m"},
		{48*time.Hour + 3*time.Hour, "2d 3h"},
		{-time.Minute, "0s"},
	}
	for _, tc := range cases {
		if got := Duration(tc.d); got != tc.want {
			t.Errorf("Duration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestInterval(t *testing.T) {
	if got := Interval(time.Hour); got != "1 hour" {
		t.Errorf("hour interval = %q", got)
	}
	if got := Interval(2 * time.Hour); got != "2 hours" {
		t.Errorf("two hours = %q", got)
	}
	if got := Interval(5 * time.Minute); got != "5 minutes" {
		t.Errorf("minutes = %q", got)
	}
}

func TestBytes(t *testing.T) {
	cases := map[int64]string{
		0:           "0 B",
		1023:        "1023 B",
		1024:        "1.0 KiB",
		1536:        "1.5 KiB",
		10 * 1024:   "10 KiB",
		1024 * 1024: "1.0 MiB",
		-1:          "unknown",
	}
	for n, want := range cases {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}
