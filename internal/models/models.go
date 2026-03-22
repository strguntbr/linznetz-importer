package models

import "time"

type DataPoint struct {
	Time   time.Time
	Total  float64
	Rest   *float64
	EEG    *float64
	Status string
}

type MeterState struct {
	LatestFinal        time.Time `json:"latest_final"`
	LatestIntermediate time.Time `json:"latest_intermediate"`
}

type State struct {
	Meters map[string]MeterState `json:"meters"`
}

type MeterInfo struct {
	RadioID string `json:"radio_id"`
	MeterID string `json:"meter_id"`
	Label   string `json:"label"`
}
