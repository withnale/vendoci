package vendor

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
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
		Target: config.TargetConfig{
			Registry: "ghcr.io/poptart-testing/poptart",
			Prefix:   "vendor",
			Auth: config.AuthConfig{
				Username: "poptart-testing",
				Password: githubToken,
			},
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
		Target: config.TargetConfig{
			Registry: "ghcr.io/poptart-testing/poptart",
			Prefix:   "vendor",
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
		Target: config.TargetConfig{
			Registry: "ghcr.io/poptart-testing/poptart",
			Prefix:   "vendor",
		},
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
				Source: config.SourceConfig{Proxy: tt.proxy},
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

// TestCreateDestinationProxyTransport tests destination proxy transport creation
func TestCreateDestinationProxyTransport(t *testing.T) {
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
				Target: config.TargetConfig{Proxy: tt.proxy},
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			transport := v.createDestinationProxyTransport()

			if transport == nil {
				t.Errorf("createDestinationProxyTransport() returned nil")
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

// TestProxyURLEmptyUserinfoStripped tests that a proxy URL with empty userinfo
// (e.g. "http://:@proxy:8080", a common convention for "no auth") does not
// cause the transport to send a present-but-empty Proxy-Authorization header,
// which many proxies reject as a failed auth attempt rather than treating as anonymous.
func TestProxyURLEmptyUserinfoStripped(t *testing.T) {
	tests := []struct {
		name         string
		proxy        *config.ProxyConfig
		wantUserNil  bool
		wantUsername string
		wantPassword string
	}{
		{
			name:        "empty userinfo in URL is stripped",
			proxy:       &config.ProxyConfig{URL: "http://:@proxy.company.com:8080"},
			wantUserNil: true,
		},
		{
			name:        "bare @ with no userinfo at all is stripped",
			proxy:       &config.ProxyConfig{URL: "http://@proxy.company.com:8080"},
			wantUserNil: true,
		},
		{
			name:        "no userinfo in URL and no username/password fields stays nil",
			proxy:       &config.ProxyConfig{URL: "http://proxy.company.com:8080"},
			wantUserNil: true,
		},
		{
			name:         "real credentials via username/password fields are preserved",
			proxy:        &config.ProxyConfig{URL: "http://proxy.company.com:8080", Username: "user", Password: "pass"},
			wantUserNil:  false,
			wantUsername: "user",
			wantPassword: "pass",
		},
		{
			name:         "real credentials embedded directly in the URL are preserved",
			proxy:        &config.ProxyConfig{URL: "http://user:pass@proxy.company.com:8080"},
			wantUserNil:  false,
			wantUsername: "user",
			wantPassword: "pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Source: config.SourceConfig{Proxy: tt.proxy}}
			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

			transport, ok := v.createSourceProxyTransport().(*http.Transport)
			if !ok {
				t.Fatalf("createSourceProxyTransport() did not return *http.Transport")
			}

			req, err := http.NewRequest("GET", "https://docker.io/v2/", nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			resolvedProxyURL, err := transport.Proxy(req)
			if err != nil {
				t.Fatalf("transport.Proxy() error: %v", err)
			}
			if resolvedProxyURL == nil {
				t.Fatalf("expected a proxy URL to be resolved, got nil")
			}

			gotUserNil := resolvedProxyURL.User == nil
			if gotUserNil != tt.wantUserNil {
				t.Errorf("resolved proxy User = %v (nil=%v), want nil=%v", resolvedProxyURL.User, gotUserNil, tt.wantUserNil)
			}

			if !tt.wantUserNil {
				if gotUsername := resolvedProxyURL.User.Username(); gotUsername != tt.wantUsername {
					t.Errorf("username = %q, want %q", gotUsername, tt.wantUsername)
				}
				gotPassword, _ := resolvedProxyURL.User.Password()
				if gotPassword != tt.wantPassword {
					t.Errorf("password = %q, want %q", gotPassword, tt.wantPassword)
				}
			}
		})
	}
}

// TestShouldUseSourceProxy tests source proxy decision logic
func TestShouldUseSourceProxy(t *testing.T) {
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
				Source: config.SourceConfig{Proxy: tt.proxy},
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			result := v.shouldUseSourceProxy(tt.sourceURL)

			if result != tt.expected {
				t.Errorf("shouldUseSourceProxy() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// TestShouldUseDestinationProxy tests destination proxy decision logic
func TestShouldUseDestinationProxy(t *testing.T) {
	tests := []struct {
		name      string
		proxy     *config.ProxyConfig
		targetURL string
		expected  bool
	}{
		{
			name:      "no proxy config",
			proxy:     nil,
			targetURL: "registry.company.com/vendor/alpine",
			expected:  false,
		},
		{
			name: "proxy configured - should use",
			proxy: &config.ProxyConfig{
				URL: "http://proxy.example.com:8080",
			},
			targetURL: "registry.company.com/vendor/alpine",
			expected:  true,
		},
		{
			name: "proxy configured - in no_proxy list",
			proxy: &config.ProxyConfig{
				URL:     "http://proxy.example.com:8080",
				NoProxy: []string{"registry.company.com", "localhost"},
			},
			targetURL: "registry.company.com/vendor/alpine",
			expected:  false,
		},
		{
			name: "proxy configured - not in no_proxy list",
			proxy: &config.ProxyConfig{
				URL:     "http://proxy.example.com:8080",
				NoProxy: []string{"localhost"},
			},
			targetURL: "registry.company.com/vendor/alpine",
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Target: config.TargetConfig{Proxy: tt.proxy},
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			result := v.shouldUseDestinationProxy(tt.targetURL)

			if result != tt.expected {
				t.Errorf("shouldUseDestinationProxy() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// TestGetSourceProxiedHTTPClient tests HTTP client creation with source proxy
func TestGetSourceProxiedHTTPClient(t *testing.T) {
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
				Source: config.SourceConfig{Proxy: tt.proxy},
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			client := v.getSourceProxiedHTTPClient()

			if client == nil {
				if !tt.wantNil {
					t.Errorf("getSourceProxiedHTTPClient() returned nil unexpectedly")
				}
				return
			}

			if tt.wantNil {
				t.Errorf("getSourceProxiedHTTPClient() expected nil, got non-nil")
				return
			}

			// Test that client has the expected properties
			if client.Timeout == 0 {
				t.Errorf("Expected client timeout to be set")
			}
		})
	}
}

// TestGetDestinationProxiedHTTPClient tests HTTP client creation with destination proxy
func TestGetDestinationProxiedHTTPClient(t *testing.T) {
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
				Target: config.TargetConfig{Proxy: tt.proxy},
			}

			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
			client := v.getDestinationProxiedHTTPClient()

			if client == nil {
				if !tt.wantNil {
					t.Errorf("getDestinationProxiedHTTPClient() returned nil unexpectedly")
				}
				return
			}

			if tt.wantNil {
				t.Errorf("getDestinationProxiedHTTPClient() expected nil, got non-nil")
				return
			}

			// Test that client has the expected properties
			if client.Timeout == 0 {
				t.Errorf("Expected client timeout to be set")
			}
		})
	}
}

// TestProxyConfigInOptions tests that source and destination proxy configuration are
// properly and independently applied to crane and remote options
func TestProxyConfigInOptions(t *testing.T) {
	sourceProxyCfg := &config.ProxyConfig{
		URL:      "http://source-proxy.example.com:8080",
		Username: "testuser",
		Password: "testpass",
		NoProxy:  []string{"localhost", "internal.registry.com"},
	}

	destProxyCfg := &config.ProxyConfig{
		URL:      "http://dest-proxy.example.com:8080",
		Username: "testuser2",
		Password: "testpass2",
		NoProxy:  []string{"localhost"},
	}

	cfg := &config.Config{
		Target: config.TargetConfig{
			Registry: "ghcr.io/test/test",
			Prefix:   "vendor",
			Proxy:    destProxyCfg,
		},
		Source: config.SourceConfig{
			Proxy: sourceProxyCfg,
		},
	}

	v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	// Test destination crane options (used for target-registry-only crane calls)
	destCraneOpts := v.getDestCraneOptions()
	if len(destCraneOpts) == 0 {
		t.Errorf("Expected at least one destination crane option, got none")
	}

	// Test source remote options (used when pulling from source)
	sourceRemoteOpts := v.getSourceRemoteOptions()
	if len(sourceRemoteOpts) == 0 {
		t.Errorf("Expected at least one source remote option, got none")
	}

	// Test destination remote options (used when pushing to target)
	destRemoteOpts := v.getDestRemoteOptions()
	if len(destRemoteOpts) == 0 {
		t.Errorf("Expected at least one destination remote option, got none")
	}

	// Note: We can't easily test the transport directly without a more complex setup,
	// but we can verify that options are being generated independently for each side
	t.Logf("✅ Proxy configuration properly integrated - generated %d dest crane options, %d source remote options, %d dest remote options",
		len(destCraneOpts), len(sourceRemoteOpts), len(destRemoteOpts))
}

func boolPtr(b bool) *bool { return &b }

// TestTLSVerifyTransport tests that tls_verify (site-level and proxy-level) controls
// InsecureSkipVerify on the built transport, independently for source and target
func TestTLSVerifyTransport(t *testing.T) {
	tests := []struct {
		name             string
		source           config.SourceConfig
		target           config.TargetConfig
		wantSourceSecure bool
		wantTargetSecure bool
	}{
		{
			name:             "no config - verification stays on for both sides",
			wantSourceSecure: true,
			wantTargetSecure: true,
		},
		{
			name:             "source.tls_verify false skips verification for source only",
			source:           config.SourceConfig{TLSVerify: boolPtr(false)},
			wantSourceSecure: false,
			wantTargetSecure: true,
		},
		{
			name:             "target.tls_verify false skips verification for target only",
			target:           config.TargetConfig{TLSVerify: boolPtr(false)},
			wantSourceSecure: true,
			wantTargetSecure: false,
		},
		{
			name:             "source.tls_verify true explicitly keeps verification on",
			source:           config.SourceConfig{TLSVerify: boolPtr(true)},
			wantSourceSecure: true,
			wantTargetSecure: true,
		},
		{
			name: "source.proxy.tls_verify false skips verification for source without a site-level override",
			source: config.SourceConfig{
				Proxy: &config.ProxyConfig{URL: "https://proxy.example.com:8443", TLSVerify: boolPtr(false)},
			},
			wantSourceSecure: false,
			wantTargetSecure: true,
		},
		{
			name: "target.proxy.tls_verify false skips verification for target without a site-level override",
			target: config.TargetConfig{
				Proxy: &config.ProxyConfig{URL: "https://proxy.example.com:8443", TLSVerify: boolPtr(false)},
			},
			wantSourceSecure: true,
			wantTargetSecure: false,
		},
		{
			name: "a proxy with tls_verify unset does not disable verification",
			source: config.SourceConfig{
				Proxy: &config.ProxyConfig{URL: "http://proxy.example.com:8080"},
			},
			wantSourceSecure: true,
			wantTargetSecure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Source: tt.source, Target: tt.target}
			v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

			sourceTransport, ok := v.createSourceProxyTransport().(*http.Transport)
			if !ok {
				t.Fatalf("createSourceProxyTransport() did not return *http.Transport")
			}
			gotSourceSecure := sourceTransport.TLSClientConfig == nil || !sourceTransport.TLSClientConfig.InsecureSkipVerify
			if gotSourceSecure != tt.wantSourceSecure {
				t.Errorf("source transport secure = %v, want %v", gotSourceSecure, tt.wantSourceSecure)
			}

			destTransport, ok := v.createDestinationProxyTransport().(*http.Transport)
			if !ok {
				t.Fatalf("createDestinationProxyTransport() did not return *http.Transport")
			}
			gotTargetSecure := destTransport.TLSClientConfig == nil || !destTransport.TLSClientConfig.InsecureSkipVerify
			if gotTargetSecure != tt.wantTargetSecure {
				t.Errorf("target transport secure = %v, want %v", gotTargetSecure, tt.wantTargetSecure)
			}
		})
	}
}

// TestNeedsCustomTransport tests when a non-default transport must be built
func TestNeedsCustomTransport(t *testing.T) {
	tests := []struct {
		name     string
		proxy    *config.ProxyConfig
		tlsVerif *bool
		want     bool
	}{
		{name: "nothing configured", want: false},
		{name: "proxy configured", proxy: &config.ProxyConfig{URL: "http://proxy.example.com:8080"}, want: true},
		{name: "tls_verify explicitly true", tlsVerif: boolPtr(true), want: false},
		{name: "tls_verify explicitly false", tlsVerif: boolPtr(false), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsCustomTransport(tt.proxy, tt.tlsVerif)
			if got != tt.want {
				t.Errorf("needsCustomTransport() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckSourceVsTargetImageExistsUsesCorrectProxy is a regression test for a bug where
// CheckImageExists (used by `vendoci check` for BOTH the source and target existence check)
// always built its remote.Options from the destination/target proxy settings, so a source
// registry check was silently routed through the target's proxy instead of the source's.
// It reproduces this by pointing source.proxy and target.proxy at two different unreachable
// SOCKS5 ports and asserting each existence check fails via its OWN proxy port, not the other's.
func TestCheckSourceVsTargetImageExistsUsesCorrectProxy(t *testing.T) {
	cfg := &config.Config{
		Source: config.SourceConfig{
			Proxy: &config.ProxyConfig{URL: "socks5://127.0.0.1:1"},
		},
		Target: config.TargetConfig{
			Registry: "registry.example.com",
			Prefix:   "vendor",
			Proxy:    &config.ProxyConfig{URL: "socks5://127.0.0.1:2"},
		},
	}

	v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
	ctx := context.Background()

	_, sourceErr := v.CheckSourceImageExists(ctx, "ghcr.io/example/source-image:latest")
	if sourceErr == nil {
		t.Fatalf("expected an error dialing the unreachable source proxy, got none")
	}
	if !strings.Contains(sourceErr.Error(), "127.0.0.1:1") {
		t.Errorf("CheckSourceImageExists error should reference the SOURCE proxy (127.0.0.1:1), got: %v", sourceErr)
	}
	if strings.Contains(sourceErr.Error(), "127.0.0.1:2") {
		t.Errorf("CheckSourceImageExists error references the TARGET proxy (127.0.0.1:2) - this is the leak bug: %v", sourceErr)
	}

	_, targetErr := v.CheckImageExists(ctx, "registry.example.com/vendor/example-image:latest")
	if targetErr == nil {
		t.Fatalf("expected an error dialing the unreachable target proxy, got none")
	}
	if !strings.Contains(targetErr.Error(), "127.0.0.1:2") {
		t.Errorf("CheckImageExists error should reference the TARGET proxy (127.0.0.1:2), got: %v", targetErr)
	}
	if strings.Contains(targetErr.Error(), "127.0.0.1:1") {
		t.Errorf("CheckImageExists error references the SOURCE proxy (127.0.0.1:1) - unexpected cross-wiring: %v", targetErr)
	}
}

// TestSourcePullDoesNotUseTargetCredentials is a regression test for a bug where source
// registry pulls (getSourceRemoteOptions) authenticated using the TARGET registry's
// credentials, so public sources like docker.io/ghcr.io rejected them
// ("incorrect username or password" / "DENIED: denied") because those credentials
// belong to an entirely different registry. It spins up a fake registry that captures
// whatever Authorization header a source pull sends, and asserts it never contains the
// target's Basic credentials when source.auth is left unset.
func TestSourcePullDoesNotUseTargetCredentials(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")

	cfg := &config.Config{
		// Source.Auth intentionally left unset - this is the common case (public source)
		Target: config.TargetConfig{
			Registry: "registry.example.com",
			Prefix:   "vendor",
			Auth: config.AuthConfig{
				Username: "target-user",
				Password: "target-pass",
			},
		},
	}
	v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	ref, err := name.ParseReference(host + "/some/repo:latest")
	if err != nil {
		t.Fatalf("parsing test reference: %v", err)
	}

	_, _ = remote.Get(ref, v.getSourceRemoteOptions()...)

	if strings.Contains(gotAuthHeader, "target-user") {
		t.Fatalf("source pull sent the TARGET's Basic credentials to a source registry: Authorization: %s", gotAuthHeader)
	}
	wantEncoded := base64.StdEncoding.EncodeToString([]byte("target-user:target-pass"))
	if strings.Contains(gotAuthHeader, wantEncoded) {
		t.Fatalf("source pull sent the TARGET's base64-encoded Basic credentials to a source registry: Authorization: %s", gotAuthHeader)
	}
	t.Logf("source pull sent Authorization: %q (expected: no target credentials)", gotAuthHeader)

	// Now verify source.auth, when explicitly set, IS used for source pulls (proving
	// the fix wires source auth through, rather than just suppressing target auth)
	cfg.Source.Auth = config.AuthConfig{Username: "source-user", Password: "source-pass"}
	v2 := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
	_, _ = remote.Get(ref, v2.getSourceRemoteOptions()...)

	wantSourceEncoded := base64.StdEncoding.EncodeToString([]byte("source-user:source-pass"))
	if !strings.Contains(gotAuthHeader, wantSourceEncoded) {
		t.Errorf("expected source pull to use source.auth credentials, got Authorization: %q", gotAuthHeader)
	}
}

// TestImageExistsTreatsStructuredNotFoundAsAbsent is a regression test for a bug where
// imageExists only recognized "not found" by pattern-matching the literal strings "404"
// or "not found" in err.Error(). A real registry returning a structured OCI distribution
// error body (e.g. NAME_UNKNOWN: "repository name not known to registry") over HTTP 404
// produces an error string with NEITHER of those substrings, so it was misclassified as
// a hard error (logged as "Warning: Could not check if target exists") instead of simply
// meaning the target doesn't exist yet - the normal, expected case for a first-time sync.
func TestImageExistsTreatsStructuredNotFoundAsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"code":"NAME_UNKNOWN","message":"repository name not known to registry","detail":{"name":"vendor/docker.io/envoyproxy/gateway-crds-helm"}}]}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")

	cfg := &config.Config{
		Target: config.TargetConfig{Registry: host, Prefix: "vendor"},
	}
	v := New(cfg, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	exists, err := v.CheckImageExists(context.Background(), host+"/vendor/docker.io/envoyproxy/gateway-crds-helm:v1.9.1")
	if err != nil {
		t.Fatalf("expected a structured NAME_UNKNOWN response to be treated as \"does not exist\" (nil error), got: %v", err)
	}
	if exists {
		t.Errorf("expected exists=false for a NAME_UNKNOWN response, got true")
	}
}

// TestIsNotFound tests the not-found classification directly against both a
// structured transport.Error (as real registries send) and a plain error
func TestIsNotFound(t *testing.T) {
	structuredErr := &transport.Error{
		StatusCode: http.StatusNotFound,
		Errors: []transport.Diagnostic{
			{Code: transport.NameUnknownErrorCode, Message: "repository name not known to registry"},
		},
	}
	if !isNotFound(structuredErr) {
		t.Errorf("expected a 404 transport.Error with a NAME_UNKNOWN diagnostic to be treated as not found, got: %v", structuredErr.Error())
	}
	if strings.Contains(structuredErr.Error(), "404") {
		t.Fatalf("test assumption violated: structured error string unexpectedly contains \"404\": %q", structuredErr.Error())
	}

	forbiddenErr := &transport.Error{StatusCode: http.StatusForbidden}
	if isNotFound(forbiddenErr) {
		t.Errorf("expected a 403 transport.Error to NOT be treated as not found")
	}

	if !isNotFound(errors.New("unexpected status code 404 Not Found")) {
		t.Errorf("expected a plain error containing \"404\" to still be treated as not found")
	}
	if isNotFound(errors.New("connection refused")) {
		t.Errorf("expected an unrelated plain error to NOT be treated as not found")
	}
}
