package source

import (
	"os"
	"testing"
	"time"

	"linznetz-import/internal/models"
)

func TestShouldSkipDownload(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Vienna")
	stateFile := "test_state_skip.json"
	
	// Create a dummy state file
	os.WriteFile(stateFile, []byte("{}"), 0644)
	defer os.Remove(stateFile)

	// Test Case 1: Before 12:00, LatestIntermediate is yesterday
	nowBefore12 := time.Date(2026, 3, 22, 10, 0, 0, 0, loc)
	s1 := &models.State{
		Meters: map[string]models.MeterState{
			"AT1": {LatestIntermediate: time.Date(2026, 3, 21, 23, 45, 0, 0, loc)},
		},
	}
	if !ShouldSkipDownload(s1, stateFile, loc, nowBefore12, false) {
		t.Error("Expected skip=true before 12:00 with yesterday's data")
	}

	// Test Case 2: After 12:00, LatestIntermediate is yesterday (should NOT skip)
	nowAfter12 := time.Date(2026, 3, 22, 14, 0, 0, 0, loc)
	if ShouldSkipDownload(s1, stateFile, loc, nowAfter12, false) {
		t.Error("Expected skip=false after 12:00 with yesterday's data")
	}

	// Test Case 3: After 12:00, LatestIntermediate is today (should skip)
	s2 := &models.State{
		Meters: map[string]models.MeterState{
			"AT1": {LatestIntermediate: time.Date(2026, 3, 22, 0, 15, 0, 0, loc)},
		},
	}
	if !ShouldSkipDownload(s2, stateFile, loc, nowAfter12, false) {
		t.Error("Expected skip=true after 12:00 with today's data")
	}

	// Test Case 4: force=true (should NOT skip)
	if ShouldSkipDownload(s2, stateFile, loc, nowAfter12, true) {
		t.Error("Expected skip=false when force=true")
	}

	// Test Case 5: No state file (should NOT skip)
	if ShouldSkipDownload(s2, "non_existent.json", loc, nowAfter12, false) {
		t.Error("Expected skip=false when state file is missing")
	}
}
