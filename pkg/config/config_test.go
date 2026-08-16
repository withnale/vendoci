package config

import "testing"

func TestSplitImageAndTag(t *testing.T) {
	tests := []struct {
		name          string
		imageRef      string
		expectedImage string
		expectedTag   string
	}{
		{
			name:          "simple image with tag",
			imageRef:      "ubuntu:latest",
			expectedImage: "ubuntu",
			expectedTag:   "latest",
		},
		{
			name:          "image with registry and tag",
			imageRef:      "docker.io/ubuntu:20.04",
			expectedImage: "docker.io/ubuntu",
			expectedTag:   "20.04",
		},
		{
			name:          "image with registry, namespace, and tag",
			imageRef:      "gcr.io/my-project/my-app:v1.2.3",
			expectedImage: "gcr.io/my-project/my-app",
			expectedTag:   "v1.2.3",
		},
		{
			name:          "image without tag",
			imageRef:      "docker.io/ubuntu",
			expectedImage: "docker.io/ubuntu",
			expectedTag:   "",
		},
		{
			name:          "registry with port and tag",
			imageRef:      "localhost:5000/myapp:latest",
			expectedImage: "localhost:5000/myapp",
			expectedTag:   "latest",
		},
		{
			name:          "registry with port without tag",
			imageRef:      "localhost:5000/myapp",
			expectedImage: "localhost:5000/myapp",
			expectedTag:   "",
		},
		{
			name:          "registry with port and namespace",
			imageRef:      "registry.example.com:443/namespace/app:v2.0",
			expectedImage: "registry.example.com:443/namespace/app",
			expectedTag:   "v2.0",
		},
		{
			name:          "digest reference (not a tag)",
			imageRef:      "ubuntu@sha256:abc123",
			expectedImage: "ubuntu@sha256",
			expectedTag:   "abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image, tag := splitImageAndTag(tt.imageRef)
			if image != tt.expectedImage {
				t.Errorf("splitImageAndTag(%q) image = %q, want %q", tt.imageRef, image, tt.expectedImage)
			}
			if tag != tt.expectedTag {
				t.Errorf("splitImageAndTag(%q) tag = %q, want %q", tt.imageRef, tag, tt.expectedTag)
			}
		})
	}
}

func TestNormalizeImageConfig(t *testing.T) {
	tests := []struct {
		name           string
		input          ImageConfig
		expectedSource string
		expectedTags   []string
	}{
		{
			name: "tag in source, no tags array",
			input: ImageConfig{
				Source: "docker.io/ubuntu:20.04",
				Tags:   []string{},
			},
			expectedSource: "docker.io/ubuntu",
			expectedTags:   []string{"20.04"},
		},
		{
			name: "tag in source, nil tags",
			input: ImageConfig{
				Source: "nginx:1.25.0",
				Tags:   nil,
			},
			expectedSource: "nginx",
			expectedTags:   []string{"1.25.0"},
		},
		{
			name: "tags array provided (should not extract from source)",
			input: ImageConfig{
				Source: "ubuntu",
				Tags:   []string{"20.04", "22.04"},
			},
			expectedSource: "ubuntu",
			expectedTags:   []string{"20.04", "22.04"},
		},
		{
			name: "use repo instead of source",
			input: ImageConfig{
				Repo: "docker.io/redis:7.2",
				Tags: []string{},
			},
			expectedSource: "docker.io/redis",
			expectedTags:   []string{"7.2"},
		},
		{
			name: "registry with port",
			input: ImageConfig{
				Source: "localhost:5000/app:latest",
				Tags:   []string{},
			},
			expectedSource: "localhost:5000/app",
			expectedTags:   []string{"latest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeImageConfig(tt.input)
			if result.Source != tt.expectedSource {
				t.Errorf("normalizeImageConfig() source = %q, want %q", result.Source, tt.expectedSource)
			}
			if len(result.Tags) != len(tt.expectedTags) {
				t.Errorf("normalizeImageConfig() tags length = %d, want %d", len(result.Tags), len(tt.expectedTags))
			}
			for i, tag := range result.Tags {
				if tag != tt.expectedTags[i] {
					t.Errorf("normalizeImageConfig() tags[%d] = %q, want %q", i, tag, tt.expectedTags[i])
				}
			}
		})
	}
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}

// TestShouldSyncGroup tests the sync flag behavior
func TestShouldSyncGroup(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		groupName string
		expected  bool
	}{
		{
			name: "sync not set (default true)",
			config: &Config{
				Groups: map[string]GroupConfig{
					"test-group": {
						Images: []ImageConfig{
							{Source: "docker.io/alpine", Tags: []string{"latest"}},
						},
					},
				},
			},
			groupName: "test-group",
			expected:  true,
		},
		{
			name: "sync explicitly set to true",
			config: &Config{
				Groups: map[string]GroupConfig{
					"test-group": {
						Images: []ImageConfig{
							{Source: "docker.io/alpine", Tags: []string{"latest"}},
						},
						Sync: boolPtr(true),
					},
				},
			},
			groupName: "test-group",
			expected:  true,
		},
		{
			name: "sync set to false",
			config: &Config{
				Groups: map[string]GroupConfig{
					"test-group": {
						Images: []ImageConfig{
							{Source: "docker.io/alpine", Tags: []string{"latest"}},
						},
						Sync: boolPtr(false),
					},
				},
			},
			groupName: "test-group",
			expected:  false,
		},
		{
			name: "non-existent group",
			config: &Config{
				Groups: map[string]GroupConfig{},
			},
			groupName: "non-existent",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.ShouldSyncGroup(tt.groupName)
			if result != tt.expected {
				t.Errorf("ShouldSyncGroup(%q) = %v, want %v", tt.groupName, result, tt.expected)
			}
		})
	}
}

// TestGetAllImagesFiltered tests group filtering and sync flag behavior
func TestGetAllImagesFiltered(t *testing.T) {
	config := &Config{
		Groups: map[string]GroupConfig{
			"auto-sync": {
				Images: []ImageConfig{
					{Source: "docker.io/nginx", Tags: []string{"latest"}},
				},
				Sync: boolPtr(true),
			},
			"no-sync": {
				Images: []ImageConfig{
					{Source: "docker.io/redis", Tags: []string{"latest"}},
				},
				Sync: boolPtr(false),
			},
			"default-sync": {
				Images: []ImageConfig{
					{Source: "docker.io/alpine", Tags: []string{"latest"}},
				},
			},
		},
	}

	tests := []struct {
		name            string
		requestedGroups []string
		expectedCount   int
		expectedSources []string
	}{
		{
			name:            "no groups specified - auto syncs enabled groups only",
			requestedGroups: nil,
			expectedCount:   2,
			expectedSources: []string{"docker.io/nginx", "docker.io/alpine"},
		},
		{
			name:            "request specific group with sync: true",
			requestedGroups: []string{"auto-sync"},
			expectedCount:   1,
			expectedSources: []string{"docker.io/nginx"},
		},
		{
			name:            "request specific group with sync: false",
			requestedGroups: []string{"no-sync"},
			expectedCount:   1,
			expectedSources: []string{"docker.io/redis"},
		},
		{
			name:            "request multiple groups including disabled one",
			requestedGroups: []string{"auto-sync", "no-sync"},
			expectedCount:   2,
			expectedSources: []string{"docker.io/nginx", "docker.io/redis"},
		},
		{
			name:            "request all groups explicitly",
			requestedGroups: []string{"auto-sync", "no-sync", "default-sync"},
			expectedCount:   3,
			expectedSources: []string{"docker.io/nginx", "docker.io/redis", "docker.io/alpine"},
		},
		{
			name:            "request non-existent group",
			requestedGroups: []string{"non-existent"},
			expectedCount:   0,
			expectedSources: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := config.GetAllImagesFiltered(tt.requestedGroups)
			
			if len(result) != tt.expectedCount {
				t.Errorf("GetAllImagesFiltered() returned %d images, want %d", len(result), tt.expectedCount)
			}

			sources := make(map[string]bool)
			for _, img := range result {
				sources[img.Image.Source] = true
			}

			for _, expectedSource := range tt.expectedSources {
				if !sources[expectedSource] {
					t.Errorf("GetAllImagesFiltered() missing expected source %q", expectedSource)
				}
			}
		})
	}
}

// TestGetAllImages tests backward compatibility
func TestGetAllImages(t *testing.T) {
	config := &Config{
		Groups: map[string]GroupConfig{
			"enabled": {
				Images: []ImageConfig{
					{Source: "docker.io/nginx", Tags: []string{"latest"}},
				},
				Sync: boolPtr(true),
			},
			"disabled": {
				Images: []ImageConfig{
					{Source: "docker.io/redis", Tags: []string{"latest"}},
				},
				Sync: boolPtr(false),
			},
			"default": {
				Images: []ImageConfig{
					{Source: "docker.io/alpine", Tags: []string{"latest"}},
				},
			},
		},
	}

	result := config.GetAllImages()

	if len(result) != 2 {
		t.Errorf("GetAllImages() returned %d images, want 2", len(result))
	}

	sources := make(map[string]bool)
	for _, img := range result {
		sources[img.Image.Source] = true
	}

	if !sources["docker.io/nginx"] {
		t.Error("GetAllImages() missing nginx (explicitly enabled)")
	}
	if !sources["docker.io/alpine"] {
		t.Error("GetAllImages() missing alpine (default enabled)")
	}
	if sources["docker.io/redis"] {
		t.Error("GetAllImages() should not include redis (disabled)")
	}
}
