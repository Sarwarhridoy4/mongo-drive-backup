package drive

import (
	"testing"
)

func TestParseCreatedTime(t *testing.T) {
	tm, err := ParseCreatedTime("2026-09-08T02:00:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tm.Year() != 2026 || tm.Month() != 9 || tm.Day() != 8 {
		t.Errorf("unexpected date: %v", tm)
	}
}

func TestParseCreatedTime_Invalid(t *testing.T) {
	_, err := ParseCreatedTime("not-a-date")
	if err == nil {
		t.Errorf("expected error for invalid date")
	}
}
