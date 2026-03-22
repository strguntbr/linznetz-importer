package state

import (
	"encoding/json"
	"log"
	"os"

	"linznetz-import/internal/models"
)

func Load(path string) *models.State {
	s := &models.State{Meters: make(map[string]models.MeterState)}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	json.NewDecoder(f).Decode(s)
	if s.Meters == nil {
		s.Meters = make(map[string]models.MeterState)
	}
	return s
}

func Save(path string, s *models.State) {
	f, err := os.Create(path)
	if err != nil {
		log.Printf("Error saving state: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.Encode(s)
}
