package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/rhodesp/vendoci/pkg/config"
	"github.com/rhodesp/vendoci/pkg/vendor"
)

var (
	parallel          int
	checkFilterType   string
	errorsOnly        bool
	checkFilterGroups []string
	checkLong         bool
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
  vendoci check -v

  # Check only OCI images
  vendoci check --type oci

  # Check only Helm charts
  vendoci check --type helm

  # Show only images with errors (missing from source or dest)
  vendoci check --errors
  vendoci check -E

  # Check only specific groups
  vendoci check --groups alpine
  vendoci check -G alpine -G busybox

  # Show the full target path (registry and prefix included)
  vendoci check --long
  vendoci check -l

  # Combine filters
  vendoci check --type oci --groups alpine --errors`,
	RunE: runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)

	// Flags
	checkCmd.Flags().IntVar(&parallel, "parallel", 5, "Number of parallel checks to perform")
	checkCmd.Flags().StringVar(&checkFilterType, "type", "", "Filter by type: oci or helm (default: show all)")
	checkCmd.Flags().BoolVarP(&errorsOnly, "errors", "E", false, "Show only images with errors (missing from source or dest)")
	checkCmd.Flags().StringSliceVarP(&checkFilterGroups, "groups", "G", []string{}, "Filter by group names (can specify multiple times)")
	checkCmd.Flags().BoolVarP(&checkLong, "long", "l", false, "Show the full target path, including the target registry and prefix")
}

// ImageCheckResult represents the result of checking an image
type ImageCheckResult struct {
	GroupName    string
	Name         string // Image basename or chart name
	SourceImage  string
	TargetImage  string
	Tag          string
	Type         string
	ChartName    string // For helm charts, the chart name
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

	// Filter by groups if specified
	if len(checkFilterGroups) > 0 {
		groupedImages = filterImagesByGroups(groupedImages, checkFilterGroups)
	}

	// Filter by type if specified
	if checkFilterType != "" {
		groupedImages = filterImagesByType(groupedImages, checkFilterType)
	}

	// Check if there are any images configured
	if len(groupedImages) == 0 {
		if len(checkFilterGroups) > 0 && checkFilterType != "" {
			fmt.Printf("No images of type '%s' in groups %v configured in config file\n", checkFilterType, checkFilterGroups)
		} else if len(checkFilterGroups) > 0 {
			fmt.Printf("No images in groups %v configured in config file\n", checkFilterGroups)
		} else if checkFilterType != "" {
			fmt.Printf("No images of type '%s' configured in config file\n", checkFilterType)
		} else {
			fmt.Println("No images configured in config file")
		}
		return nil
	}

	// Display configuration info
	fmt.Printf("Configuration: %s\n", cfgFile)
	fmt.Printf("Target Registry: %s\n", cfg.Target.Registry)
	fmt.Printf("Vendor Prefix: %s\n", cfg.Target.Prefix)
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
		imgType := groupedImg.Type
		chartName := groupedImg.ChartName
		if group == "" {
			group = "-"
		}

		// Determine the name to display
		var name string
		if imgType == "helm" {
			name = chartName
		} else {
			name = config.GetImageBasename(img.Source)
		}

		for _, tag := range img.Tags {
			// For Helm charts, don't append version to source (it's a repo URL)
			// For OCI images, append the tag
			var sourceImage string
			if imgType == "helm" {
				sourceImage = img.Source
			} else {
				sourceImage = fmt.Sprintf("%s:%s", img.Source, tag)
			}

			targetBase := generateTarget(cfg, img, imgType, chartName)
			targetImage := fmt.Sprintf("%s:%s", targetBase, tag)

			checks = append(checks, ImageCheckResult{
				GroupName:   group,
				Name:        name,
				SourceImage: sourceImage,
				TargetImage: targetImage,
				Tag:         tag,
				Type:        imgType,
				ChartName:   chartName,
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

	// Filter results if --errors flag is set
	if errorsOnly {
		results = filterErrorResults(results)
	}

	// Display results
	displayCheckResults(cfg, results, errorsOnly)

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

			// Check based on type
			if results[idx].Type == "helm" {
				// For helm charts, use CheckHelmChartExists
				sourceExists, sourceErr := checkHelmChartExists(ctx, v, results[idx].SourceImage, results[idx].ChartName, results[idx].Tag)
				results[idx].SourceExists = sourceExists
				results[idx].SourceError = sourceErr

				// Target check for helm charts (still uses OCI check as charts are stored as OCI artifacts)
				targetExists, targetErr := checkTargetImageExists(ctx, v, results[idx].TargetImage)
				results[idx].TargetExists = targetExists
				results[idx].TargetError = targetErr
			} else {
				// For OCI images, use CheckSourceImageExists (source proxy/TLS/auth settings)
				sourceExists, sourceErr := checkSourceImageExists(ctx, v, results[idx].SourceImage)
				results[idx].SourceExists = sourceExists
				results[idx].SourceError = sourceErr

				// Check target image (target proxy/TLS/auth settings)
				targetExists, targetErr := checkTargetImageExists(ctx, v, results[idx].TargetImage)
				results[idx].TargetExists = targetExists
				results[idx].TargetError = targetErr
			}
		}(i)
	}

	wg.Wait()
	return results
}

func checkSourceImageExists(ctx context.Context, v *vendor.Vendor, imageRef string) (bool, error) {
	return v.CheckSourceImageExists(ctx, imageRef)
}

func checkTargetImageExists(ctx context.Context, v *vendor.Vendor, imageRef string) (bool, error) {
	return v.CheckImageExists(ctx, imageRef)
}

func checkHelmChartExists(ctx context.Context, v *vendor.Vendor, repoURL, chartName, version string) (bool, error) {
	// Parse the source to extract just the repo URL
	// Format is: repoURL:version (e.g., "https://charts.jetstack.io:v1.15.2")
	// We need to remove the version suffix

	// Remove the version suffix from the URL
	colonIdx := strings.LastIndex(repoURL, ":")
	cleanRepoURL := repoURL
	if colonIdx > 0 {
		// Make sure this is not part of the URL scheme (http:// or https://)
		if colonIdx > 8 { // len("https://") = 8
			cleanRepoURL = repoURL[:colonIdx]
		}
	}

	// Check if the chart exists in the repository
	return v.CheckHelmChartExists(ctx, cleanRepoURL, chartName, version)
}

func filterErrorResults(results []ImageCheckResult) []ImageCheckResult {
	var filtered []ImageCheckResult
	for _, result := range results {
		// Include if source is missing OR dest is missing (or has error)
		if !result.SourceExists || !result.TargetExists {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func displayCheckResults(cfg *config.Config, results []ImageCheckResult, showingErrorsOnly bool) {
	table := NewTable("GROUP", "NAME", "SOURCE_PATH", "VERSION", "TYPE", "SOURCE", "DEST", "TARGET_PATH")

	presentCount := 0
	missingCount := 0
	sourceOnlyCount := 0
	targetOnlyCount := 0

	for _, result := range results {
		sourceStatus := formatStatus(result.SourceExists, result.SourceError)
		targetStatus := formatStatus(result.TargetExists, result.TargetError)

		targetPath := result.TargetImage
		if !checkLong {
			targetPath = shortTargetPath(cfg, targetPath)
		}

		table.AddRow(
			result.GroupName,
			result.Name,
			result.SourceImage,
			result.Tag,
			result.Type,
			sourceStatus,
			targetStatus,
			targetPath,
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
	if showingErrorsOnly {
		fmt.Printf("  Showing only errors (missing images)\n")
	}
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

// shortTargetPath strips the target registry and prefix from a full target
// image reference, leaving just the path (and tag) underneath them.
// e.g. "registry.company.com/vendor/docker.io/ubuntu:22.04" -> "docker.io/ubuntu:22.04"
func shortTargetPath(cfg *config.Config, targetImage string) string {
	prefix := fmt.Sprintf("%s/%s/", cfg.Target.Registry, cfg.Target.Prefix)
	if strings.HasPrefix(targetImage, prefix) {
		return strings.TrimPrefix(targetImage, prefix)
	}
	return targetImage
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
