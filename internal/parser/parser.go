package parser

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"

	"linznetz-import/internal/models"
)

func ParseCSVToPoints(r io.Reader, loc *time.Location) ([]models.DataPoint, string) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.LazyQuotes = true
	
	header, err := reader.Read() // skip header
	if err != nil { return nil, "import" }

	direction := "import"
	if len(header) >= 5 && strings.Contains(header[4], "Restnetzueberschuss") {
		direction = "export"
	}

	var points []models.DataPoint
	for {
		record, err := reader.Read()
		if err == io.EOF { break }
		if len(record) < 6 { continue }
		
		layoutWithSec := "02.01.2006 15:04:05"
		layoutWithoutSec := "02.01.2006 15:04"
		
		t, err := time.ParseInLocation(layoutWithSec, record[1], loc)
		if err != nil {
			t, err = time.ParseInLocation(layoutWithoutSec, record[1], loc)
			if err != nil {
				continue
			}
		}
		
		total := 0.0
		if record[2] != "" {
			total, _ = parseCommaFloat(record[2])
		}

		var restPtr, eegPtr *float64
		if record[4] != "" {
			rest, _ := parseCommaFloat(record[4])
			restPtr = &rest
			eeg := total - rest
			eegPtr = &eeg
		}

		status := record[5]

		points = append(points, models.DataPoint{
			Time:   t,
			Total:  total,
			Rest:   restPtr,
			EEG:    eegPtr,
			Status: status,
		})
	}
	return points, direction
}

func parseCommaFloat(s string) (float64, error) {
	s = strings.Replace(s, ",", ".", 1)
	return strconv.ParseFloat(s, 64)
}
