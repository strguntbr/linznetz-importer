package main

import (
	"strings"
	"testing"
	"time"

	"linznetz-import/internal/parser"
	"linznetz-import/internal/util"
)

func TestExtractMeterID(t *testing.T) {
	// Case 1: Standard ID with prefix
	filename := "AT0031000000099000000000000017641_QH_20260317.csv"
	expected := "AT0031000000099000000000000017641"
	got := util.ExtractMeterID(filename)
	if got != expected {
		t.Errorf("ExtractMeterID(%q) = %q; want %q", filename, got, expected)
	}

	// Case 2: ID without 0031... prefix
	filename2 := "AT0031099012345_test.csv"
	expected2 := "AT0031099012345"
	got2 := util.ExtractMeterID(filename2)
	if got2 != expected2 {
		t.Errorf("ExtractMeterID(%q) = %q; want %q", filename2, got2, expected2)
	}
}

func TestShortenMeterID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"AT0031000000099000000000000017641", "17641"},
		{"AT0031099012345", "12345"},
		{"AT003109900", "0"},
		{"OTHER123", "OTHER123"},
	}

	for _, tc := range tests {
		got := util.ShortenMeterID(tc.input)
		if got != tc.expected {
			t.Errorf("ShortenMeterID(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseCSVToPoints(t *testing.T) {
	// Import Case
	csvDataImport := "ignore;Datum bis;Energiemenge in kWh;ignore2;Restnetzbezug in kWh;Status\n" +
		"x;17.03.2026 00:15:00;1,23;y;0,45;M\n"

	loc, _ := time.LoadLocation("Europe/Vienna")
	points, dir := parser.ParseCSVToPoints(strings.NewReader(csvDataImport), loc)

	if len(points) != 1 || dir != "import" {
		t.Errorf("expected 1 point and dir=import, got %d points and dir=%s", len(points), dir)
	}

	// Export Case
	csvDataExport := "ignore;Datum bis;Energiemenge in kWh;ignore2;Restnetzueberschuss in kWh;Status\n" +
		"x;17.03.2026 00:15:00;1,23;y;0,45;M\n"

	points, dir = parser.ParseCSVToPoints(strings.NewReader(csvDataExport), loc)
	if len(points) != 1 || dir != "export" {
		t.Errorf("expected 1 point and dir=export, got %d points and dir=%s", len(points), dir)
	}
}
