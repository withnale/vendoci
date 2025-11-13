package vendor

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/rhodesp/vendoci/pkg/config"
)

// TestAlpineImageCopyToGHCR tests copying alpine:3.23.0 directly using vendor package
func TestAlpineImageCopyToGHCR(t *testing.T) {
	// Check for required environment variables
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("Skipping test: GITHUB_TOKEN environment variable not set")
	}

	// Create test configuration
	cfg := &config.Config{
		TargetRegistry: "ghcr.io/poptart-testing/poptart",
		VendorPrefix:   "vendor",
		Auth: config.AuthConfig{
			Username: "poptart-testing",
			Password: githubToken,
		},
		Images: []config.ImageConfig{
			{
				Source: "docker.io/alpine",
				Tags:   []string{"3.23.0"},
			},
		},
	}

	// Create logger for test output
	logger := log.New(os.Stdout, "[VENDOR-TEST] ", log.LstdFlags)

	// Create vendor instance directly
	v := New(cfg, logger)

	// Test configuration
	ctx := context.Background()
	opts := SyncOptions{
		DryRun:  false, // Set to true for dry-run
		Verbose: true,
		Force:   true, // Force re-sync even if image exists
	}

	expectedSource := "docker.io/alpine:3.23.0"
	expectedTarget := "ghcr.io/poptart-testing/poptart/vendor/docker.io.alpine:3.23.0"

	t.Logf("Testing direct vendor copy: %s -> %s", expectedSource, expectedTarget)

	// Call SyncAll directly on the vendor instance
	results, err := v.SyncAll(ctx, opts)
	if err != nil {
		t.Fatalf("Vendor SyncAll failed: %v", err)
	}

	// Verify results
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]

	// Validate the result
	if result.Source != expectedSource {
		t.Errorf("Expected source %s, got %s", expectedSource, result.Source)
	}

	if result.Target != expectedTarget {
		t.Errorf("Expected target %s, got %s", expectedTarget, result.Target)
	}

	if result.Status != SyncStatusSuccess {
		t.Fatalf("Expected status %s, got %s. Error: %v", SyncStatusSuccess, result.Status, result.Error)
	}

	if result.Tag != "3.23.0" {
		t.Errorf("Expected tag 3.23.0, got %s", result.Tag)
	}

	t.Logf("✅ Success! Copied %s -> %s", result.Source, result.Target)
}

// TestAlpineImageCopyDryRun tests the same but with dry-run (no auth needed)
func TestAlpineImageCopyDryRun(t *testing.T) {
	cfg := &config.Config{
		TargetRegistry: "ghcr.io/poptart-testing/poptart",
		VendorPrefix:   "vendor",
		Images: []config.ImageConfig{
			{
				Source: "docker.io/alpine",
				Tags:   []string{"3.23.0"},
			},
		},
	}

	logger := log.New(os.Stdout, "[DRY-RUN] ", log.LstdFlags)
	v := New(cfg, logger)

	ctx := context.Background()
	opts := SyncOptions{
		DryRun:  true, // Dry run - no actual copy
		Verbose: true,
		Force:   false,
	}

	expectedTarget := "ghcr.io/poptart-testing/poptart/vendor/docker.io.alpine:3.23.0"

	t.Logf("Testing dry-run vendor copy to: %s", expectedTarget)

	results, err := v.SyncAll(ctx, opts)
	if err != nil {
		t.Fatalf("Dry-run failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]
	if result.Status != SyncStatusSuccess {
		t.Errorf("Dry-run should succeed, got status: %s, error: %v", result.Status, result.Error)
	}

	t.Logf("✅ Dry-run success: Would copy to %s", result.Target)
}

// TestTargetImageGeneration tests the image name transformation
func TestTargetImageGeneration(t *testing.T) {
	cfg := &config.Config{
		TargetRegistry: "ghcr.io/poptart-testing/poptart",
		VendorPrefix:   "vendor",
	}

	source := "docker.io/alpine"
	expected := "ghcr.io/poptart-testing/poptart/vendor/docker.io.alpine"

	result := cfg.GenerateTargetImage(source)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}

	t.Logf("✅ Target generation works: %s -> %s", source, result)
}