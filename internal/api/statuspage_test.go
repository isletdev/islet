package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/uptime"
)

func rendered(t *testing.T, view statusPageView) string {
	t.Helper()
	var b bytes.Buffer
	if err := statusTemplate.Execute(&b, view); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// The page is for people who are not the operator, so what it must never carry
// is what the operator monitors. A name is a name; a target is a URL somebody
// chose not to publish.
func TestTheStatusPageNeverPublishesWhatIsBeingProbed(t *testing.T) {
	st := uptime.PublicStatus{
		CheckedAt: "2026-09-30T10:00:00Z",
		Checks: []uptime.Summary{{
			Name: "Website", Status: "up", LastLatency: 42, Uptime30d: 99.98,
			Days: []uptime.Day{{Date: "2026-09-29", Up: 1440}},
		}},
	}
	html := rendered(t, statusView("Acme", "", st))
	if !strings.Contains(html, "Website") {
		t.Error("the check's name is not on the page")
	}
	for _, secret := range []string{"https://", "10.0.0", ":8080", "internal"} {
		if strings.Contains(html, secret) {
			t.Errorf("the page carries %q, which is a target rather than a name", secret)
		}
	}
	// Nothing to fetch and nothing to run: a page a stranger loads should ask
	// their browser for nothing at all.
	for _, tag := range []string{"<script", "src=", "fetch("} {
		if strings.Contains(html, tag) {
			t.Errorf("the page contains %q", tag)
		}
	}
}

// Colour is never the only thing that says a system is down.
func TestTheHeadlineSaysWhatIsWrongInWords(t *testing.T) {
	cases := []struct {
		down, checks int
		want         string
	}{
		{0, 2, "All systems operational"},
		{1, 2, "One system is down"},
		{3, 5, "3 systems are down"},
		{0, 0, "Nothing is being reported yet"},
	}
	for _, c := range cases {
		st := uptime.PublicStatus{Down: c.down}
		for i := 0; i < c.checks; i++ {
			status := "up"
			if i < c.down {
				status = "down"
			}
			st.Checks = append(st.Checks, uptime.Summary{Name: "check", Status: status})
		}
		v := statusView("Acme", "", st)
		if v.Headline != c.want {
			t.Errorf("%d down of %d: headline is %q, want %q", c.down, c.checks, v.Headline, c.want)
		}
		if got := rendered(t, v); !strings.Contains(got, c.want) {
			t.Errorf("the page does not say %q", c.want)
		}
	}
}

// A day with no data is a gap, not a failure: before a check existed, or while
// the daemon was off, is not downtime and must not be drawn as any.
func TestADayWithNoDataIsNotDrawnAsDowntime(t *testing.T) {
	st := uptime.PublicStatus{Checks: []uptime.Summary{{
		Name: "API", Status: "up",
		Days: []uptime.Day{{Date: "2026-09-01"}, {Date: "2026-09-02", Up: 100}, {Date: "2026-09-03", Up: 90, Down: 10}, {Date: "2026-09-04", Down: 50}},
	}}}
	v := statusView("Acme", "", st)
	got := []string{}
	for _, d := range v.Checks[0].Days {
		got = append(got, d.Class)
	}
	// 10 failures of 100 is the middle band: a fixed 55/45 split for any day
	// with one failure was the page overstating what it exists to report.
	want := []string{"none", "up", "partial-mid", "down"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d is %q, want %q", i, got[i], want[i])
		}
	}
	if !strings.Contains(v.Checks[0].Days[0].Title, "no data") {
		t.Errorf("a day with nothing in it reads as %q", v.Checks[0].Days[0].Title)
	}
	// A day that was 1% down and a day that was 90% down must not be drawn the
	// same.
	light := statusView("Acme", "", uptime.PublicStatus{Checks: []uptime.Summary{{
		Name: "API", Days: []uptime.Day{{Date: "2026-09-03", Up: 1417, Down: 24}},
	}}})
	if got := light.Checks[0].Days[0].Class; got != "partial-low" {
		t.Errorf("a day that was 1.7%% down is drawn as %q", got)
	}
	// And the history is in words as well, because thirty bars nine pixels wide
	// are not reachable on a phone and reach a screen reader as nothing.
	if !strings.Contains(light.Checks[0].Summary, "failures") {
		t.Errorf("the summary reads %q", light.Checks[0].Summary)
	}
	// And it counts in English rather than in "1 days".
	many := statusView("Acme", "", uptime.PublicStatus{Checks: []uptime.Summary{{
		Name: "API", Days: []uptime.Day{{Date: "2026-09-01", Up: 10}, {Date: "2026-09-02", Up: 10}, {Date: "2026-09-03", Up: 9, Down: 1}},
	}}})
	if got := many.Checks[0].Summary; got != "One day with failures, out of 3." {
		t.Errorf("the summary reads %q", got)
	}
	// The figure comes from the same days as the bars, so the two cannot
	// disagree: 190 of 250 probes answered.
	if !strings.HasPrefix(v.Checks[0].Uptime, "76.00%") {
		t.Errorf("the percentage does not match the bars: %q", v.Checks[0].Uptime)
	}
}

// A month with nothing in it has no percentage to claim. Saying 0.00% there is
// how a page that nobody has any data for announces a total outage.
func TestAMonthWithNoDataClaimsNoPercentage(t *testing.T) {
	st := uptime.PublicStatus{Checks: []uptime.Summary{{
		Name: "Brand new", Status: "unknown",
		Days: []uptime.Day{{Date: "2026-09-29"}, {Date: "2026-09-30"}},
	}}}
	v := statusView("Acme", "", st)
	if v.Checks[0].Uptime != "" {
		t.Errorf("an uptime figure was invented: %q", v.Checks[0].Uptime)
	}
	if got := rendered(t, v); strings.Contains(got, "0.00%") {
		t.Error("the page shows 0.00% for a check that has never run")
	}
	if v.Checks[0].Summary != "No history yet." {
		t.Errorf("the summary reads %q", v.Checks[0].Summary)
	}
}

// Three states, not two. A check that has never run is not down, and painting
// it red under a banner saying everything is operational is the page
// contradicting itself in two places at once.
func TestACheckThatHasNeverRunIsNotPaintedAsDown(t *testing.T) {
	st := uptime.PublicStatus{Checks: []uptime.Summary{
		{Name: "New thing", Status: "unknown"},
		{Name: "Website", Status: "up"},
	}}
	v := statusView("Acme", "", st)
	if v.Checks[0].Tone != "idle" {
		t.Errorf("a never-probed check is toned %q", v.Checks[0].Tone)
	}
	if v.Checks[1].Tone != "up" {
		t.Errorf("a working check is toned %q", v.Checks[1].Tone)
	}
	if v.Headline != "All systems operational" {
		t.Errorf("headline is %q", v.Headline)
	}
	if got := rendered(t, v); !strings.Contains(got, `class="state idle"`) {
		t.Error("the page has no third state to draw")
	}
}

// Whatever an operator types goes through a template that escapes it. A title
// is a company name, and a company name is user input like any other.
func TestTheTitleAndMessageAreEscaped(t *testing.T) {
	html := rendered(t, statusView(`Acme <script>alert(1)</script>`, `hi "there" & <b>`, uptime.PublicStatus{}))
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a title was rendered as markup")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("the title was not escaped at all")
	}
	if strings.Contains(html, "& <b>") {
		t.Error("the message was rendered as markup")
	}
}
