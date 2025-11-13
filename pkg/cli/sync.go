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
	dryRun     bool
	force      bool
	configFile string
	images     []string
	tags       []string
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
  vendoci sync --force`,
	RunE: runSync,
}

func init() {
	rootCmd.AddCommand(syncCmd)

	// Flags
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be synced without actually doing it")
	syncCmd.Flags().BoolVar(&force, "force", false, "Force re-sync even if target images already exist")
	syncCmd.Flags().StringSliceVar(&images, "images", []string{}, "Comma-separated list of specific images to sync (overrides config)")
	syncCmd.Flags().StringSliceVar(&tags, "tags", []string{}, "Comma-separated list of specific tags to sync (requires --images)")

	// Bind flags to viper
	viper.BindPFlag("sync.dry-run", syncCmd.Flags().Lookup("dry-run"))
	viper.BindPFlag("sync.force", syncCmd.Flags().Lookup("force"))
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := loadConfigForSync()
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
	printSyncSummary(cfg, opts)

	// Perform sync
	results, err := v.SyncAll(ctx, opts)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	// Print results
	printSyncResults(results, opts)

	return nil
}

func loadConfigForSync() (*config.Config, error) {
	// Try to get config file from viper
	configPath := viper.GetString("config")

	// If no config file specified, try default locations
	if configPath == "" {
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

	if configPath == "" {
		return nil, fmt.Errorf("no configuration file found. Please create .vendoci.yaml or specify --config")
	}

	return config.LoadConfig(configPath)
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

func printSyncSummary(cfg *config.Config, opts vendor.SyncOptions) {
	fmt.Printf("VendOCI Sync Summary\n")
	fmt.Printf("===================\n")
	fmt.Printf("Target Registry: %s\n", cfg.TargetRegistry)
	fmt.Printf("Vendor Prefix: %s\n", cfg.VendorPrefix)
	fmt.Printf("Dry Run: %t\n", opts.DryRun)
	fmt.Printf("Force: %t\n", opts.Force)

	// Count all images from all sources
	allImages := cfg.GetAllImages()
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
