package util

import (
	"log"
	"os"
	"regexp"
	"strings"
)

func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	
	if filePath, ok := os.LookupEnv(key + "_FILE"); ok && filePath != "" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("Warning: Error reading secret from file %s (specified via %s_FILE): %v", filePath, key, err)
			return fallback
		}
		return strings.TrimSpace(string(content))
	}
	
	return fallback
}

func ExtractMeterID(filename string) string {
	re := regexp.MustCompile(`AT\d+`)
	match := re.FindString(filename)
	return match
}

func ShortenMeterID(fullID string) string {
	re := regexp.MustCompile(`AT(\d+)`)
	match := re.FindStringSubmatch(fullID)
	if len(match) > 1 {
		rePrefix := regexp.MustCompile(`^00310+990*`)
		id := rePrefix.ReplaceAllString(match[1], "")
		if id == "" {
			return "0"
		}
		return id
	}
	return fullID
}
