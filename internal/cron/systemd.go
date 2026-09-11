package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	calendarRe = regexp.MustCompile(`(?m)^\s*OnCalendar\s*=\s*(.+)$`)
	execRe     = regexp.MustCompile(`(?m)^\s*ExecStart\s*=\s*(.+)$`)
	unitRe     = regexp.MustCompile(`(?m)^\s*Unit\s*=\s*(.+)$`)
	descRe     = regexp.MustCompile(`(?m)^\s*Description\s*=\s*(.+)$`)
	timeRe     = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::\d{2})?$`)
	dowNames   = map[string]string{"mon": "1", "tue": "2", "wed": "3", "thu": "4", "fri": "5", "sat": "6", "sun": "0"}
)

// CalendarToCron converts the common systemd OnCalendar forms to a five
// field cron expression. Returns ok=false for forms cron cannot express.
func CalendarToCron(cal string) (string, bool) {
	cal = strings.TrimSpace(strings.ToLower(cal))
	switch cal {
	case "minutely":
		return "* * * * *", true
	case "hourly":
		return "0 * * * *", true
	case "daily", "midnight":
		return "0 0 * * *", true
	case "weekly":
		return "0 0 * * 1", true
	case "monthly":
		return "0 0 1 * *", true
	case "yearly", "annually":
		return "0 0 1 1 *", true
	case "quarterly":
		return "0 0 1 1,4,7,10 *", true
	}
	// Forms: [DOW[..DOW]] [DATE] TIME   e.g. "Mon..Fri *-*-* 03:00:00", "*-*-* 04:30", "Sun 02:00"
	fields := strings.Fields(cal)
	dow, date, tm := "*", "*-*-*", ""
	for _, f := range fields {
		switch {
		case timeRe.MatchString(f):
			tm = f
		case strings.Contains(f, "-") && !strings.Contains(f, ".."):
			date = f
		case strings.Contains(f, "*-*-*"):
			date = f
		default:
			dow = f
		}
	}
	if tm == "" {
		return "", false
	}
	m := timeRe.FindStringSubmatch(tm)
	hour, minute := strings.TrimLeft(m[1], "0"), strings.TrimLeft(m[2], "0")
	if hour == "" {
		hour = "0"
	}
	if minute == "" {
		minute = "0"
	}
	dom, mon := "*", "*"
	if date != "*-*-*" {
		p := strings.Split(date, "-")
		if len(p) != 3 {
			return "", false
		}
		if p[1] != "*" {
			mon = strings.TrimLeft(p[1], "0")
		}
		if p[2] != "*" {
			dom = strings.TrimLeft(p[2], "0")
		}
		if p[0] != "*" {
			return "", false // a fixed year is not a recurring schedule
		}
	}
	cronDow := "*"
	if dow != "*" {
		var parts []string
		for _, d := range strings.Split(dow, ",") {
			if a, b, ok := strings.Cut(d, ".."); ok {
				x, y := dowNames[a[:3]], dowNames[b[:3]]
				if x == "" || y == "" {
					return "", false
				}
				parts = append(parts, x+"-"+y)
			} else {
				x := dowNames[d[:min(3, len(d))]]
				if x == "" {
					return "", false
				}
				parts = append(parts, x)
			}
		}
		cronDow = strings.Join(parts, ",")
	}
	return fmt.Sprintf("%s %s %s %s %s", minute, hour, dom, mon, cronDow), true
}

// ParseTimer turns a .timer unit plus its .service into a job.
func ParseTimer(name, timerText, serviceText string) (Job, bool) {
	cal := calendarRe.FindStringSubmatch(timerText)
	exec := execRe.FindStringSubmatch(serviceText)
	if cal == nil || exec == nil {
		return Job{}, false
	}
	sched, ok := CalendarToCron(cal[1])
	if !ok {
		return Job{}, false
	}
	cmd := strings.TrimSpace(exec[1])
	cmd = strings.TrimLeft(cmd, "-@+!") // systemd prefixes
	label := name
	if d := descRe.FindStringSubmatch(timerText); d != nil {
		label = strings.TrimSpace(d[1])
	}
	label = regexp.MustCompile(`[^A-Za-z0-9 ._-]`).ReplaceAllString(label, " ")
	label = strings.TrimSpace(strings.Join(strings.Fields(label), " "))
	if len(label) > 60 {
		label = label[:60]
	}
	return Job{Name: label, Type: TypeCommand, Schedule: sched, Command: cmd, Enabled: false, Overlap: "skip", NotifyOn: "failure", TimeoutSec: 3600}, true
}

// ReadSystemdTimers lists user-installed timers under /etc/systemd/system.
func ReadSystemdTimers() []Job {
	if runtime.GOOS != "linux" {
		return nil
	}
	var out []Job
	timers, _ := filepath.Glob("/etc/systemd/system/*.timer")
	for _, t := range timers {
		tb, err := os.ReadFile(t)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(t), ".timer")
		service := base + ".service"
		if u := unitRe.FindSubmatch(tb); u != nil {
			service = strings.TrimSpace(string(u[1]))
		}
		sb, err := os.ReadFile(filepath.Join("/etc/systemd/system", service))
		if err != nil {
			sb, err = os.ReadFile(filepath.Join("/lib/systemd/system", service))
			if err != nil {
				continue
			}
		}
		if j, ok := ParseTimer(base, string(tb), string(sb)); ok {
			out = append(out, j)
		}
	}
	return out
}
