package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the main configuration structure
type Config struct {
	// Target registry where images will be vendored
	TargetRegistry string `yaml:"target_registry" json:"target_registry"`

	// Prefix for vendored images (e.g., "vendor")
	VendorPrefix string `yaml:"vendor_prefix" json:"vendor_prefix"`

	// Images to vendor (can be array or map format)
	// The actual type is determined during unmarshaling
	Images interface{} `yaml:"images,omitempty" json:"images,omitempty"`

	// Parsed images from array format
	ImagesArray []ImageConfig `yaml:"-" json:"-"`

	// Parsed images from map format
	ImagesMap map[string]ImageConfig `yaml:"-" json:"-"`

	// Groups of images and charts to vendor
	Groups map[string]GroupConfig `yaml:"groups,omitempty" json:"groups,omitempty"`

	// Registry authentication
	Auth AuthConfig `yaml:"auth" json:"auth"`
}

// GroupConfig represents a group of images and charts
type GroupConfig struct {
	Images []ImageConfig `yaml:"images,omitempty" json:"images,omitempty"`
	Charts []ChartEntry  `yaml:"charts,omitempty" json:"charts,omitempty"`
}

// ChartEntry represents a chart within a group
type ChartEntry struct {
	Chart   string `yaml:"chart" json:"chart"`
	Repo    string `yaml:"repo" json:"repo"`
	Version string `yaml:"version" json:"version"`
}

// ImageConfig represents a single image source configuration
type ImageConfig struct {
	// Source repository (legacy format)
	Source string `yaml:"source,omitempty" json:"source,omitempty"`

	// Repository (new format)
	Repo string `yaml:"repo,omitempty" json:"repo,omitempty"`

	// Tags to vendor
	Tags []string `yaml:"tags" json:"tags,omitempty"`

	// Optional: Override the target name transformation
	TargetOverride string `yaml:"target_override,omitempty" json:"target_override,omitempty"`
}

// ChartConfig represents a single chart source configuration
type ChartConfig struct {
	// Repository
	Repo string `yaml:"repo" json:"repo"`

	// Version to vendor
	Version string `yaml:"version" json:"version"`
}

// GroupedImage represents an image with its group name and type
type GroupedImage struct {
	GroupName string
	Image     ImageConfig
	Type      string // "oci" for container images, "helm" for charts
	ChartName string // For helm charts, the chart name
}

// AuthConfig represents registry authentication configuration
type AuthConfig struct {
	// Username for target registry
	Username string `yaml:"username,omitempty" json:"username,omitempty"`

	// Password for target registry (prefer using environment variable)
	Password string `yaml:"password,omitempty" json:"password,omitempty"`

	// Environment variable name for password
	PasswordEnv string `yaml:"password_env,omitempty" json:"password_env,omitempty"`

	// Token for registry authentication
	Token string `yaml:"token,omitempty" json:"token,omitempty"`

	// Environment variable name for token
	TokenEnv string `yaml:"token_env,omitempty" json:"token_env,omitempty"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Parse the images field based on its actual type
	if config.Images != nil {
		switch images := config.Images.(type) {
		case []interface{}:
			// Array format - convert to ImageConfig array
			for _, item := range images {
				imgData, err := yaml.Marshal(item)
				if err != nil {
					return nil, fmt.Errorf("marshaling image config: %w", err)
				}
				var imgCfg ImageConfig
				if err := yaml.Unmarshal(imgData, &imgCfg); err != nil {
					return nil, fmt.Errorf("unmarshaling image config: %w", err)
				}
				config.ImagesArray = append(config.ImagesArray, imgCfg)
			}
		case map[string]interface{}:
			// Map format - convert to ImageConfig map
			config.ImagesMap = make(map[string]ImageConfig)
			for name, value := range images {
				imgData, err := yaml.Marshal(value)
				if err != nil {
					return nil, fmt.Errorf("marshaling image config for %s: %w", name, err)
				}
				var imgCfg ImageConfig
				if err := yaml.Unmarshal(imgData, &imgCfg); err != nil {
					return nil, fmt.Errorf("unmarshaling image config for %s: %w", name, err)
				}
				config.ImagesMap[name] = imgCfg
			}
		}
	}

	// Apply environment variable substitutions
	if config.Auth.PasswordEnv != "" && config.Auth.Password == "" {
		config.Auth.Password = os.Getenv(config.Auth.PasswordEnv)
	}
	if config.Auth.TokenEnv != "" && config.Auth.Token == "" {
		config.Auth.Token = os.Getenv(config.Auth.TokenEnv)
	}

	// Set defaults
	if config.VendorPrefix == "" {
		config.VendorPrefix = "vendor"
	}

	return &config, nil
}

// GetAllImages returns all images and charts with their group names
func (c *Config) GetAllImages() []GroupedImage {
	var result []GroupedImage

	// Add images and charts from groups
	for groupName, group := range c.Groups {
		// Add images
		for _, img := range group.Images {
			// Normalize the image config - use Repo if Source is empty
			normalizedImg := img
			if normalizedImg.Source == "" && normalizedImg.Repo != "" {
				normalizedImg.Source = normalizedImg.Repo
			}

			result = append(result, GroupedImage{
				GroupName: groupName,
				Image:     normalizedImg,
				Type:      "oci",
			})
		}

		// Add charts as image-like entries
		for _, chart := range group.Charts {
			// Convert chart to ImageConfig format
			// chart.Repo -> source_image, chart.Version -> tag
			chartAsImage := ImageConfig{
				Source: chart.Repo,
				Tags:   []string{chart.Version},
			}

			result = append(result, GroupedImage{
				GroupName: groupName,
				Image:     chartAsImage,
				Type:      "helm",
				ChartName: chart.Chart,
			})
		}
	}

	// Add ungrouped images from array format (legacy support)
	for _, img := range c.ImagesArray {
		// Normalize the image config - use Repo if Source is empty
		normalizedImg := img
		if normalizedImg.Source == "" && normalizedImg.Repo != "" {
			normalizedImg.Source = normalizedImg.Repo
		}

		result = append(result, GroupedImage{
			GroupName: "", // No group
			Image:     normalizedImg,
			Type:      "oci",
		})
	}

	// Add ungrouped images from map format
	for imageName, img := range c.ImagesMap {
		// Normalize the image config - use Repo if Source is empty
		normalizedImg := img
		if normalizedImg.Source == "" && normalizedImg.Repo != "" {
			normalizedImg.Source = normalizedImg.Repo
		}

		result = append(result, GroupedImage{
			GroupName: imageName, // Use the map key as group name
			Image:     normalizedImg,
			Type:      "oci",
		})
	}

	return result
}

// GenerateTargetImage creates the target image name based on the source
func (c *Config) GenerateTargetImage(source string) string {
	// Transform source: docker.io/ubuntu -> docker.io/ubuntu
	// Keep the original path structure
	transformed := transformSourceName(source)

	return fmt.Sprintf("%s/%s/%s", c.TargetRegistry, c.VendorPrefix, transformed)
}

// transformSourceName converts a source image name to a vendor-friendly format
// Examples:
//
//	docker.io/ubuntu -> docker.io/ubuntu
//	gcr.io/google-containers/pause -> gcr.io/google-containers/pause
//	ubuntu -> docker.io/ubuntu (assumes docker.io as default)
func transformSourceName(source string) string {
	// If no registry specified, assume docker.io
	if !containsRegistry(source) {
		source = "docker.io/" + source
	}

	// Keep the original path structure with slashes
	return source
}

// Helper functions

func containsRegistry(image string) bool {
	// Simple heuristic: if it contains a dot before the first slash, assume it's a registry
	slashIndex := findFirstSlash(image)
	if slashIndex == -1 {
		return false
	}

	registryPart := image[:slashIndex]
	return containsDot(registryPart)
}

func findFirstSlash(s string) int {
	for i, r := range s {
		if r == '/' {
			return i
		}
	}
	return -1
}

func containsDot(s string) bool {
	for _, r := range s {
		if r == '.' {
			return true
		}
	}
	return false
}
