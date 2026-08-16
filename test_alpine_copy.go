package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/rhodesp/vendoci/pkg/config"
	"github.com/rhodesp/vendoci/pkg/vendor"
)

func main() {
	fmt.Println("🧪 Testing Alpine 3.23.0 copy to ghcr.io/poptart-testing/poptart")
	fmt.Println("=" + fmt.Sprint(60))

	// Check for GitHub token
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		fmt.Println("❌ GITHUB_TOKEN environment variable not set")
		fmt.Println("Set it with: export GITHUB_TOKEN=your_token")
		os.Exit(1)
	}

	// Create configuration
	cfg := &config.Config{
		Target: config.TargetConfig{
			Registry: "ghcr.io/poptart-testing/poptart",
			Prefix:   "vendor",
			Auth: config.AuthConfig{
				Username: "poptart-testing",
				Password: githubToken,
			},
		},
		Images: []config.ImageConfig{
			{
				Source: "docker.io/alpine",
				Tags:   []string{"3.23.0"},
			},
		},
	}

	// Create logger
	logger := log.New(os.Stdout, "", log.LstdFlags)

	// Create vendor instance
	v := vendor.New(cfg, logger)

	// Determine if this should be a dry run
	dryRun := len(os.Args) > 1 && os.Args[1] == "--dry-run"

	// Setup options
	opts := vendor.SyncOptions{
		DryRun:  dryRun,
		Verbose: true,
		Force:   true,
	}

	expectedSource := "docker.io/alpine:3.23.0"
	expectedTarget := "ghcr.io/poptart-testing/poptart/vendor/docker.io.alpine:3.23.0"

	if dryRun {
		fmt.Printf("🧪 DRY RUN: Would copy %s -> %s\n\n", expectedSource, expectedTarget)
	} else {
		fmt.Printf("🚀 LIVE RUN: Copying %s -> %s\n\n", expectedSource, expectedTarget)
	}

	// Run the sync
	ctx := context.Background()
	results, err := v.SyncAll(ctx, opts)
	if err != nil {
		fmt.Printf("❌ Sync failed: %v\n", err)
		os.Exit(1)
	}

	// Check results
	if len(results) != 1 {
		fmt.Printf("❌ Expected 1 result, got %d\n", len(results))
		os.Exit(1)
	}

	result := results[0]
	fmt.Println("\n📊 Results:")
	fmt.Printf("  Source: %s\n", result.Source)
	fmt.Printf("  Target: %s\n", result.Target)
	fmt.Printf("  Tag: %s\n", result.Tag)
	fmt.Printf("  Status: %s\n", result.Status)

	if result.Error != nil {
		fmt.Printf("  Error: %v\n", result.Error)
	}

	if result.Skipped {
		fmt.Printf("  Skip Reason: %s\n", result.SkipReason)
	}

	switch result.Status {
	case vendor.SyncStatusSuccess:
		if dryRun {
			fmt.Println("\n✅ Dry run successful! The copy operation would work.")
		} else {
			fmt.Println("\n🎉 SUCCESS! Alpine 3.23.0 has been copied to ghcr.io/poptart-testing/poptart/vendor/docker.io.alpine:3.23.0")
		}
	case vendor.SyncStatusSkipped:
		fmt.Println("\n⚠️  Sync was skipped:", result.SkipReason)
	case vendor.SyncStatusFailed:
		fmt.Printf("\n❌ Sync failed: %v\n", result.Error)
		os.Exit(1)
	}
}
