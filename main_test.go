package main

import (
	"strings"
	"testing"
	"time"
)

func TestExtractMeterID(t *testing.T) {
	// Case 1: Standard ID with prefix
	filename := "AT0031000000099000000000000017641_QH_20260317.csv"
	expected := "17641"
	got := extractMeterID(filename)
	if got != expected {
		t.Errorf("extractMeterID(%q) = %q; want %q", filename, got, expected)
	}

	// Case 2: ID without 0031... prefix
	filename2 := "AT00012345_test.csv"
	expected2 := "12345"
	got2 := extractMeterID(filename2)
	if got2 != expected2 {
		t.Errorf("extractMeterID(%q) = %q; want %q", filename2, got2, expected2)
	}

	// Case 3: Empty ID digits
	filename3 := "AT0000.csv"
	expected3 := "0"
	got3 := extractMeterID(filename3)
	if got3 != expected3 {
		t.Errorf("extractMeterID(%q) = %q; want %q", filename3, got3, expected3)
	}
}

func TestShortenSubject(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "LINZ NETZ VDI - Tagesbericht Viertelstundenverbrauch Detail:  4232 Hagenberg im Mühlkreis, AT0031000000099000000000000017641",
			expected: "Tagesbericht Viertelstundenverbrauch",
		},
		{
			input:    "LINZ NETZ VDI - Tagesbericht Viertelstundenverbrauch Detail: 4232 Hagenberg im Mühlkreis, AT12345",
			expected: "Tagesbericht Viertelstundenverbrauch",
		},
		{
			input:    "Something Else - AT12345",
			expected: "Something Else",
		},
	}

	for _, tc := range tests {
		got := shortenSubject(tc.input)
		if got != tc.expected {
			t.Errorf("shortenSubject(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseCSVToPoints(t *testing.T) {
	// Import Case
	csvDataImport := "ignore;Datum bis;Energiemenge in kWh;ignore2;Restnetzbezug in kWh;Status\n" +
		"x;17.03.2026 00:15:00;1,23;y;0,45;M\n"

	loc, _ := time.LoadLocation("Europe/Vienna")
	points, dir := parseCSVToPoints(strings.NewReader(csvDataImport), loc)

	if len(points) != 1 || dir != "import" {
		t.Errorf("expected 1 point and dir=import, got %d points and dir=%s", len(points), dir)
	}

	// Export Case
	csvDataExport := "ignore;Datum bis;Energiemenge in kWh;ignore2;Restnetzueberschuss in kWh;Status\n" +
		"x;17.03.2026 00:15:00;1,23;y;0,45;M\n"

	points, dir = parseCSVToPoints(strings.NewReader(csvDataExport), loc)
	if len(points) != 1 || dir != "export" {
		t.Errorf("expected 1 point and dir=export, got %d points and dir=%s", len(points), dir)
	}
}
