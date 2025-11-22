package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/rhodesp/vendoci/pkg/config"
	"github.com/rhodesp/vendoci/pkg/vendor"
)

var (
	dryRun           bool
	force            bool
	configFile       string
	images           []string
	tags             []string
	syncFilterType   string
	syncFilterGroups []string
)

// syncCmd represents the sync command
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync OCI images to the target registry",
	Long: `Sync OCI images from public registries to your private registry with vendoring.

Examples:
  # Sync all images defined in config
  vendoci sync

  # Sync specific images
  vendoci sync --images docker.io/ubuntu,docker.io/nginx

  # Sync with specific tags
  vendoci sync --images docker.io/ubuntu --tags latest,20.04

  # Dry run to see what would be synced
  vendoci sync --dry-run

  # Force re-sync even if images exist
  vendoci sync --force

  # Sync only OCI images
  vendoci sync --type oci

  # Sync only Helm charts
  vendoci sync --type helm

  # Sync only specific groups
  vendoci sync --groups alpine
  vendoci sync -G alpine -G busybox

  # Combine filters
  vendoci sync --type oci --groups alpine --dry-run`,
	RunE: runSync,
}

func init() {
	rootCmd.AddCommand(syncCmd)

	// Flags
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be synced without actually doing it")
	syncCmd.Flags().BoolVar(&force, "force", false, "Force re-sync even if target images already exist")
	syncCmd.Flags().StringSliceVar(&images, "images", []string{}, "Comma-separated list of specific images to sync (overrides config)")
	syncCmd.Flags().StringSliceVar(&tags, "tags", []string{}, "Comma-separated list of specific tags to sync (requires --images)")
	syncCmd.Flags().StringVar(&syncFilterType, "type", "", "Filter by type: oci or helm (default: sync all)")
	syncCmd.Flags().StringSliceVarP(&syncFilterGroups, "groups", "G", []string{}, "Filter by group names (can specify multiple times)")

	// Bind flags to viper
	viper.BindPFlag("sync.dry-run", syncCmd.Flags().Lookup("dry-run"))
	viper.BindPFlag("sync.force", syncCmd.Flags().Lookup("force"))
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration
	cfg, configPath, err := loadConfigForSync()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Override configuration if specific images are provided
	if len(images) > 0 {
		cfg = overrideConfigWithFlags(cfg)
	}

	// Determine requested groups for filtering
	var requestedGroups []string
	if len(syncFilterGroups) > 0 && len(images) == 0 {
		requestedGroups = syncFilterGroups
	}

	// Filter by type if specified (unless using --images override)
	if syncFilterType != "" && len(images) == 0 {
		cfg = filterConfigByType(cfg, syncFilterType)
	}

	// Check if we have any images left after filtering
	if len(images) == 0 {
		groupedImages := cfg.GetAllImagesFiltered(requestedGroups)
		if len(groupedImages) == 0 {
			if len(syncFilterGroups) > 0 && syncFilterType != "" {
				fmt.Printf("No images of type '%s' in groups %v to sync\n", syncFilterType, syncFilterGroups)
			} else if len(syncFilterGroups) > 0 {
				fmt.Printf("No images in groups %v to sync\n", syncFilterGroups)
			} else if syncFilterType != "" {
				fmt.Printf("No images of type '%s' to sync\n", syncFilterType)
			} else {
				fmt.Printf("No images to sync (all groups may have sync: false)\n")
			}
			return nil
		}
	}

	// Create logger
	logger := log.New(os.Stdout, "", log.LstdFlags)
	if !viper.GetBool("verbose") {
		logger = log.New(os.Stdout, "", 0) // No timestamp for non-verbose
	}

	// Create vendor
	v := vendor.New(cfg, logger)

	// Setup sync options
	opts := vendor.SyncOptions{
		DryRun:  dryRun,
		Verbose: viper.GetBool("verbose"),
		Force:   force,
	}

	// Print configuration summary
	printSyncSummary(cfg, configPath, opts, requestedGroups)

	// Perform sync
	results, err := v.SyncAllWithGroups(ctx, opts, requestedGroups)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	// Print results
	printSyncResults(results, opts)

	return nil
}

func loadConfigForSync() (*config.Config, string, error) {
	// Use the global cfgFile variable set by the --config flag
	configPath := cfgFile

	// If no config file specified, try default locations
	if configPath == "" {
		// First, check for VENDOCI_CONFIG environment variable
		if envConfig := os.Getenv("VENDOCI_CONFIG"); envConfig != "" {
			configPath = envConfig
		} else {
			// Try current directory first
			if _, err := os.Stat(".vendoci.yaml"); err == nil {
				configPath = ".vendoci.yaml"
			} else if _, err := os.Stat("vendoci.yaml"); err == nil {
				configPath = "vendoci.yaml"
			} else {
				// Try home directory
				home, err := os.UserHomeDir()
				if err == nil {
					homeConfig := home + "/.vendoci.yaml"
					if _, err := os.Stat(homeConfig); err == nil {
						configPath = homeConfig
					}
				}
			}
		}
	}

	if configPath == "" {
		return nil, "", fmt.Errorf("no configuration file found. Please create .vendoci.yaml, specify --config, or set VENDOCI_CONFIG environment variable")
	}

	cfg, err := config.LoadConfig(configPath)
	return cfg, configPath, err
}

func validateConfig(cfg *config.Config) error {
	if cfg.TargetRegistry == "" {
		return fmt.Errorf("target_registry must be specified")
	}

	// Check if there are any images configured
	hasImages := len(cfg.ImagesArray) > 0 || len(cfg.ImagesMap) > 0 || len(cfg.Groups) > 0
	if !hasImages && len(images) == 0 {
		return fmt.Errorf("no images specified in config or command line")
	}

	return nil
}

func overrideConfigWithFlags(cfg *config.Config) *config.Config {
	// Create new config with command line overrides
	newCfg := *cfg
	newCfg.ImagesArray = nil
	newCfg.ImagesMap = nil

	// If tags are specified but images aren't, return error
	if len(tags) > 0 && len(images) == 0 {
		fmt.Fprintf(os.Stderr, "Warning: --tags specified but no --images provided, ignoring tags\n")
	}

	// Create image configs from command line
	for _, image := range images {
		imageConfig := config.ImageConfig{
			Source: strings.TrimSpace(image),
		}

		// Use specified tags or default to "latest"
		if len(tags) > 0 {
			imageConfig.Tags = make([]string, len(tags))
			for i, tag := range tags {
				imageConfig.Tags[i] = strings.TrimSpace(tag)
			}
		} else {
			imageConfig.Tags = []string{"latest"}
		}

		newCfg.ImagesArray = append(newCfg.ImagesArray, imageConfig)
	}

	return &newCfg
}

// filterConfigByType filters the config to only include images of the specified type
func filterConfigByType(cfg *config.Config, imageType string) *config.Config {
	newCfg := *cfg
	newCfg.Groups = make(map[string]config.GroupConfig)

	for groupName, group := range cfg.Groups {
		newGroup := config.GroupConfig{}

		// OCI images are in the Images array
		if imageType == "oci" {
			newGroup.Images = group.Images
		}

		// Helm charts are in the Charts array
		if imageType == "helm" {
			newGroup.Charts = group.Charts
		}

		// Only add group if it has items
		if len(newGroup.Images) > 0 || len(newGroup.Charts) > 0 {
			newCfg.Groups[groupName] = newGroup
		}
	}

	return &newCfg
}

// filterConfigByGroups filters the config to only include specified groups
func filterConfigByGroups(cfg *config.Config, groups []string) *config.Config {
	newCfg := *cfg
	newCfg.Groups = make(map[string]config.GroupConfig)

	// Create a map for O(1) lookups
	groupMap := make(map[string]bool)
	for _, group := range groups {
		groupMap[group] = true
	}

	for groupName, group := range cfg.Groups {
		if groupMap[groupName] {
			newCfg.Groups[groupName] = group
		}
	}

	return &newCfg
}

func printSyncSummary(cfg *config.Config, configPath string, opts vendor.SyncOptions, requestedGroups []string) {
	fmt.Printf("VendOCI Sync Summary\n")
	fmt.Printf("===================\n")
	fmt.Printf("Configuration: %s\n", configPath)
	fmt.Printf("Target Registry: %s\n", cfg.TargetRegistry)
	fmt.Printf("Vendor Prefix: %s\n", cfg.VendorPrefix)
	fmt.Printf("Dry Run: %t\n", opts.DryRun)
	fmt.Printf("Force: %t\n", opts.Force)

	// Show requested groups if any
	if len(requestedGroups) > 0 {
		fmt.Printf("Requested Groups: %s\n", strings.Join(requestedGroups, ", "))
	}

	// Count all images from all sources (with group filtering)
	allImages := cfg.GetAllImagesFiltered(requestedGroups)
	fmt.Printf("Images to sync: %d\n", len(allImages))
	fmt.Println()

	if opts.Verbose {
		fmt.Println("Image Details:")
		for i, groupedImg := range allImages {
			img := groupedImg.Image
			groupLabel := ""
			if groupedImg.GroupName != "" {
				groupLabel = fmt.Sprintf(" [%s]", groupedImg.GroupName)
			}
			fmt.Printf("  %d. %s%s (tags: %s)\n", i+1, img.Source, groupLabel, strings.Join(img.Tags, ", "))
		}
		fmt.Println()
	}
}

func printSyncResults(results []vendor.SyncResult, opts vendor.SyncOptions) {
	fmt.Printf("Sync Results\n")
	fmt.Printf("============\n")

	success := 0
	failed := 0
	skipped := 0

	for _, result := range results {
		switch result.Status {
		case vendor.SyncStatusSuccess:
			success++
			if opts.Verbose {
				fmt.Printf("✓ %s -> %s\n", result.Source, result.Target)
			}
		case vendor.SyncStatusFailed:
			failed++
			fmt.Printf("✗ %s -> %s (ERROR: %v)\n", result.Source, result.Target, result.Error)
		case vendor.SyncStatusSkipped:
			skipped++
			if opts.Verbose {
				fmt.Printf("- %s -> %s (SKIPPED: %s)\n", result.Source, result.Target, result.SkipReason)
			}
		}
	}

	fmt.Printf("\nSummary: %d successful, %d failed, %d skipped\n", success, failed, skipped)

	if failed > 0 {
		fmt.Printf("\nSome images failed to sync. Use --verbose for more details.\n")
		os.Exit(1)
	}
}
