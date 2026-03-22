package util

import (
	"os"
	"testing"
)

func TestGetEnvWithFile(t *testing.T) {
	key := "TEST_VAR"
	keyFile := key + "_FILE"
	val := "direct_value"
	fileVal := "file_value"
	tmpPath := "test_secret.txt"

	// Cleanup
	os.Unsetenv(key)
	os.Unsetenv(keyFile)
	os.Remove(tmpPath)
	defer os.Unsetenv(key)
	defer os.Unsetenv(keyFile)
	defer os.Remove(tmpPath)

	// Test 1: Fallback
	if got := GetEnv(key, "fallback"); got != "fallback" {
		t.Errorf("GetEnv() = %q; want %q", got, "fallback")
	}

	// Test 2: Direct value
	os.Setenv(key, val)
	if got := GetEnv(key, "fallback"); got != val {
		t.Errorf("GetEnv() = %q; want %q", got, val)
	}

	// Test 3: File value (direct unset)
	os.Unsetenv(key)
	os.WriteFile(tmpPath, []byte(fileVal+"\n "), 0644) // test trimming
	os.Setenv(keyFile, tmpPath)
	if got := GetEnv(key, "fallback"); got != fileVal {
		t.Errorf("GetEnv() = %q; want %q", got, fileVal)
	}

	// Test 4: Direct takes precedence over file
	os.Setenv(key, val)
	if got := GetEnv(key, "fallback"); got != val {
		t.Errorf("GetEnv() = %q; want %q", got, val)
	}

	// Test 5: Non-existent file
	os.Unsetenv(key)
	os.Setenv(keyFile, "non_existent_file.txt")
	if got := GetEnv(key, "fallback"); got != "fallback" {
		t.Errorf("GetEnv() = %q; want %q", got, "fallback")
	}
}
