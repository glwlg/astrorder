package sessions

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	scheduleKindOnce     = "once"
	scheduleKindInterval = "interval"
	scheduleKindDaily    = "daily"
	scheduleKindWeekly   = "weekly"
	scheduleKindCron     = "cron"

	whenBusySkip  = "skip"
	whenBusyQueue = "queue"

	misfireGraceOnce = "grace_once"
	misfireSkip      = "skip"
	misfireRunOnce   = "run_once"

	defaultMisfireGraceSec = 900
	maxIntervalMinutes     = 43200 // 30 days
)

// ScheduleSpec captures the timing parameters for a scheduled session prompt.
type ScheduleSpec struct {
	ScheduleKind    string `json:"schedule_kind"`
	CronExpr        string `json:"cron_expr"`
	IntervalMinutes int    `json:"interval_minutes"`
	DailyTime       string `json:"daily_time"`
	WeekdayMask     int    `json:"weekday_mask"`
	RunAt           string `json:"run_at"`
	Timezone        string `json:"timezone"`
	WhenBusy        string `json:"when_busy"`
	MisfirePolicy   string `json:"misfire_policy"`
	MisfireGraceSec int    `json:"misfire_grace_sec"`
}

func resolveScheduleLocation(tz string) (*time.Location, string, error) {
	trimmed := strings.TrimSpace(tz)
	if trimmed == "" || strings.EqualFold(trimmed, "local") {
		return time.Local, "Local", nil
	}
	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("invalid timezone %q: %w", trimmed, err)
	}
	return loc, trimmed, nil
}

func parseDailyTime(raw string) (int, int, string, error) {
	trimmed := strings.TrimSpace(raw)
	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return 0, 0, "", errors.New("daily_time must be in HH:MM format")
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, "", errors.New("daily_time must be a valid 24-hour HH:MM time")
	}
	return hour, minute, fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func parseRFC3339Time(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, errors.New("timestamp is empty")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid RFC3339 timestamp %q", trimmed)
}

// NormalizeScheduleSpec validates and normalizes a ScheduleSpec.
func NormalizeScheduleSpec(spec ScheduleSpec) (ScheduleSpec, error) {
	spec.ScheduleKind = strings.ToLower(strings.TrimSpace(spec.ScheduleKind))
	_, normTZ, err := resolveScheduleLocation(spec.Timezone)
	if err != nil {
		return spec, err
	}
	spec.Timezone = normTZ

	spec.WhenBusy = strings.ToLower(strings.TrimSpace(spec.WhenBusy))
	if spec.WhenBusy == "" {
		spec.WhenBusy = whenBusySkip
	}
	if spec.WhenBusy != whenBusySkip && spec.WhenBusy != whenBusyQueue {
		return spec, fmt.Errorf("when_busy must be %q or %q", whenBusySkip, whenBusyQueue)
	}

	spec.MisfirePolicy = strings.ToLower(strings.TrimSpace(spec.MisfirePolicy))
	if spec.MisfirePolicy == "" {
		spec.MisfirePolicy = misfireGraceOnce
	}
	if spec.MisfirePolicy != misfireGraceOnce && spec.MisfirePolicy != misfireSkip && spec.MisfirePolicy != misfireRunOnce {
		return spec, fmt.Errorf("misfire_policy must be %q, %q, or %q", misfireGraceOnce, misfireSkip, misfireRunOnce)
	}
	if spec.MisfireGraceSec <= 0 {
		spec.MisfireGraceSec = defaultMisfireGraceSec
	}
	if spec.MisfireGraceSec > 86400*7 {
		spec.MisfireGraceSec = 86400 * 7
	}

	switch spec.ScheduleKind {
	case scheduleKindOnce:
		parsed, err := parseRFC3339Time(spec.RunAt)
		if err != nil {
			return spec, fmt.Errorf("once schedule requires valid run_at: %w", err)
		}
		spec.RunAt = parsed.UTC().Format(time.RFC3339)
		spec.CronExpr = ""
		spec.IntervalMinutes = 0
		spec.DailyTime = ""
		spec.WeekdayMask = 0
	case scheduleKindInterval:
		if spec.IntervalMinutes < 1 || spec.IntervalMinutes > maxIntervalMinutes {
			return spec, fmt.Errorf("interval_minutes must be between 1 and %d", maxIntervalMinutes)
		}
		spec.CronExpr = ""
		spec.DailyTime = ""
		spec.WeekdayMask = 0
		spec.RunAt = ""
	case scheduleKindDaily:
		_, _, formatted, err := parseDailyTime(spec.DailyTime)
		if err != nil {
			return spec, err
		}
		spec.DailyTime = formatted
		spec.CronExpr = ""
		spec.IntervalMinutes = 0
		spec.WeekdayMask = 0
		spec.RunAt = ""
	case scheduleKindWeekly:
		_, _, formatted, err := parseDailyTime(spec.DailyTime)
		if err != nil {
			return spec, err
		}
		if spec.WeekdayMask < 1 || spec.WeekdayMask > 127 {
			return spec, errors.New("weekly schedule requires at least one weekday (weekday_mask 1..127)")
		}
		spec.DailyTime = formatted
		spec.CronExpr = ""
		spec.IntervalMinutes = 0
		spec.RunAt = ""
	case scheduleKindCron:
		spec.CronExpr = strings.Join(strings.Fields(strings.TrimSpace(spec.CronExpr)), " ")
		if _, err := parseCronExpression(spec.CronExpr); err != nil {
			return spec, err
		}
		spec.IntervalMinutes = 0
		spec.DailyTime = ""
		spec.WeekdayMask = 0
		spec.RunAt = ""
	default:
		return spec, fmt.Errorf("unsupported schedule_kind %q", spec.ScheduleKind)
	}
	return spec, nil
}

// NextScheduleRun computes the next UTC run time strictly after `after`.
// Returns ok=false when no future run exists (e.g. a completed one-shot schedule).
func NextScheduleRun(spec ScheduleSpec, after time.Time) (time.Time, bool, error) {
	norm, err := NormalizeScheduleSpec(spec)
	if err != nil {
		return time.Time{}, false, err
	}
	loc, _, err := resolveScheduleLocation(norm.Timezone)
	if err != nil {
		return time.Time{}, false, err
	}
	afterUTC := after.UTC()

	switch norm.ScheduleKind {
	case scheduleKindOnce:
		target, err := parseRFC3339Time(norm.RunAt)
		if err != nil {
			return time.Time{}, false, err
		}
		if !target.After(afterUTC) {
			return time.Time{}, false, nil
		}
		return target.UTC(), true, nil

	case scheduleKindInterval:
		next := afterUTC.Add(time.Duration(norm.IntervalMinutes) * time.Minute).Truncate(time.Second)
		if !next.After(afterUTC) {
			next = afterUTC.Add(time.Duration(norm.IntervalMinutes) * time.Minute)
		}
		return next.UTC(), true, nil

	case scheduleKindDaily:
		hour, minute, _, err := parseDailyTime(norm.DailyTime)
		if err != nil {
			return time.Time{}, false, err
		}
		localAfter := afterUTC.In(loc)
		for dayOffset := 0; dayOffset <= 2; dayOffset++ {
			base := localAfter.AddDate(0, 0, dayOffset)
			candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, loc)
			if candidate.After(localAfter) {
				return candidate.UTC(), true, nil
			}
		}
		return time.Time{}, false, errors.New("could not compute next daily run")

	case scheduleKindWeekly:
		hour, minute, _, err := parseDailyTime(norm.DailyTime)
		if err != nil {
			return time.Time{}, false, err
		}
		localAfter := afterUTC.In(loc)
		for dayOffset := 0; dayOffset <= 8; dayOffset++ {
			base := localAfter.AddDate(0, 0, dayOffset)
			bit := 1 << uint(base.Weekday())
			if norm.WeekdayMask&bit == 0 {
				continue
			}
			candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, loc)
			if candidate.After(localAfter) {
				return candidate.UTC(), true, nil
			}
		}
		return time.Time{}, false, errors.New("could not compute next weekly run")

	case scheduleKindCron:
		expr, err := parseCronExpression(norm.CronExpr)
		if err != nil {
			return time.Time{}, false, err
		}
		next, ok := expr.next(afterUTC.In(loc))
		if !ok {
			return time.Time{}, false, errors.New("cron expression produces no matching date within 4 years")
		}
		return next.UTC(), true, nil
	}
	return time.Time{}, false, fmt.Errorf("unsupported schedule_kind %q", norm.ScheduleKind)
}

// PreviewScheduleRuns computes up to `count` future UTC RFC3339 timestamps after `after`.
func PreviewScheduleRuns(spec ScheduleSpec, after time.Time, count int) ([]string, error) {
	if count <= 0 {
		count = 3
	}
	if count > 10 {
		count = 10
	}
	norm, err := NormalizeScheduleSpec(spec)
	if err != nil {
		return nil, err
	}
	results := make([]string, 0, count)
	cursor := after.UTC()
	for len(results) < count {
		next, ok, err := NextScheduleRun(norm, cursor)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		results = append(results, next.UTC().Format(time.RFC3339))
		cursor = next
	}
	return results, nil
}

type cronExpression struct {
	minutes       uint64 // bits 0..59
	hours         uint32 // bits 0..23
	doms          uint32 // bits 1..31
	months        uint16 // bits 1..12
	dows          uint8  // bits 0..6 (0=Sunday)
	domRestricted bool
	dowRestricted bool
}

var cronMonthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var cronDowNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

func parseCronExpression(raw string) (cronExpression, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) != 5 {
		return cronExpression{}, errors.New("cron expression must have exactly 5 fields (minute hour day month weekday)")
	}
	minMask, _, err := parseCronField(fields[0], 0, 59, nil, false)
	if err != nil {
		return cronExpression{}, fmt.Errorf("invalid cron minute field: %w", err)
	}
	hourMask, _, err := parseCronField(fields[1], 0, 23, nil, false)
	if err != nil {
		return cronExpression{}, fmt.Errorf("invalid cron hour field: %w", err)
	}
	domMask, domWild, err := parseCronField(fields[2], 1, 31, nil, false)
	if err != nil {
		return cronExpression{}, fmt.Errorf("invalid cron day-of-month field: %w", err)
	}
	monMask, _, err := parseCronField(fields[3], 1, 12, cronMonthNames, false)
	if err != nil {
		return cronExpression{}, fmt.Errorf("invalid cron month field: %w", err)
	}
	dowMask, dowWild, err := parseCronField(fields[4], 0, 7, cronDowNames, true)
	if err != nil {
		return cronExpression{}, fmt.Errorf("invalid cron day-of-week field: %w", err)
	}
	// Normalize DOW bit 7 (Sunday) into bit 0.
	if dowMask&(1<<7) != 0 {
		dowMask = (dowMask &^ (1 << 7)) | 1
	}
	return cronExpression{
		minutes:       minMask,
		hours:         uint32(hourMask),
		doms:          uint32(domMask),
		months:        uint16(monMask),
		dows:          uint8(dowMask),
		domRestricted: !domWild,
		dowRestricted: !dowWild,
	}, nil
}

func parseCronField(field string, minVal, maxVal int, names map[string]int, isDow bool) (uint64, bool, error) {
	if field == "" {
		return 0, false, errors.New("empty field")
	}
	isWildcard := field == "*"
	var mask uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, false, errors.New("empty list element")
		}
		step := 1
		rangePart := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 || pieces[1] == "" {
				return 0, false, fmt.Errorf("invalid step syntax %q", part)
			}
			parsedStep, err := strconv.Atoi(pieces[1])
			if err != nil || parsedStep <= 0 {
				return 0, false, fmt.Errorf("invalid step value %q", pieces[1])
			}
			step = parsedStep
			rangePart = pieces[0]
		}
		var start, end int
		if rangePart == "*" {
			start, end = minVal, maxVal
			if isDow {
				end = 6
			}
		} else if strings.Contains(rangePart, "-") {
			bounds := strings.Split(rangePart, "-")
			if len(bounds) != 2 {
				return 0, false, fmt.Errorf("invalid range %q", rangePart)
			}
			var err error
			start, err = parseCronAtom(bounds[0], minVal, maxVal, names)
			if err != nil {
				return 0, false, err
			}
			end, err = parseCronAtom(bounds[1], minVal, maxVal, names)
			if err != nil {
				return 0, false, err
			}
			if start > end {
				return 0, false, fmt.Errorf("range start %d > end %d", start, end)
			}
		} else {
			val, err := parseCronAtom(rangePart, minVal, maxVal, names)
			if err != nil {
				return 0, false, err
			}
			start = val
			if strings.Contains(part, "/") {
				end = maxVal
				if isDow {
					end = 6
				}
			} else {
				end = val
			}
		}
		for v := start; v <= end; v += step {
			mask |= 1 << uint(v)
		}
	}
	if mask == 0 {
		return 0, false, errors.New("field matched no values")
	}
	return mask, isWildcard, nil
}

func parseCronAtom(raw string, minVal, maxVal int, names map[string]int) (int, error) {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	if names != nil {
		if val, ok := names[upper]; ok {
			return val, nil
		}
	}
	val, err := strconv.Atoi(upper)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", raw)
	}
	if val < minVal || val > maxVal {
		return 0, fmt.Errorf("value %d out of range [%d..%d]", val, minVal, maxVal)
	}
	return val, nil
}

func (c cronExpression) matchesDay(t time.Time) bool {
	domMatch := c.doms&(1<<uint(t.Day())) != 0
	dowMatch := c.dows&(1<<uint(t.Weekday())) != 0
	if c.domRestricted && c.dowRestricted {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

func (c cronExpression) next(after time.Time) (time.Time, bool) {
	loc := after.Location()
	// Start strictly after `after` on a minute boundary.
	t := after.Truncate(time.Minute).Add(time.Minute)
	limitYear := t.Year() + 4
	for t.Year() <= limitYear {
		if c.months&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !c.matchesDay(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if c.hours&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}
		if c.minutes&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		if t.After(after) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}
