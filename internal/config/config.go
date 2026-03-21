package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/term"
)

var IsInteractive bool

func init() {
	IsInteractive = term.IsTerminal(int(os.Stdin.Fd()))
}

func RequireEnv(key string, sensitive bool) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}

	if !IsInteractive {
		log.Fatalf("Missing required environment variable: %s", key)
	}

	fmt.Printf("Please enter %s: ", key)
	if sensitive {
		byteVal, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalf("Error reading %s: %v", key, err)
		}
		fmt.Println() // Newline after password
		val = string(byteVal)
	} else {
		fmt.Scanln(&val)
	}

	if val == "" {
		log.Fatalf("Required environment variable %s cannot be empty.", key)
	}
	os.Setenv(key, val)
	return val
}

func ParseDateEnv(key string, loc *time.Location) *time.Time {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	formats := []string{"2006-01-02", "02.01.2006"}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, val, loc); err == nil {
			return &t
		}
	}
	log.Printf("Warning: Invalid date format in %s: %s", key, val)
	return nil
}
