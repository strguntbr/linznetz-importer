package state

import (
	"os"
	"testing"
	"time"

	"linznetz-import/internal/models"
)

func TestLoadSaveState(t *testing.T) {
	tmpFile := "test_state.json"
	defer os.Remove(tmpFile)

	s := &models.State{
		Meters: map[string]models.MeterState{
			"AT123": {
				LatestFinal:        time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC),
				LatestIntermediate: time.Date(2026, 3, 21, 11, 0, 0, 0, time.UTC),
			},
		},
	}

	Save(tmpFile, s)

	loaded := Load(tmpFile)
	if len(loaded.Meters) != 1 {
		t.Fatalf("expected 1 meter, got %d", len(loaded.Meters))
	}

	ms, ok := loaded.Meters["AT123"]
	if !ok {
		t.Fatal("meter AT123 not found")
	}

	if !ms.LatestFinal.Equal(s.Meters["AT123"].LatestFinal) {
		t.Errorf("expected Final %v, got %v", s.Meters["AT123"].LatestFinal, ms.LatestFinal)
	}
	if !ms.LatestIntermediate.Equal(s.Meters["AT123"].LatestIntermediate) {
		t.Errorf("expected Intermediate %v, got %v", s.Meters["AT123"].LatestIntermediate, ms.LatestIntermediate)
	}
}
