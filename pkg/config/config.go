package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

// LoadConfig loads configuration from a YAML file or directory
// If a directory is provided, it walks through all .yaml and .yml files
// and deep merges them into a single configuration
func LoadConfig(path string) (*Config, error) {
	// Check if path is a directory or file
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("accessing config path: %w", err)
	}

	var configs []*Config

	if info.IsDir() {
		// Load all YAML files from directory
		configs, err = loadConfigsFromDirectory(path)
		if err != nil {
			return nil, err
		}

		if len(configs) == 0 {
			return nil, fmt.Errorf("no YAML files found in directory: %s", path)
		}
	} else {
		// Load single file
		cfg, err := loadSingleConfig(path)
		if err != nil {
			return nil, err
		}
		configs = []*Config{cfg}
	}

	// Merge all configs
	merged := mergeConfigs(configs)

	// Apply post-processing
	return postProcessConfig(merged)
}

// loadConfigsFromDirectory loads all .yaml and .yml files from a directory
func loadConfigsFromDirectory(dir string) ([]*Config, error) {
	var configs []*Config
	var files []string

	// Walk directory and collect YAML files
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process .yaml and .yml files
		ext := filepath.Ext(path)
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	// Sort files for consistent ordering
	sort.Strings(files)

	// Load each file
	for _, file := range files {
		cfg, err := loadSingleConfig(file)
		if err != nil {
			return nil, fmt.Errorf("loading %s: %w", file, err)
		}
		configs = append(configs, cfg)
	}

	return configs, nil
}

// loadSingleConfig loads a single YAML configuration file
func loadSingleConfig(filename string) (*Config, error) {
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

	return &config, nil
}

// mergeConfigs deep merges multiple configs into one
// Later configs override earlier ones
func mergeConfigs(configs []*Config) *Config {
	if len(configs) == 0 {
		return &Config{}
	}

	if len(configs) == 1 {
		return configs[0]
	}

	// Start with the first config
	result := configs[0]

	// Merge each subsequent config
	for i := 1; i < len(configs); i++ {
		result = mergeTwoConfigs(result, configs[i])
	}

	return result
}

// mergeTwoConfigs merges two configs, with 'overlay' taking precedence
func mergeTwoConfigs(base, overlay *Config) *Config {
	merged := &Config{}

	// Merge simple fields - overlay wins if set
	if overlay.TargetRegistry != "" {
		merged.TargetRegistry = overlay.TargetRegistry
	} else {
		merged.TargetRegistry = base.TargetRegistry
	}

	if overlay.VendorPrefix != "" {
		merged.VendorPrefix = overlay.VendorPrefix
	} else {
		merged.VendorPrefix = base.VendorPrefix
	}

	// Merge auth - overlay wins for any set field
	merged.Auth = mergeAuth(base.Auth, overlay.Auth)

	// Merge groups - deep merge by group name
	merged.Groups = mergeGroups(base.Groups, overlay.Groups)

	// Merge images arrays
	merged.ImagesArray = append(merged.ImagesArray, base.ImagesArray...)
	merged.ImagesArray = append(merged.ImagesArray, overlay.ImagesArray...)

	// Merge images maps
	merged.ImagesMap = make(map[string]ImageConfig)
	for k, v := range base.ImagesMap {
		merged.ImagesMap[k] = v
	}
	for k, v := range overlay.ImagesMap {
		merged.ImagesMap[k] = v
	}

	// Preserve the Images interface field from overlay if set
	if overlay.Images != nil {
		merged.Images = overlay.Images
	} else {
		merged.Images = base.Images
	}

	return merged
}

// mergeAuth merges two auth configs
func mergeAuth(base, overlay AuthConfig) AuthConfig {
	merged := base

	if overlay.Username != "" {
		merged.Username = overlay.Username
	}
	if overlay.Password != "" {
		merged.Password = overlay.Password
	}
	if overlay.PasswordEnv != "" {
		merged.PasswordEnv = overlay.PasswordEnv
	}
	if overlay.Token != "" {
		merged.Token = overlay.Token
	}
	if overlay.TokenEnv != "" {
		merged.TokenEnv = overlay.TokenEnv
	}

	return merged
}

// mergeGroups merges two group maps
func mergeGroups(base, overlay map[string]GroupConfig) map[string]GroupConfig {
	merged := make(map[string]GroupConfig)

	// Copy all base groups
	for name, group := range base {
		merged[name] = group
	}

	// Merge overlay groups
	for name, overlayGroup := range overlay {
		if baseGroup, exists := merged[name]; exists {
			// Merge existing group
			mergedGroup := GroupConfig{
				Images: append(append([]ImageConfig{}, baseGroup.Images...), overlayGroup.Images...),
				Charts: append(append([]ChartEntry{}, baseGroup.Charts...), overlayGroup.Charts...),
			}
			merged[name] = mergedGroup
		} else {
			// Add new group
			merged[name] = overlayGroup
		}
	}

	return merged
}

// postProcessConfig applies environment variable substitutions and defaults
func postProcessConfig(config *Config) (*Config, error) {
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

	return config, nil
}

// GetAllImages returns all images and charts with their group names
func (c *Config) GetAllImages() []GroupedImage {
	var result []GroupedImage

	// Add images and charts from groups
	for groupName, group := range c.Groups {
		// Add images
		for _, img := range group.Images {
			normalizedImg := normalizeImageConfig(img)

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
		normalizedImg := normalizeImageConfig(img)

		result = append(result, GroupedImage{
			GroupName: "", // No group
			Image:     normalizedImg,
			Type:      "oci",
		})
	}

	// Add ungrouped images from map format
	for imageName, img := range c.ImagesMap {
		normalizedImg := normalizeImageConfig(img)

		result = append(result, GroupedImage{
			GroupName: imageName, // Use the map key as group name
			Image:     normalizedImg,
			Type:      "oci",
		})
	}

	return result
}

// GetImageBasename extracts the basename from an image source path
// Examples:
//
//	"docker.io/ubuntu" -> "ubuntu"
//	"gcr.io/my-project/my-app" -> "my-app"
//	"localhost:5000/namespace/app" -> "app"
func GetImageBasename(source string) string {
	// Find the last slash
	lastSlash := -1
	for i := len(source) - 1; i >= 0; i-- {
		if source[i] == '/' {
			lastSlash = i
			break
		}
	}

	// No slash found - return the whole string
	if lastSlash == -1 {
		return source
	}

	return source[lastSlash+1:]
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

// normalizeImageConfig processes an ImageConfig to extract tag from source if needed
func normalizeImageConfig(img ImageConfig) ImageConfig {
	normalizedImg := img

	// Use Repo if Source is empty
	if normalizedImg.Source == "" && normalizedImg.Repo != "" {
		normalizedImg.Source = normalizedImg.Repo
	}

	// If tags array is empty, try to extract tag from source
	if len(normalizedImg.Tags) == 0 && normalizedImg.Source != "" {
		source, tag := splitImageAndTag(normalizedImg.Source)
		if tag != "" {
			normalizedImg.Source = source
			normalizedImg.Tags = []string{tag}
		}
	}

	return normalizedImg
}

// splitImageAndTag splits an image reference into image and tag
// Examples:
//
//	"docker.io/ubuntu:latest" -> ("docker.io/ubuntu", "latest")
//	"docker.io/ubuntu" -> ("docker.io/ubuntu", "")
//	"ubuntu:20.04" -> ("ubuntu", "20.04")
func splitImageAndTag(imageRef string) (string, string) {
	// Find the last occurrence of ':'
	// We use the last one because registry names can contain ports (e.g., localhost:5000/image:tag)
	lastColon := -1
	for i := len(imageRef) - 1; i >= 0; i-- {
		if imageRef[i] == ':' {
			lastColon = i
			break
		}
	}

	// No colon found - no tag specified
	if lastColon == -1 {
		return imageRef, ""
	}

	// Check if this is a port (registry:port/image format)
	// If there's a slash after the colon, it's a port, not a tag
	for i := lastColon + 1; i < len(imageRef); i++ {
		if imageRef[i] == '/' {
			// This colon is part of a registry:port, not a tag separator
			return imageRef, ""
		}
	}

	return imageRef[:lastColon], imageRef[lastColon+1:]
}

func containsRegistry(image string) bool {
	// Simple heuristic: if it contains a dot or colon before the first slash, assume it's a registry
	// Examples:
	//   docker.io/ubuntu -> true (has dot)
	//   localhost:5000/app -> true (has colon for port)
	//   ubuntu -> false (no dot or colon)
	slashIndex := findFirstSlash(image)
	if slashIndex == -1 {
		return false
	}

	registryPart := image[:slashIndex]
	return containsDot(registryPart) || containsColon(registryPart)
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

func containsColon(s string) bool {
	for _, r := range s {
		if r == ':' {
			return true
		}
	}
	return false
}
