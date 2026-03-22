package exporter

import (
	"os"
	"testing"
	"time"

	"linznetz-import/internal/models"
)

func TestExporterStateUpdate(t *testing.T) {
	stateFile := "test_state_exporter.json"
	defer os.Remove(stateFile)

	s := &models.State{
		Meters: make(map[string]models.MeterState),
	}

	exp := &DebugExporter{Measurement: "test"}

	loc := time.UTC
	meterID := "AT123"

	// 1. Export intermediate data point (Status "F")
	t1 := time.Date(2026, 3, 22, 10, 0, 0, 0, loc)
	points1 := []models.DataPoint{
		{Time: t1, Total: 1.0, Status: "F"},
	}
	exp.Export(meterID, points1, "import", s, stateFile)

	ms := s.Meters[meterID]
	if !ms.LatestFinal.IsZero() {
		t.Errorf("Expected LatestFinal to be zero for Status F, got %v", ms.LatestFinal)
	}
	if !ms.LatestIntermediate.Equal(t1) {
		t.Errorf("Expected LatestIntermediate %v, got %v", t1, ms.LatestIntermediate)
	}

	// 2. Export final data point (Status "M")
	t2 := time.Date(2026, 3, 22, 11, 0, 0, 0, loc)
	points2 := []models.DataPoint{
		{Time: t2, Total: 2.0, Status: "M"},
	}
	exp.Export(meterID, points2, "import", s, stateFile)

	ms = s.Meters[meterID]
	if !ms.LatestFinal.Equal(t2) {
		t.Errorf("Expected LatestFinal %v, got %v", t2, ms.LatestFinal)
	}
	if !ms.LatestIntermediate.Equal(t2) {
		t.Errorf("Expected LatestIntermediate %v, got %v", t2, ms.LatestIntermediate)
	}

	// 3. Export older data point (should NOT update state)
	t3 := time.Date(2026, 3, 22, 0, 0, 0, 0, loc)
	points3 := []models.DataPoint{
		{Time: t3, Total: 0.5, Status: "M"},
	}
	exp.Export(meterID, points3, "import", s, stateFile)

	ms = s.Meters[meterID]
	if !ms.LatestFinal.Equal(t2) {
		t.Errorf("Expected LatestFinal to remain %v, got %v", t2, ms.LatestFinal)
	}
}
