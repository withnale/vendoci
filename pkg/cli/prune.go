package cli

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhodesp/vendoci/pkg/config"
	"github.com/rhodesp/vendoci/pkg/vendor"
)

var (
	pruneDelete bool
	pruneYes    bool
)

// pruneCmd represents the prune command
var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Find orphaned images in the target registry",
	Long: `Find images in the target registry that are not defined in the configuration.
This command compares the images present in your target registry with the images
defined in your configuration and identifies any orphaned images that should not be there.

Examples:
  # List orphaned images (dry run)
  vendoci prune

  # List orphaned images with verbose output
  vendoci prune -v

  # Delete orphaned images (with confirmation prompt)
  vendoci prune --delete

  # Delete orphaned images (skip confirmation - use with caution!)
  vendoci prune --delete --yes

  # Use specific config directory
  vendoci prune --config .vendoci.d`,
	RunE: runPrune,
}

func init() {
	rootCmd.AddCommand(pruneCmd)

	// Flags
	pruneCmd.Flags().BoolVar(&pruneDelete, "delete", false, "Delete orphaned images (default is dry-run)")
	pruneCmd.Flags().BoolVarP(&pruneYes, "yes", "y", false, "Skip confirmation prompt")
}

func runPrune(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := loadConfigForList()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	// Create logger
	logger := log.New(os.Stdout, "", 0)
	if verbose {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}

	// Create vendor instance
	v := vendor.New(cfg, logger)

	// Display configuration info
	fmt.Printf("Configuration: %s\n", cfgFile)
	fmt.Printf("Target Registry: %s\n", cfg.Target.Registry)
	fmt.Printf("Vendor Prefix: %s\n", cfg.Target.Prefix)
	if pruneDelete {
		fmt.Printf("Mode: DELETE (images will be removed)\n")
	} else {
		fmt.Printf("Mode: DRY-RUN (no images will be removed)\n")
	}
	fmt.Println()

	// Step 1: Build list of expected images from config
	expectedImages := buildExpectedImagesList(cfg)

	if verbose {
		fmt.Printf("Expected images from config: %d\n", len(expectedImages))
		for img := range expectedImages {
			fmt.Printf("  - %s\n", img)
		}
		fmt.Println()
	}

	// Step 2: List all images in target registry
	fmt.Println("Scanning target registry...")
	actualImages, err := v.ListTargetImages(ctx)
	if err != nil {
		return fmt.Errorf("listing target images: %w", err)
	}

	if verbose {
		fmt.Printf("Found images in registry: %d\n", len(actualImages))
		for _, img := range actualImages {
			fmt.Printf("  - %s\n", img)
		}
		fmt.Println()
	}

	// Step 3: Find orphaned images (in registry but not in config)
	orphanedImages := findOrphanedImages(expectedImages, actualImages)

	// Display results
	displayPruneResults(orphanedImages, pruneDelete)

	// Step 4: Delete if requested
	if pruneDelete && len(orphanedImages) > 0 {
		// Ask for confirmation unless --yes flag is set
		if !pruneYes {
			confirmed := confirmDeletion(len(orphanedImages))
			if !confirmed {
				fmt.Println("\nDeletion cancelled.")
				return nil
			}
		}

		fmt.Println("\nDeleting orphaned images...")
		deleteResults := deleteOrphanedImages(ctx, v, orphanedImages)
		displayDeleteResults(deleteResults)
	}

	return nil
}

// buildExpectedImagesList creates a set of all expected image references from config
func buildExpectedImagesList(cfg *config.Config) map[string]bool {
	expected := make(map[string]bool)

	// Get all images and charts from config
	groupedImages := cfg.GetAllImages()

	for _, groupedImg := range groupedImages {
		img := groupedImg.Image
		imgType := groupedImg.Type
		chartName := groupedImg.ChartName

		for _, tag := range img.Tags {
			var targetBase string

			if imgType == "helm" {
				// Helm charts: {registry}/{vendor-prefix}/charts/{chartName}
				targetBase = fmt.Sprintf("%s/%s/charts/%s", cfg.Target.Registry, cfg.Target.Prefix, chartName)
			} else {
				// OCI images: use existing logic
				targetBase = generateTarget(cfg, img, imgType, chartName)
			}

			targetImage := fmt.Sprintf("%s:%s", targetBase, tag)
			expected[targetImage] = true
		}
	}

	return expected
}

// findOrphanedImages identifies images in registry that are not in expected list
func findOrphanedImages(expected map[string]bool, actual []string) []string {
	var orphaned []string

	for _, image := range actual {
		if !expected[image] {
			orphaned = append(orphaned, image)
		}
	}

	return orphaned
}

// displayPruneResults shows the orphaned images in a table
func displayPruneResults(orphaned []string, willDelete bool) {
	if len(orphaned) == 0 {
		fmt.Println("✓ No orphaned images found. Registry is clean!")
		return
	}

	action := "To be reviewed"
	if willDelete {
		action = "To be deleted"
	}

	fmt.Printf("Found %d orphaned image(s) - %s:\n\n", len(orphaned), action)

	table := NewTable("ORPHANED_IMAGE")

	for _, image := range orphaned {
		table.AddRow(image)
	}

	table.Render()
}

// DeleteResult represents the result of deleting an image
type DeleteResult struct {
	Image   string
	Success bool
	Error   error
}

// deleteOrphanedImages deletes the orphaned images from registry
func deleteOrphanedImages(ctx context.Context, v *vendor.Vendor, orphaned []string) []DeleteResult {
	var results []DeleteResult

	for _, image := range orphaned {
		err := v.DeleteImage(ctx, image)
		results = append(results, DeleteResult{
			Image:   image,
			Success: err == nil,
			Error:   err,
		})
	}

	return results
}

// displayDeleteResults shows the results of deletion
func displayDeleteResults(results []DeleteResult) {
	success := 0
	failed := 0

	for _, result := range results {
		if result.Success {
			success++
			if verbose {
				fmt.Printf("✓ Deleted: %s\n", result.Image)
			}
		} else {
			failed++
			fmt.Printf("✗ Failed to delete %s: %v\n", result.Image, result.Error)
		}
	}

	fmt.Printf("\nDeletion Summary: %d successful, %d failed\n", success, failed)
}

// confirmDeletion prompts the user to confirm deletion of orphaned images
func confirmDeletion(count int) bool {
	fmt.Printf("\n⚠️  WARNING: You are about to delete %d orphaned image(s) from the registry.\n", count)
	fmt.Println("This action cannot be undone.")
	fmt.Print("\nType 'yes' to confirm deletion: ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("\nError reading input: %v\n", err)
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "yes"
}
