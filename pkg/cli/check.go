package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"

	"github.com/rhodesp/vendoci/pkg/vendor"
)

var (
	parallel int
)

// checkCmd represents the check command
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check the presence of source and destination images",
	Long: `Check whether source and destination images exist in their respective registries.
This command verifies the presence of both source images (in public registries) and
destination images (in your target registry) for all images defined in the configuration.

The output shows:
  - SOURCE: Whether the source image exists (PRESENT or MISSING)
  - DEST: Whether the destination image exists (PRESENT or MISSING)

Examples:
  # Check all images and tags
  vendoci check

  # Check with parallel requests for faster results
  vendoci check --parallel 10

  # Check with verbose output
  vendoci check -v`,
	RunE: runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)

	// Flags
	checkCmd.Flags().IntVar(&parallel, "parallel", 5, "Number of parallel checks to perform")
}

// ImageCheckResult represents the result of checking an image
type ImageCheckResult struct {
	GroupName    string
	SourceImage  string
	TargetImage  string
	Tag          string
	SourceExists bool
	TargetExists bool
	SourceError  error
	TargetError  error
}

func runCheck(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := loadConfigForList()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	// Get all images with groups
	groupedImages := cfg.GetAllImages()

	// Check if there are any images configured
	if len(groupedImages) == 0 {
		fmt.Println("No images configured in config file")
		return nil
	}

	// Display configuration info
	fmt.Printf("Configuration: %s\n", cfgFile)
	fmt.Printf("Target Registry: %s\n", cfg.TargetRegistry)
	fmt.Printf("Vendor Prefix: %s\n", cfg.VendorPrefix)
	fmt.Printf("Parallel Checks: %d\n\n", parallel)

	// Create logger
	logger := log.New(os.Stdout, "", 0)
	if verbose {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}

	// Create vendor instance for checking
	v := vendor.New(cfg, logger)

	// Collect all image:tag combinations to check
	var checks []ImageCheckResult
	for _, groupedImg := range groupedImages {
		img := groupedImg.Image
		group := groupedImg.GroupName
		if group == "" {
			group = "-"
		}

		for _, tag := range img.Tags {
			sourceImage := fmt.Sprintf("%s:%s", img.Source, tag)
			targetBase := generateTarget(cfg, img)
			targetImage := fmt.Sprintf("%s:%s", targetBase, tag)

			checks = append(checks, ImageCheckResult{
				GroupName:   group,
				SourceImage: sourceImage,
				TargetImage: targetImage,
				Tag:         tag,
			})
		}
	}

	if len(checks) == 0 {
		fmt.Println("No images with tags to check")
		return nil
	}

	fmt.Printf("Checking %d image(s)...\n\n", len(checks))

	// Perform checks in parallel
	results := performChecks(ctx, v, checks, parallel)

	// Display results
	displayCheckResults(results)

	return nil
}

func performChecks(ctx context.Context, v *vendor.Vendor, checks []ImageCheckResult, parallelism int) []ImageCheckResult {
	results := make([]ImageCheckResult, len(checks))
	copy(results, checks)

	// Create a semaphore to limit parallelism
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Check source image
			sourceExists, sourceErr := checkImageExists(ctx, v, results[idx].SourceImage)
			results[idx].SourceExists = sourceExists
			results[idx].SourceError = sourceErr

			// Check target image
			targetExists, targetErr := checkImageExists(ctx, v, results[idx].TargetImage)
			results[idx].TargetExists = targetExists
			results[idx].TargetError = targetErr
		}(i)
	}

	wg.Wait()
	return results
}

func checkImageExists(ctx context.Context, v *vendor.Vendor, imageRef string) (bool, error) {
	return v.CheckImageExists(ctx, imageRef)
}

func displayCheckResults(results []ImageCheckResult) {
	table := tablewriter.NewWriter(os.Stdout)
	
	// Set header with uppercase and underscores
	table.Options(tablewriter.WithHeaderAutoFormat(tw.Off))
	table.Header("GROUP", "SOURCE_IMAGE", "TAG", "SOURCE", "DEST", "TARGET_IMAGE")

	presentCount := 0
	missingCount := 0
	sourceOnlyCount := 0
	targetOnlyCount := 0

	for _, result := range results {
		sourceStatus := formatStatus(result.SourceExists, result.SourceError)
		targetStatus := formatStatus(result.TargetExists, result.TargetError)

		table.Append(
			result.GroupName,
			result.SourceImage,
			result.Tag,
			sourceStatus,
			targetStatus,
			result.TargetImage,
		)

		// Count statuses
		if result.SourceExists && result.TargetExists {
			presentCount++
		} else if !result.SourceExists && !result.TargetExists {
			missingCount++
		} else if result.SourceExists && !result.TargetExists {
			sourceOnlyCount++
		} else if !result.SourceExists && result.TargetExists {
			targetOnlyCount++
		}
	}

	table.Render()

	// Summary
	fmt.Printf("\nSummary:\n")
	fmt.Printf("  Both present: %d\n", presentCount)
	fmt.Printf("  Source only: %d (needs sync)\n", sourceOnlyCount)
	fmt.Printf("  Target only: %d (orphaned)\n", targetOnlyCount)
	fmt.Printf("  Both missing: %d\n", missingCount)
	fmt.Printf("  Total: %d\n", len(results))

	// Verbose error reporting
	if verbose {
		hasErrors := false
		for _, result := range results {
			if result.SourceError != nil || result.TargetError != nil {
				if !hasErrors {
					fmt.Printf("\nErrors encountered:\n")
					hasErrors = true
				}
				if result.SourceError != nil {
					fmt.Printf("  %s (source): %v\n", result.SourceImage, result.SourceError)
				}
				if result.TargetError != nil {
					fmt.Printf("  %s (target): %v\n", result.TargetImage, result.TargetError)
				}
			}
		}
	}
}

func formatStatus(exists bool, err error) string {
	if err != nil {
		if verbose {
			return "ERROR"
		}
		return "UNKNOWN"
	}
	if exists {
		return "PRESENT"
	}
	return "MISSING"
}

// Dummy - file end
