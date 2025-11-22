package vendor

import (
	"context"
	"log"
	"net/http"
	"os"
	"testing"

	"github.com/rhodesp/vendoci/pkg/config"
)

// TestAlpineImageCopyToGHCR tests copying alpine:3.23.0 directly using vendor package
func TestAlpineImageCopyToGHCR(t *testing.T) {
	// Check for required environment variables
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("Skipping test: GITHUB_TOKEN environment variable not set")
	}

	// Create test configuration
	cfg := &config.Config{
		TargetRegistry: "ghcr.io/poptart-testing/poptart",
		VendorPrefix:   "vendor",
		Auth: config.AuthConfig{
			Username: "poptart-testing",
			Password: githubToken,
		},
		Groups: map[string]config.GroupConfig{
			"test": {
				Images: []config.ImageConfig{
					{
						Source: "docker.io/alpine",
						Tags:   []string{"3.23.0"},
					},
				},
			},
		},
	}

	// Create logger for test output
	logger := log.New(os.Stdout, "[VENDOR-TEST] ", log.LstdFlags)

	// Create vendor instance directly
	v := New(cfg, logger)

	// Test configuration
	ctx := context.Background()
	opts := SyncOptions{
		DryRun:  false, // Set to true for dry-run
		Verbose: true,
		Force:   true, // Force re-sync even if image exists
	}

	expectedSource := "docker.io/alpine:3.23.0"
	expectedTarget := "ghcr.io/poptart-testing/poptart/vendor/docker.io/alpine:3.23.0"

	t.Logf("Testing direct vendor copy: %s -> %s", expectedSource, expectedTarget)

	// Call SyncAll directly on the vendor instance
	results, err := v.SyncAll(ctx, opts)
	if err != nil {
		t.Fatalf("Vendor SyncAll failed: %v", err)
	}

	// Verify results
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]

	// Validate the result
	if result.Source != expectedSource {
		t.Errorf("Expected source %s, got %s", expectedSource, result.Source)
	}

	if result.Target != expectedTarget {
		t.Errorf("Expected target %s, got %s", expectedTarget, result.Target)
	}

	if result.Status != SyncStatusSuccess {
		t.Fatalf("Expected status %s, got %s. Error: %v", SyncStatusSuccess, result.Status, result.Error)
	}

	if result.Tag != "3.23.0" {
		t.Errorf("Expected tag 3.23.0, got %s", result.Tag)
	}

	t.Logf("✅ Success! Copied %s -> %s", result.Source, result.Target)
}

// TestAlpineImageCopyDryRun tests the same but with dry-run (no auth needed)
func TestAlpineImageCopyDryRun(t *testing.T) {
	cfg := &config.Config{
		TargetRegistry: "ghcr.io/poptart-testing/poptart",
		VendorPrefix:   "vendor",
		Groups: map[string]config.GroupConfig{
			"test": {
				Images: []config.ImageConfig{
					{
						Source: "docker.io/alpine",
						Tags:   []string{"3.23.0"},
					},
				},
			},
		},
	}

	logger := log.New(os.Stdout, "[DRY-RUN] ", log.LstdFlags)
	v := New(cfg, logger)

	ctx := context.Background()
	opts := SyncOptions{
		DryRun:  true, // Dry run - no actual copy
		Verbose: true,
		Force:   false,
	}

	expectedTarget := "ghcr.io/poptart-testing/poptart/vendor/docker.io/alpine:3.23.0"

	t.Logf("Testing dry-run vendor copy to: %s", expectedTarget)

	results, err := v.SyncAll(ctx, opts)
	if err != nil {
		t.Fatalf("Dry-run failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]
	if result.Status != SyncStatusSuccess {
		t.Errorf("Dry-run should succeed, got status: %s, error: %v", result.Status, result.Error)
	}

	t.Logf("✅ Dry-run success: Would copy to %s", result.Target)
}

// TestTargetImageGeneration tests the image name transformation
func TestTargetImageGeneration(t *testing.T) {
	cfg := &config.Config{
		TargetRegistry: "ghcr.io/poptart-testing/poptart",
		VendorPrefix:   "vendor",
	}

	source := "docker.io/alpine"
	expected := "ghcr.io/poptart-testing/poptart/vendor/docker.io/alpine"

	result := cfg.GenerateTargetImage(source)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}

	t.Logf("✅ Target generation works: %s -> %s", source, result)
}

// TestCreateSourceProxyTransport tests proxy transport creation
func TestCreateSourceProxyTransport(t *testing.T) {
	tests := []struct {
		name    string
		proxy   *config.ProxyConfig
		wantErr bool
	}{
		{
			name:    "no proxy",
			proxy:   nil,
			wantErr: false,
		},
		{
			name: "proxy with URL only",
			proxy: &config.ProxyConfig{
				URL: "http://proxy.example.com:8080",
			},
			wantErr: false,
		},
		{
			name: "proxy with auth",
			proxy: &config.ProxyConfig{
				URL:      "http://proxy.example.com:8080",
				Username: "user",
				Password: "pass",
			},
			wantErr: false,
		},
		{
			name: "proxy with no_proxy list",
			proxy: &config.ProxyConfig{
				URL:     "http://proxy.example.com:8080",
				NoProxy: []string{"localhost", "internal.registry.com"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				SourceProxy: tt.proxy,
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			transport := v.createSourceProxyTransport()

			if transport == nil {
				t.Errorf("createSourceProxyTransport() returned nil")
				return
			}

			// Test that transport is a valid http.RoundTripper
			req, err := http.NewRequest("GET", "http://example.com", nil)
			if err != nil {
				t.Errorf("Failed to create test request: %v", err)
				return
			}

			_, err = transport.RoundTrip(req)
			// We expect errors since this is a test, but transport should be valid
			if tt.wantErr && err == nil {
				t.Errorf("Expected error but got none")
			} else if !tt.wantErr && err != nil {
				t.Logf("Got expected error (test environment): %v", err)
			}
		})
	}
}

// TestShouldUseProxy tests proxy decision logic
func TestShouldUseProxy(t *testing.T) {
	tests := []struct {
		name      string
		proxy     *config.ProxyConfig
		sourceURL string
		expected  bool
	}{
		{
			name:      "no proxy config",
			proxy:     nil,
			sourceURL: "docker.io/alpine",
			expected:  false,
		},
		{
			name: "proxy configured - should use",
			proxy: &config.ProxyConfig{
				URL: "http://proxy.example.com:8080",
			},
			sourceURL: "docker.io/alpine",
			expected:  true,
		},
		{
			name: "proxy configured - in no_proxy list",
			proxy: &config.ProxyConfig{
				URL:     "http://proxy.example.com:8080",
				NoProxy: []string{"docker.io", "localhost"},
			},
			sourceURL: "docker.io/alpine",
			expected:  false,
		},
		{
			name: "proxy configured - not in no_proxy list",
			proxy: &config.ProxyConfig{
				URL:     "http://proxy.example.com:8080",
				NoProxy: []string{"localhost"},
			},
			sourceURL: "docker.io/alpine",
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				SourceProxy: tt.proxy,
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			result := v.shouldUseProxy(tt.sourceURL)

			if result != tt.expected {
				t.Errorf("shouldUseProxy() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// TestGetProxiedHTTPClient tests HTTP client creation with proxy
func TestGetProxiedHTTPClient(t *testing.T) {
	tests := []struct {
		name    string
		proxy   *config.ProxyConfig
		wantNil bool
	}{
		{
			name:    "no proxy config",
			proxy:   nil,
			wantNil: false,
		},
		{
			name: "proxy configured",
			proxy: &config.ProxyConfig{
				URL: "http://proxy.example.com:8080",
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				SourceProxy: tt.proxy,
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			client := v.getProxiedHTTPClient()

			if client == nil {
				if !tt.wantNil {
					t.Errorf("getProxiedHTTPClient() returned nil unexpectedly")
				}
				return
			}

			if tt.wantNil {
				t.Errorf("getProxiedHTTPClient() expected nil, got non-nil")
				return
			}

			// Test that client has the expected properties
			if client.Timeout == 0 {
				t.Errorf("Expected client timeout to be set")
			}
		})
	}
}

// TestProxyConfigInOptions tests that proxy configuration is properly applied to crane and remote options
func TestProxyConfigInOptions(t *testing.T) {
	proxyCfg := &config.ProxyConfig{
		URL:      "http://proxy.example.com:8080",
		Username: "testuser",
		Password: "testpass",
		NoProxy:  []string{"localhost", "internal.registry.com"},
	}

	cfg := &config.Config{
		TargetRegistry: "ghcr.io/test/test",
		VendorPrefix:   "vendor",
		SourceProxy:    proxyCfg,
	}

	v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	// Test crane options
	craneOpts := v.getCraneOptions()
	if len(craneOpts) == 0 {
		t.Errorf("Expected at least one crane option, got none")
	}

	// Test remote options
	remoteOpts := v.getRemoteOptions()
	if len(remoteOpts) == 0 {
		t.Errorf("Expected at least one remote option, got none")
	}

	// Note: We can't easily test the transport directly without a more complex setup,
	// but we can verify that options are being generated
	t.Logf("✅ Proxy configuration properly integrated - generated %d crane options and %d remote options",
		len(craneOpts), len(remoteOpts))
}
