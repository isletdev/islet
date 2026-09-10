package cron

import (
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"":              "Runs only when started by hand.",
		"* * * * *":     "Every minute, every day.",
		"*/5 * * * *":   "Every 5 minutes, every day.",
		"0 * * * *":     "Every hour, on the hour, every day.",
		"30 3 * * *":    "Every day at 03:30.",
		"0 3 * * 1-5":   "On weekdays at 03:00.",
		"0 3 * * 0":     "On Sunday at 03:00.",
		"0 4 1 * *":     "On day 1 of every month at 04:00.",
		"0 */6 * * *":   "Every 6 hours at minute 0, every day.",
		"15 8,20 * * *": "Every day at 08:15, 20:15.",
		"@daily":        "Every day at 00:00.",
		"0 0 25 12 *":   "On December 25 at 00:00.",
	}
	for spec, want := range cases {
		if got := Describe(spec); got != want {
			t.Errorf("%q: got %q want %q", spec, got, want)
		}
	}
}

func TestPreview(t *testing.T) {
	desc, next, err := Preview("0 3 * * *", "Europe/Skopje", 5)
	if err != nil || len(next) != 5 || desc == "" {
		t.Fatalf("preview: %v %d %q", err, len(next), desc)
	}
	if next[0].Hour() != 3 {
		t.Fatalf("next run hour = %d, want 3 in Skopje time", next[0].Hour())
	}
	if _, _, err := Preview("99 * * * *", "", 1); err == nil {
		t.Fatal("expected invalid schedule error")
	}
	if _, _, err := Preview("* * * * *", "Mars/Olympus", 1); err == nil {
		t.Fatal("expected timezone error")
	}
}

func TestParseCrontab(t *testing.T) {
	text := `# m h dom mon dow command
MAILTO=root
CRON_TZ=UTC
0 3 * * * /usr/local/bin/backup.sh --full  > /var/log/backup.log 2>&1
@hourly   curl -fsS https://example.com/ping
bad line here
*/10 * * * * echo "hi"
`
	jobs := ParseCrontab(text)
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs: %+v", len(jobs), jobs)
	}
	if jobs[0].Command != "/usr/local/bin/backup.sh --full  > /var/log/backup.log 2>&1" || jobs[0].Timezone != "UTC" || jobs[0].Enabled {
		t.Errorf("first job wrong: %+v", jobs[0])
	}
	if jobs[1].Schedule != "@hourly" || !strings.HasPrefix(jobs[1].Command, "curl") {
		t.Errorf("second job wrong: %+v", jobs[1])
	}
	if jobs[2].Command != `echo "hi"` {
		t.Errorf("third job wrong: %+v", jobs[2])
	}
}

func TestValidate(t *testing.T) {
	j := Job{Name: "x", Type: TypeCommand, Command: "true", Schedule: "0 3 * * *"}
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
	if j.Overlap != "skip" || j.NotifyOn != "failure" || j.TimeoutSec != 3600 {
		t.Fatalf("defaults not applied: %+v", j)
	}
	bad := []Job{
		{Name: "", Type: TypeCommand, Command: "true"},
		{Name: "x", Type: "weird"},
		{Name: "x", Type: TypeHTTP, Command: "ftp://x"},
		{Name: "x", Type: TypeFile, Command: "relative/path"},
		{Name: "x", Type: TypeHeartbeat},
		{Name: "x", Type: TypeCommand, Command: "true", RunAs: "Bad User"},
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("case %d should fail", i)
		}
	}
}
