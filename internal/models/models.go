package models

import "time"

type DataPoint struct {
	Time   time.Time
	Total  float64
	Rest   *float64
	EEG    *float64
	Status string
}

type State struct {
	LatestDates map[string]time.Time `json:"latest_dates"`
}

type MeterInfo struct {
	RadioID string `json:"radio_id"`
	MeterID string `json:"meter_id"`
	Label   string `json:"label"`
}
