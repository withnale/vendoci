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

	// Images to vendor
	Images []ImageConfig `yaml:"images" json:"images"`

	// Registry authentication
	Auth AuthConfig `yaml:"auth" json:"auth"`
}

// ImageConfig represents a single image source configuration
type ImageConfig struct {
	// Source repository (e.g., "docker.io/ubuntu")
	Source string `yaml:"source" json:"source"`

	// Tags to vendor (e.g., ["latest", "20.04", "22.04"])
	Tags []string `yaml:"tags" json:"tags"`

	// Optional: Override the target name transformation
	TargetOverride string `yaml:"target_override,omitempty" json:"target_override,omitempty"`
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

// GenerateTargetImage creates the target image name based on the source
func (c *Config) GenerateTargetImage(source string) string {
	// Transform source: docker.io/ubuntu -> docker.io.ubuntu
	// Remove any registry prefix and replace slashes with dots
	transformed := transformSourceName(source)

	return fmt.Sprintf("%s/%s/%s", c.TargetRegistry, c.VendorPrefix, transformed)
}

// transformSourceName converts a source image name to a vendor-friendly format
// Examples:
//   docker.io/ubuntu -> docker.io.ubuntu
//   gcr.io/google-containers/pause -> gcr.io.google-containers.pause
//   ubuntu -> docker.io.ubuntu (assumes docker.io as default)
func transformSourceName(source string) string {
	// If no registry specified, assume docker.io
	if !containsRegistry(source) {
		source = "docker.io/" + source
	}

	// Replace slashes with dots, except for the first one which separates registry from path
	result := ""
	parts := splitImageName(source)

	if len(parts.Registry) > 0 {
		result = parts.Registry
	}

	if len(parts.Path) > 0 {
		if result != "" {
			result += "."
		}
		// Replace remaining slashes with dots
		for i, segment := range parts.PathSegments {
			if i > 0 {
				result += "."
			}
			result += segment
		}
	}

	return result
}

// ImageParts represents the components of an image name
type ImageParts struct {
	Registry     string
	Path         string
	PathSegments []string
}

// splitImageName splits an image name into registry and path components
func splitImageName(image string) ImageParts {
	// Simple implementation - can be enhanced for more complex cases
	registryPath := image

	// Find the first slash
	slashIndex := findFirstSlash(registryPath)
	if slashIndex == -1 {
		return ImageParts{
			Registry:     "",
			Path:         image,
			PathSegments: []string{image},
		}
	}

	registry := registryPath[:slashIndex]
	path := registryPath[slashIndex+1:]

	// Split path into segments
	segments := splitBySlash(path)

	return ImageParts{
		Registry:     registry,
		Path:         path,
		PathSegments: segments,
	}
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

func splitBySlash(s string) []string {
	if s == "" {
		return []string{}
	}

	var result []string
	current := ""

	for _, r := range s {
		if r == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(r)
		}
	}

	if current != "" {
		result = append(result, current)
	}

	return result
}