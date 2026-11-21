package backups

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	minIntervalMinutes = 5
	maxIntervalMinutes = 43200
	maxRetentionCount  = 500
)

// ValidateSchedule normalizes and validates schedule and retention settings.
func ValidateSchedule(enabled bool, mode string, intervalMinutes int, dailyTime string, weekday int, retentionCount int) (normalizedMode string, normalizedInterval int, normalizedTime string, normalizedWeekday int, normalizedRetention int, err error) {
	normalizedMode = strings.TrimSpace(strings.ToLower(mode))
	if normalizedMode == "" {
		normalizedMode = ScheduleManual
	}
	switch normalizedMode {
	case ScheduleManual, ScheduleInterval, ScheduleDaily, ScheduleWeekly:
	default:
		return "", 0, "", 0, 0, policyError(422, fmt.Sprintf("不支持的定时模式 %q。", mode))
	}

	normalizedInterval = intervalMinutes
	if normalizedInterval == 0 {
		normalizedInterval = DefaultIntervalMins
	}
	if normalizedInterval < minIntervalMinutes || normalizedInterval > maxIntervalMinutes {
		return "", 0, "", 0, 0, policyError(422, "定时间隔必须在 5 到 43200 分钟之间。")
	}

	normalizedTime = strings.TrimSpace(dailyTime)
	if normalizedTime == "" {
		normalizedTime = DefaultDailyTime
	}
	if _, _, parseErr := parseDailyTime(normalizedTime); parseErr != nil {
		return "", 0, "", 0, 0, parseErr
	}

	normalizedWeekday = weekday
	if normalizedWeekday < 0 || normalizedWeekday > 6 {
		return "", 0, "", 0, 0, policyError(422, "星期设置必须在 0（周日）到 6（周六）之间。")
	}

	normalizedRetention = retentionCount
	if normalizedRetention < 0 || normalizedRetention > maxRetentionCount {
		return "", 0, "", 0, 0, policyError(422, "保留版本数必须在 0 到 500 之间。")
	}

	if normalizedMode == ScheduleManual && enabled {
		return "", 0, "", 0, 0, policyError(422, "选择「仅手动」模式时不能启用定时计划，请选择按间隔、每天或每周。")
	}
	return normalizedMode, normalizedInterval, normalizedTime, normalizedWeekday, normalizedRetention, nil
}

func parseDailyTime(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, policyError(422, "每日执行时间必须采用 HH:MM 格式，例如 03:00。")
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, policyError(422, "每日执行时间小时范围为 00-23，分钟范围为 00-59。")
	}
	return hour, minute, nil
}

// NextRunTime computes the next scheduled UTC run time strictly after now in loc.
func NextRunTime(job BackupJob, now time.Time, loc *time.Location) *time.Time {
	if !job.ScheduleEnabled || job.ScheduleMode == ScheduleManual {
		return nil
	}
	if loc == nil {
		loc = time.Local
	}
	baseUTC := now.UTC()
	switch job.ScheduleMode {
	case ScheduleInterval:
		mins := job.IntervalMinutes
		if mins < minIntervalMinutes {
			mins = DefaultIntervalMins
		}
		next := baseUTC.Add(time.Duration(mins) * time.Minute)
		return &next
	case ScheduleDaily:
		hour, minute, err := parseDailyTime(job.DailyTime)
		if err != nil {
			hour, minute = 3, 0
		}
		localNow := now.In(loc)
		candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc)
		if !candidate.After(localNow) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		next := candidate.UTC()
		return &next
	case ScheduleWeekly:
		hour, minute, err := parseDailyTime(job.DailyTime)
		if err != nil {
			hour, minute = 3, 0
		}
		targetWeekday := time.Weekday(job.Weekday)
		if targetWeekday < time.Sunday || targetWeekday > time.Saturday {
			targetWeekday = time.Sunday
		}
		localNow := now.In(loc)
		daysAhead := (int(targetWeekday) - int(localNow.Weekday()) + 7) % 7
		candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc).AddDate(0, 0, daysAhead)
		if !candidate.After(localNow) {
			candidate = candidate.AddDate(0, 0, 7)
		}
		next := candidate.UTC()
		return &next
	default:
		return nil
	}
}
