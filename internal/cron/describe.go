package cron

import (
	"fmt"
	"strconv"
	"strings"
)

var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var monthNames = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

// Describe turns a five-field cron expression into English. Unusual
// expressions fall back to a generic phrase; the caller still has the
// next-runs list to show what really happens.
func Describe(spec string) string {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "":
		return "Runs only when started by hand."
	case "@yearly", "@annually":
		return "Once a year, on January 1 at 00:00."
	case "@monthly":
		return "Once a month, on the 1st at 00:00."
	case "@weekly":
		return "Once a week, on Sunday at 00:00."
	case "@daily", "@midnight":
		return "Every day at 00:00."
	case "@hourly":
		return "Every hour, on the hour."
	}
	if strings.HasPrefix(spec, "@every ") {
		return "Every " + strings.TrimPrefix(spec, "@every ") + "."
	}
	f := strings.Fields(spec)
	if len(f) != 5 {
		return "Custom schedule."
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	timePart := ""
	if isNum(min) && isNum(hour) {
		timePart = fmt.Sprintf("at %02s:%02s", hour, min)
	} else if isNum(min) && hour == "*" {
		if min == "0" {
			timePart = "every hour, on the hour"
		} else {
			timePart = fmt.Sprintf("every hour at minute %s", min)
		}
	} else if step, ok := stepOf(min); ok && hour == "*" {
		timePart = fmt.Sprintf("every %d minutes", step)
	} else if min == "*" && hour == "*" {
		timePart = "every minute"
	} else if isNum(min) {
		if step, ok := stepOf(hour); ok {
			timePart = fmt.Sprintf("every %d hours at minute %s", step, min)
		} else if isList(hour) {
			timePart = "at " + joinTimes(hour, min)
		}
	}
	if timePart == "" {
		return "Custom schedule."
	}
	datePart := ""
	switch {
	case dom == "*" && mon == "*" && dow == "*":
		datePart = "every day"
	case dom == "*" && mon == "*" && dow != "*":
		datePart = "on " + describeDays(dow)
	case dom != "*" && mon == "*" && dow == "*":
		if isNum(dom) {
			datePart = "on day " + dom + " of every month"
		} else if isList(dom) {
			datePart = "on days " + strings.ReplaceAll(dom, ",", ", ") + " of every month"
		} else if step, ok := stepOf(dom); ok {
			datePart = fmt.Sprintf("every %d days", step)
		}
	case dom != "*" && mon != "*" && dow == "*":
		if isNum(dom) && isNum(mon) {
			m, _ := strconv.Atoi(mon)
			if m >= 1 && m <= 12 {
				datePart = "on " + monthNames[m] + " " + dom
			}
		}
	}
	if datePart == "" {
		return "Custom schedule."
	}
	out := strings.ToUpper(timePart[:1]) + timePart[1:] + ", " + datePart + "."
	if strings.HasPrefix(timePart, "at ") {
		out = strings.ToUpper(datePart[:1]) + datePart[1:] + " " + timePart + "."
	}
	return out
}

func isNum(s string) bool { _, err := strconv.Atoi(s); return err == nil }
func isList(s string) bool {
	for _, p := range strings.Split(s, ",") {
		if !isNum(p) {
			return false
		}
	}
	return true
}
func stepOf(s string) (int, bool) {
	if strings.HasPrefix(s, "*/") {
		n, err := strconv.Atoi(s[2:])
		return n, err == nil && n > 0
	}
	return 0, false
}
func joinTimes(hours, min string) string {
	var parts []string
	for _, h := range strings.Split(hours, ",") {
		parts = append(parts, fmt.Sprintf("%02s:%02s", h, min))
	}
	return strings.Join(parts, ", ")
}
func describeDays(dow string) string {
	if strings.Contains(dow, "-") {
		a, b, _ := strings.Cut(dow, "-")
		ai, err1 := strconv.Atoi(a)
		bi, err2 := strconv.Atoi(b)
		if err1 == nil && err2 == nil && ai >= 0 && bi <= 7 {
			if ai == 1 && bi == 5 {
				return "weekdays"
			}
			return dayNames[ai%7] + " to " + dayNames[bi%7]
		}
	}
	var names []string
	for _, p := range strings.Split(dow, ",") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 7 {
			return "selected days"
		}
		names = append(names, dayNames[n%7])
	}
	if len(names) == 2 && names[0] == "Saturday" && names[1] == "Sunday" || len(names) == 2 && names[0] == "Sunday" && names[1] == "Saturday" {
		return "weekends"
	}
	return strings.Join(names, ", ")
}
