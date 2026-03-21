package state

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"linznetz-import/internal/models"
)

func Load(path string) *models.State {
	s := &models.State{LatestDates: make(map[string]time.Time)}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	json.NewDecoder(f).Decode(s)
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
