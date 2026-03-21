package util

import (
	"os"
	"regexp"
)

func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
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
