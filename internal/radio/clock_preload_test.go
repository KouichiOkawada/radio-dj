package radio

import (
	"strings"
	"testing"
	"time"
)

func TestClockAnnouncementHandlesMidnightNoonAndOtherHours(t *testing.T) {
	cases := []struct {
		hour int
		want string
	}{
		{0, "午前0時"},
		{12, "正午"},
		{18, "18時"},
	}
	for _, tc := range cases {
		got := clockAnnouncement(time.Date(2026, 9, 4, tc.hour, 37, 0, 0, time.Local))
		if !strings.Contains(got, tc.want) {
			t.Errorf("hour %d: %q does not contain %q", tc.hour, got, tc.want)
		}
	}
}

func TestClockHourKeyChangesOnlyAtHourBoundary(t *testing.T) {
	before := time.Date(2026, 9, 4, 11, 59, 59, 0, time.Local)
	after := before.Add(time.Second)
	if clockHourKey(before) == clockHourKey(after) {
		t.Fatal("hour key did not change across the top of the hour")
	}
	if clockHourKey(before) != clockHourKey(before.Add(-30*time.Minute)) {
		t.Fatal("hour key changed within one clock hour")
	}
}
