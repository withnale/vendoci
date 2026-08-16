package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rhodesp/vendoci/pkg/config"
)

var (
	showTargets  bool
	filterType   string
	filterGroups []string
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
  vendoci list --show-targets

  # List only OCI images
  vendoci list --type oci

  # List only Helm charts
  vendoci list --type helm

  # List only specific groups
  vendoci list --groups alpine
  vendoci list -G alpine -G busybox

  # Combine filters
  vendoci list --type oci --groups alpine`,
	RunE: runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Flags
	listCmd.Flags().BoolVar(&showTargets, "show-targets", false, "Show target image names in addition to source images")
	listCmd.Flags().StringVar(&filterType, "type", "", "Filter by type: oci or helm (default: show all)")
	listCmd.Flags().StringSliceVarP(&filterGroups, "groups", "G", []string{}, "Filter by group names (can specify multiple times)")
}

func runList(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := loadConfigForList()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	// Get all images with groups
	groupedImages := cfg.GetAllImages()

	// Filter by groups if specified
	if len(filterGroups) > 0 {
		groupedImages = filterImagesByGroups(groupedImages, filterGroups)
	}

	// Filter by type if specified
	if filterType != "" {
		groupedImages = filterImagesByType(groupedImages, filterType)
	}

	// Check if there are any images configured
	if len(groupedImages) == 0 {
		if len(filterGroups) > 0 && filterType != "" {
			fmt.Printf("No images of type '%s' in groups %v configured in config file\n", filterType, filterGroups)
		} else if len(filterGroups) > 0 {
			fmt.Printf("No images in groups %v configured in config file\n", filterGroups)
		} else if filterType != "" {
			fmt.Printf("No images of type '%s' configured in config file\n", filterType)
		} else {
			fmt.Println("No images configured in config file")
		}
		return nil
	}

	// Display configuration info
	fmt.Printf("Configuration: %s\n", cfgFile)
	fmt.Printf("Target Registry: %s\n", cfg.Target.Registry)
	fmt.Printf("Vendor Prefix: %s\n\n", cfg.Target.Prefix)

	// Create table
	var table *TablePrinter
	if showTargets {
		table = NewTable("GROUP", "NAME", "SOURCE_PATH", "VERSION", "TYPE", "TARGET_PATH")
	} else {
		table = NewTable("GROUP", "NAME", "SOURCE_PATH", "VERSION", "TYPE", "OVERRIDE")
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

		// Determine the name to display
		var name string
		if imgType == "helm" {
			name = chartName
		} else {
			name = config.GetImageBasename(img.Source)
		}

		if len(img.Tags) == 0 {
			// Image with no tags
			totalImages++
			if showTargets {
				targetBase := generateTarget(cfg, img, imgType, chartName)
				table.AddRow(group, name, img.Source, "(no tags)", imgType, targetBase)
			} else {
				override := img.TargetOverride
				if override == "" {
					override = "-"
				}
				table.AddRow(group, name, img.Source, "(no tags)", imgType, override)
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
						table.AddRow(group, name, img.Source, tag, imgType, targetFull)
					} else {
						table.AddRow("", "", "", tag, "", targetFull)
					}
				} else {
					override := img.TargetOverride
					if override == "" {
						override = "-"
					}
					if i == 0 {
						table.AddRow(group, name, img.Source, tag, imgType, override)
					} else {
						table.AddRow("", "", "", tag, "", "")
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
		// First, check for VENDOCI_CONFIG environment variable
		if envConfig := os.Getenv("VENDOCI_CONFIG"); envConfig != "" {
			cfgFile = envConfig
		} else {
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
		}

		if cfgFile == "" {
			return nil, fmt.Errorf("no config file found. Use --config flag or set VENDOCI_CONFIG environment variable")
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
		return fmt.Sprintf("%s/%s/charts/%s", cfg.Target.Registry, cfg.Target.Prefix, chartName)
	}

	// For OCI images, use existing logic
	if img.TargetOverride != "" {
		return fmt.Sprintf("%s/%s/%s", cfg.Target.Registry, cfg.Target.Prefix, img.TargetOverride)
	}
	return cfg.GenerateTargetImage(img.Source)
}

// filterImagesByType filters grouped images by type (oci or helm)
func filterImagesByType(images []config.GroupedImage, imageType string) []config.GroupedImage {
	var filtered []config.GroupedImage
	for _, img := range images {
		if img.Type == imageType {
			filtered = append(filtered, img)
		}
	}
	return filtered
}

// filterImagesByGroups filters grouped images by group names
func filterImagesByGroups(images []config.GroupedImage, groups []string) []config.GroupedImage {
	// Create a map for O(1) lookups
	groupMap := make(map[string]bool)
	for _, group := range groups {
		groupMap[group] = true
	}

	var filtered []config.GroupedImage
	for _, img := range images {
		if groupMap[img.GroupName] {
			filtered = append(filtered, img)
		}
	}
	return filtered
}
