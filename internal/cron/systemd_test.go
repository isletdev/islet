package cron

import "testing"

func TestCalendarToCron(t *testing.T) {
	cases := map[string]string{
		"daily":                   "0 0 * * *",
		"hourly":                  "0 * * * *",
		"weekly":                  "0 0 * * 1",
		"*-*-* 03:00:00":          "0 3 * * *",
		"*-*-* 04:30":             "30 4 * * *",
		"Mon..Fri *-*-* 08:15:00": "15 8 * * 1-5",
		"Sun 02:00":               "0 2 * * 0",
		"Mon,Thu *-*-* 06:00:00":  "0 6 * * 1,4",
		"*-*-01 05:00:00":         "0 5 1 * *",
		"2026-01-01 00:00:00":     "",
		"*-*-* *:00:00":           "",
	}
	for in, want := range cases {
		got, ok := CalendarToCron(in)
		if (want == "") == ok || got != want {
			t.Errorf("%q: got %q ok=%v want %q", in, got, ok, want)
		}
	}
}

func TestParseTimer(t *testing.T) {
	timer := "[Unit]\nDescription=Nightly cleanup\n[Timer]\nOnCalendar=*-*-* 03:00:00\nPersistent=true\n[Install]\nWantedBy=timers.target\n"
	service := "[Service]\nType=oneshot\nExecStart=-/usr/local/bin/cleanup.sh --all\n"
	j, ok := ParseTimer("cleanup", timer, service)
	if !ok || j.Schedule != "0 3 * * *" || j.Command != "/usr/local/bin/cleanup.sh --all" || j.Name != "Nightly cleanup" || j.Enabled {
		t.Fatalf("got ok=%v %+v", ok, j)
	}
}
