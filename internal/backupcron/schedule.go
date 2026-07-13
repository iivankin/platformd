package backupcron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const searchYears = 8

type field struct {
	bits     uint64
	minimum  int
	maximum  int
	wildcard bool
}

type Schedule struct {
	minute     field
	hour       field
	dayOfMonth field
	month      field
	dayOfWeek  field
}

func Parse(value string) (Schedule, error) {
	parts := strings.Fields(value)
	if len(parts) != 5 {
		return Schedule{}, errors.New("backup cron must contain exactly five space-separated fields")
	}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	fields := make([]field, len(parts))
	for index, part := range parts {
		parsed, err := parseField(part, ranges[index][0], ranges[index][1])
		if err != nil {
			return Schedule{}, fmt.Errorf("backup cron field %d: %w", index+1, err)
		}
		fields[index] = parsed
	}
	return Schedule{
		minute: fields[0], hour: fields[1], dayOfMonth: fields[2],
		month: fields[3], dayOfWeek: fields[4],
	}, nil
}

func Canonical(value string) (string, error) {
	if _, err := Parse(value); err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(value), " "), nil
}

func (schedule Schedule) Matches(timestamp time.Time) bool {
	value := timestamp.UTC()
	if value.Second() != 0 || value.Nanosecond() != 0 {
		return false
	}
	if !schedule.minute.contains(value.Minute()) || !schedule.hour.contains(value.Hour()) || !schedule.month.contains(int(value.Month())) {
		return false
	}
	dayOfMonth := schedule.dayOfMonth.contains(value.Day())
	dayOfWeek := schedule.dayOfWeek.contains(int(value.Weekday()))
	switch {
	case schedule.dayOfMonth.wildcard && schedule.dayOfWeek.wildcard:
		return true
	case schedule.dayOfMonth.wildcard:
		return dayOfWeek
	case schedule.dayOfWeek.wildcard:
		return dayOfMonth
	default:
		return dayOfMonth || dayOfWeek
	}
}

func (schedule Schedule) Next(after time.Time) (time.Time, error) {
	candidate := after.UTC().Truncate(time.Minute).Add(time.Minute)
	deadline := candidate.AddDate(searchYears, 0, 0)
	for candidate.Before(deadline) {
		if !schedule.month.contains(int(candidate.Month())) {
			month, wrapped := schedule.month.next(int(candidate.Month()))
			year := candidate.Year()
			if wrapped {
				year++
			}
			candidate = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !schedule.matchesDay(candidate) {
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !schedule.hour.contains(candidate.Hour()) {
			hour, wrapped := schedule.hour.next(candidate.Hour())
			if wrapped {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()+1, hour, 0, 0, 0, time.UTC)
			} else {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), hour, 0, 0, 0, time.UTC)
			}
			continue
		}
		if !schedule.minute.contains(candidate.Minute()) {
			minute, wrapped := schedule.minute.next(candidate.Minute())
			if wrapped {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour()+1, minute, 0, 0, time.UTC)
			} else {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour(), minute, 0, 0, time.UTC)
			}
			continue
		}
		if schedule.Matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, errors.New("backup cron has no occurrence within eight years")
}

// Previous returns the latest occurrence strictly before the supplied time.
func (schedule Schedule) Previous(before time.Time) (time.Time, error) {
	candidate := before.UTC().Truncate(time.Minute)
	if !candidate.Before(before.UTC()) {
		candidate = candidate.Add(-time.Minute)
	}
	deadline := candidate.AddDate(-searchYears, 0, 0)
	for candidate.After(deadline) {
		if !schedule.month.contains(int(candidate.Month())) {
			month, wrapped := schedule.month.previous(int(candidate.Month()))
			year := candidate.Year()
			if wrapped {
				year--
			}
			candidate = time.Date(year, time.Month(month)+1, 0, 23, 59, 0, 0, time.UTC)
			continue
		}
		if !schedule.matchesDay(candidate) {
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()-1, 23, 59, 0, 0, time.UTC)
			continue
		}
		if !schedule.hour.contains(candidate.Hour()) {
			hour, wrapped := schedule.hour.previous(candidate.Hour())
			if wrapped {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()-1, hour, 59, 0, 0, time.UTC)
			} else {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), hour, 59, 0, 0, time.UTC)
			}
			continue
		}
		if !schedule.minute.contains(candidate.Minute()) {
			minute, wrapped := schedule.minute.previous(candidate.Minute())
			if wrapped {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour()-1, minute, 0, 0, time.UTC)
			} else {
				candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour(), minute, 0, 0, time.UTC)
			}
			continue
		}
		if schedule.Matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(-time.Minute)
	}
	return time.Time{}, errors.New("backup cron has no previous occurrence within eight years")
}

func (schedule Schedule) matchesDay(value time.Time) bool {
	dayOfMonth := schedule.dayOfMonth.contains(value.Day())
	dayOfWeek := schedule.dayOfWeek.contains(int(value.Weekday()))
	switch {
	case schedule.dayOfMonth.wildcard && schedule.dayOfWeek.wildcard:
		return true
	case schedule.dayOfMonth.wildcard:
		return dayOfWeek
	case schedule.dayOfWeek.wildcard:
		return dayOfMonth
	default:
		return dayOfMonth || dayOfWeek
	}
}

func parseField(value string, minimum, maximum int) (field, error) {
	if value == "" {
		return field{}, errors.New("field is empty")
	}
	result := field{minimum: minimum, maximum: maximum, wildcard: value == "*"}
	for _, item := range strings.Split(value, ",") {
		if item == "" {
			return field{}, errors.New("list contains an empty item")
		}
		base, stepText, hasStep := strings.Cut(item, "/")
		if hasStep && (stepText == "" || strings.Contains(stepText, "/")) {
			return field{}, errors.New("step is malformed")
		}
		step := 1
		if hasStep {
			var err error
			step, err = parseNumber(stepText, 1, maximum-minimum+1)
			if err != nil {
				return field{}, fmt.Errorf("step: %w", err)
			}
		}
		start, end, err := parseBase(base, minimum, maximum, hasStep)
		if err != nil {
			return field{}, err
		}
		for current := start; current <= end; current += step {
			result.bits |= uint64(1) << uint(current-minimum)
		}
	}
	if result.bits == 0 {
		return field{}, errors.New("field selects no values")
	}
	return result, nil
}

func parseBase(value string, minimum, maximum int, stepped bool) (int, int, error) {
	if value == "*" {
		return minimum, maximum, nil
	}
	if left, right, rangeFound := strings.Cut(value, "-"); rangeFound {
		if left == "" || right == "" || strings.Contains(right, "-") {
			return 0, 0, errors.New("range is malformed")
		}
		start, err := parseNumber(left, minimum, maximum)
		if err != nil {
			return 0, 0, fmt.Errorf("range start: %w", err)
		}
		end, err := parseNumber(right, minimum, maximum)
		if err != nil {
			return 0, 0, fmt.Errorf("range end: %w", err)
		}
		if start > end {
			return 0, 0, errors.New("range start exceeds end")
		}
		return start, end, nil
	}
	start, err := parseNumber(value, minimum, maximum)
	if err != nil {
		return 0, 0, err
	}
	if stepped {
		return start, maximum, nil
	}
	return start, start, nil
}

func parseNumber(value string, minimum, maximum int) (int, error) {
	if value == "" {
		return 0, errors.New("number is empty")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("only numeric values are supported")
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum || number > maximum {
		return 0, fmt.Errorf("value must be %d..%d", minimum, maximum)
	}
	return number, nil
}

func (value field) contains(number int) bool {
	if number < value.minimum || number > value.maximum {
		return false
	}
	return value.bits&(uint64(1)<<uint(number-value.minimum)) != 0
}

func (value field) next(current int) (int, bool) {
	for candidate := current + 1; candidate <= value.maximum; candidate++ {
		if value.contains(candidate) {
			return candidate, false
		}
	}
	for candidate := value.minimum; candidate <= current; candidate++ {
		if value.contains(candidate) {
			return candidate, true
		}
	}
	panic("validated cron field contains no values")
}

func (value field) previous(current int) (int, bool) {
	for candidate := current - 1; candidate >= value.minimum; candidate-- {
		if value.contains(candidate) {
			return candidate, false
		}
	}
	for candidate := value.maximum; candidate >= current; candidate-- {
		if value.contains(candidate) {
			return candidate, true
		}
	}
	panic("validated cron field contains no values")
}
