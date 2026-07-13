package backupcron

import (
	"testing"
	"time"
)

func TestFiveFieldValidationAndNextOccurrence(t *testing.T) {
	t.Parallel()
	schedule, err := Parse("*/15 9-17/2 * 1,6,12 1-5")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, time.June, 1, 10, 16, 23, 0, time.FixedZone("test", 2*60*60))
	next, err := schedule.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2026, time.June, 1, 9, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("next = %s, want %s", next, expected)
	}
	if !schedule.Matches(expected) || schedule.Matches(expected.Add(time.Minute)) {
		t.Fatal("schedule match differs from the parsed minute selection")
	}
}

func TestVixieDayOfMonthOrDayOfWeekSemantics(t *testing.T) {
	t.Parallel()
	schedule, err := Parse("0 0 13 * 5")
	if err != nil {
		t.Fatal(err)
	}
	friday := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)
	thirteenth := time.Date(2026, time.April, 13, 0, 0, 0, 0, time.UTC)
	neither := time.Date(2026, time.April, 14, 0, 0, 0, 0, time.UTC)
	if !schedule.Matches(friday) || !schedule.Matches(thirteenth) || schedule.Matches(neither) {
		t.Fatal("day-of-month/day-of-week OR semantics are incorrect")
	}
}

func TestLeapDayAndStrictlyLaterOccurrence(t *testing.T) {
	t.Parallel()
	schedule, err := Parse("0 0 29 2 *")
	if err != nil {
		t.Fatal(err)
	}
	next, err := schedule.Next(time.Date(2028, time.February, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2032, time.February, 29, 0, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("next leap day = %s, want %s", next, expected)
	}
}

func TestRejectsUnsupportedOrOutOfRangeCron(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"", "* * * *", "@daily", "0 0 * * SUN", "0 0 * * 7",
		"60 * * * *", "*/0 * * * *", "0 0 31-1 * *", "0 0 * * * extra",
		"0 0 * * 1,", "0 0 * * 1//2", "TZ=UTC 0 0 * * *",
	}
	for _, value := range invalid {
		if _, err := Parse(value); err == nil {
			t.Errorf("accepted invalid cron %q", value)
		}
	}
}

func TestCanonicalWhitespace(t *testing.T) {
	t.Parallel()
	value, err := Canonical("  0\t0   * * *  ")
	if err != nil || value != "0 0 * * *" {
		t.Fatalf("canonical cron = %q, %v", value, err)
	}
}

func TestPreviousOccurrenceIsStrictAndUsesVixieDaySemantics(t *testing.T) {
	t.Parallel()
	schedule, err := Parse("15 9 1 * 1")
	if err != nil {
		t.Fatal(err)
	}
	previous, err := schedule.Previous(time.Date(2026, time.July, 14, 9, 15, 30, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.July, 13, 9, 15, 0, 0, time.UTC)
	if !previous.Equal(want) {
		t.Fatalf("previous occurrence = %s, want %s", previous, want)
	}
	strict, err := schedule.Previous(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strict.Before(want) || !schedule.Matches(strict) {
		t.Fatalf("strict previous occurrence = %s", strict)
	}
}

func TestPreviousOccurrenceCrossesMonthAndYear(t *testing.T) {
	t.Parallel()
	schedule, err := Parse("0 0 31 12 *")
	if err != nil {
		t.Fatal(err)
	}
	previous, err := schedule.Previous(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC)
	if !previous.Equal(want) {
		t.Fatalf("previous occurrence = %s, want %s", previous, want)
	}
}
