package config

import (
	"os"
	"strings"
	"testing"
)

func TestSaveAccessTokenToEnv(t *testing.T) {
	// Create a temp file
	tmpFile, err := os.CreateTemp("", "test_env_*.env")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	tmpFile.Close()

	// Test 1: Save to non-existing or empty file
	token1 := "token_xyz_123"
	err = SaveAccessTokenToEnv(tmpPath, token1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}

	if !strings.Contains(string(content), "KITE_ACCESS_TOKEN="+token1) {
		t.Errorf("expected file to contain KITE_ACCESS_TOKEN=%s, got %s", token1, string(content))
	}

	// Test 2: Update existing token in file
	token2 := "token_abc_789"
	err = SaveAccessTokenToEnv(tmpPath, token2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	content, err = os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}

	if !strings.Contains(string(content), "KITE_ACCESS_TOKEN="+token2) {
		t.Errorf("expected file to contain updated KITE_ACCESS_TOKEN=%s, got %s", token2, string(content))
	}

	// Verify it didn't duplicate the entry
	count := strings.Count(string(content), "KITE_ACCESS_TOKEN=")
	if count != 1 {
		t.Errorf("expected exactly 1 KITE_ACCESS_TOKEN entry, got %d", count)
	}
}
