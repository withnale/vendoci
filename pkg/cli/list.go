package cli

import (
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"

	"github.com/rhodesp/vendoci/pkg/config"
)

var (
	showTargets bool
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all images and tags defined in the config file",
	Long: `List all images and tags that are defined in the configuration file.
This shows what would be synced when running the sync command.

Examples:
  # List all images and tags
  vendoci list

  # List with target image names
  vendoci list --show-targets`,
	RunE: runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Flags
	listCmd.Flags().BoolVar(&showTargets, "show-targets", false, "Show target image names in addition to source images")
}

func runList(cmd *cobra.Command, args []string) error {
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
	fmt.Printf("Vendor Prefix: %s\n\n", cfg.VendorPrefix)

	// Create table
	table := tablewriter.NewWriter(os.Stdout)

	// Set headers with uppercase and underscores
	if showTargets {
		table.Options(tablewriter.WithHeaderAutoFormat(tw.Off))
		table.Header("GROUP", "SOURCE_PATH", "VERSION", "TYPE", "TARGET_PATH")
	} else {
		table.Options(tablewriter.WithHeaderAutoFormat(tw.Off))
		table.Header("GROUP", "SOURCE_PATH", "VERSION", "TYPE", "OVERRIDE")
	}

	// List all images and their tags
	totalTags := 0
	totalImages := 0
	for _, groupedImg := range groupedImages {
		img := groupedImg.Image
		group := groupedImg.GroupName
		imgType := groupedImg.Type
		chartName := groupedImg.ChartName
		if group == "" {
			group = "-"
		}

		if len(img.Tags) == 0 {
			// Image with no tags
			totalImages++
			if showTargets {
				targetBase := generateTarget(cfg, img, imgType, chartName)
				table.Append(group, img.Source, "(no tags)", imgType, targetBase)
			} else {
				override := img.TargetOverride
				if override == "" {
					override = "-"
				}
				table.Append(group, img.Source, "(no tags)", imgType, override)
			}
		} else {
			// List each tag
			totalImages++
			for i, tag := range img.Tags {
				totalTags++
				if showTargets {
					targetBase := generateTarget(cfg, img, imgType, chartName)
					targetFull := fmt.Sprintf("%s:%s", targetBase, tag)
					if i == 0 {
						table.Append(group, img.Source, tag, imgType, targetFull)
					} else {
						table.Append("", "", tag, "", targetFull)
					}
				} else {
					override := img.TargetOverride
					if override == "" {
						override = "-"
					}
					if i == 0 {
						table.Append(group, img.Source, tag, imgType, override)
					} else {
						table.Append("", "", tag, "", "")
					}
				}
			}
		}
	}

	table.Render()

	// Summary
	fmt.Printf("\nTotal: %d images, %d tags\n", totalImages, totalTags)

	return nil
}

func loadConfigForList() (*config.Config, error) {
	// If no config file specified, try to find one
	if cfgFile == "" {
		// Try current directory first
		if _, err := os.Stat(".vendoci.yaml"); err == nil {
			cfgFile = ".vendoci.yaml"
		} else if _, err := os.Stat("vendoci.yaml"); err == nil {
			cfgFile = "vendoci.yaml"
		} else {
			// Try home directory
			home, err := os.UserHomeDir()
			if err == nil {
				homeConfig := home + "/.vendoci.yaml"
				if _, err := os.Stat(homeConfig); err == nil {
					cfgFile = homeConfig
				}
			}
		}

		if cfgFile == "" {
			return nil, fmt.Errorf("no config file found. Use --config to specify one")
		}
	}

	// Load the config file
	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	return cfg, nil
}

func generateTarget(cfg *config.Config, img config.ImageConfig, imgType string, chartName string) string {
	// For Helm charts, use vendor/charts/{chartName} format
	if imgType == "helm" {
		return fmt.Sprintf("%s/%s/charts/%s", cfg.TargetRegistry, cfg.VendorPrefix, chartName)
	}

	// For OCI images, use existing logic
	if img.TargetOverride != "" {
		return fmt.Sprintf("%s/%s/%s", cfg.TargetRegistry, cfg.VendorPrefix, img.TargetOverride)
	}
	return cfg.GenerateTargetImage(img.Source)
}
