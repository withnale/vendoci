package vendor

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/rhodesp/vendoci/pkg/config"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/registry"
)

// Vendor handles the vendoring of OCI images
type Vendor struct {
	config *config.Config
	logger *log.Logger
}

// New creates a new Vendor instance
func New(cfg *config.Config, logger *log.Logger) *Vendor {
	return &Vendor{
		config: cfg,
		logger: logger,
	}
}

// SyncOptions contains options for the sync operation
type SyncOptions struct {
	DryRun  bool
	Verbose bool
	Force   bool // Force re-sync even if image exists
}

// SyncResult represents the result of a sync operation
type SyncResult struct {
	Source     string
	Target     string
	Tag        string
	Status     SyncStatus
	Error      error
	Skipped    bool
	SkipReason string
}

// SyncStatus represents the status of a sync operation
type SyncStatus int

const (
	SyncStatusPending SyncStatus = iota
	SyncStatusSuccess
	SyncStatusFailed
	SyncStatusSkipped
)

func (s SyncStatus) String() string {
	switch s {
	case SyncStatusPending:
		return "pending"
	case SyncStatusSuccess:
		return "success"
	case SyncStatusFailed:
		return "failed"
	case SyncStatusSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// SyncAll syncs all images and charts defined in the configuration
// If requestedGroups is provided, only syncs those groups regardless of sync flag
// Otherwise, syncs only groups where sync is true (or not specified)
func (v *Vendor) SyncAll(ctx context.Context, opts SyncOptions) ([]SyncResult, error) {
	return v.SyncAllWithGroups(ctx, opts, nil)
}

// SyncAllWithGroups syncs images and charts with optional group filtering
func (v *Vendor) SyncAllWithGroups(ctx context.Context, opts SyncOptions, requestedGroups []string) ([]SyncResult, error) {
	var results []SyncResult

	// Get all images with their groups, respecting sync flags and group filters
	groupedImages := v.config.GetAllImagesFiltered(requestedGroups)

	for _, groupedImg := range groupedImages {
		var imageResults []SyncResult
		var err error

		// Handle based on type
		if groupedImg.Type == "helm" {
			// Sync Helm chart
			imageResults, err = v.SyncHelmChart(ctx, groupedImg, opts)
		} else {
			// Sync OCI image
			imageResults, err = v.SyncImage(ctx, groupedImg.Image, opts)
		}

		if err != nil {
			return results, fmt.Errorf("syncing %s %s: %w", groupedImg.Type, groupedImg.Image.Source, err)
		}
		results = append(results, imageResults...)
	}

	return results, nil
}

// SyncImage syncs all tags for a specific image
func (v *Vendor) SyncImage(ctx context.Context, imageConfig config.ImageConfig, opts SyncOptions) ([]SyncResult, error) {
	var results []SyncResult

	for _, tag := range imageConfig.Tags {
		result := v.syncSingleImage(ctx, imageConfig, tag, opts)
		results = append(results, result)

		// If this is not a dry run and sync failed, we might want to continue or stop
		// For now, we'll continue with other tags
		if result.Status == SyncStatusFailed && opts.Verbose {
			v.logger.Printf("Failed to sync %s:%s: %v", imageConfig.Source, tag, result.Error)
		}
	}

	return results, nil
}

// syncSingleImage syncs a single image:tag combination
func (v *Vendor) syncSingleImage(ctx context.Context, imageConfig config.ImageConfig, tag string, opts SyncOptions) SyncResult {
	sourceImage := fmt.Sprintf("%s:%s", imageConfig.Source, tag)

	// Generate target image name
	var targetBase string
	if imageConfig.TargetOverride != "" {
		targetBase = fmt.Sprintf("%s/%s/%s", v.config.TargetRegistry, v.config.VendorPrefix, imageConfig.TargetOverride)
	} else {
		targetBase = v.config.GenerateTargetImage(imageConfig.Source)
	}
	targetImage := fmt.Sprintf("%s:%s", targetBase, tag)

	result := SyncResult{
		Source: sourceImage,
		Target: targetImage,
		Tag:    tag,
		Status: SyncStatusPending,
	}

	if opts.Verbose {
		v.logger.Printf("Syncing %s -> %s", sourceImage, targetImage)
	}

	// Check if target already exists (unless force is specified)
	if !opts.Force {
		exists, err := v.imageExists(ctx, targetImage)
		if err != nil {
			if opts.Verbose {
				v.logger.Printf("Warning: Could not check if target exists: %v", err)
			}
		} else if exists {
			result.Status = SyncStatusSkipped
			result.Skipped = true
			result.SkipReason = "Image already exists (use --force to override)"
			if opts.Verbose {
				v.logger.Printf("Skipping %s (already exists)", targetImage)
			}
			return result
		}
	}

	// Dry run - just simulate
	if opts.DryRun {
		result.Status = SyncStatusSuccess
		v.logger.Printf("DRY RUN: Would copy %s -> %s", sourceImage, targetImage)
		return result
	}

	// Perform the actual copy
	err := v.copyImage(ctx, sourceImage, targetImage)
	if err != nil {
		result.Status = SyncStatusFailed
		result.Error = err
		return result
	}

	result.Status = SyncStatusSuccess
	if opts.Verbose {
		v.logger.Printf("Successfully synced %s -> %s", sourceImage, targetImage)
	}

	return result
}

// SyncHelmChart syncs a Helm chart from a Helm repository to the target registry as an OCI artifact
func (v *Vendor) SyncHelmChart(ctx context.Context, groupedImg config.GroupedImage, opts SyncOptions) ([]SyncResult, error) {
	var results []SyncResult

	chartName := groupedImg.ChartName
	img := groupedImg.Image

	for _, version := range img.Tags {
		result := v.syncSingleHelmChart(ctx, img.Source, chartName, version, opts)
		results = append(results, result)

		if result.Status == SyncStatusFailed && opts.Verbose {
			v.logger.Printf("Failed to sync chart %s:%s: %v", chartName, version, result.Error)
		}
	}

	return results, nil
}

// syncSingleHelmChart syncs a single Helm chart version
func (v *Vendor) syncSingleHelmChart(ctx context.Context, repoURL, chartName, version string, opts SyncOptions) SyncResult {
	sourceRef := fmt.Sprintf("%s:%s", repoURL, version)

	// Generate target OCI reference: {registry}/{vendor-prefix}/charts/{chartName}:{version}
	targetBase := fmt.Sprintf("%s/%s/charts/%s", v.config.TargetRegistry, v.config.VendorPrefix, chartName)
	targetRef := fmt.Sprintf("%s:%s", targetBase, version)

	result := SyncResult{
		Source: sourceRef,
		Target: targetRef,
		Tag:    version,
		Status: SyncStatusPending,
	}

	if opts.Verbose {
		v.logger.Printf("Syncing Helm chart %s version %s -> %s", chartName, version, targetRef)
	}

	// Check if target already exists (unless force is specified)
	if !opts.Force {
		exists, err := v.imageExists(ctx, targetRef)
		if err != nil {
			if opts.Verbose {
				v.logger.Printf("Warning: Could not check if target exists: %v", err)
			}
		} else if exists {
			result.Status = SyncStatusSkipped
			result.Skipped = true
			result.SkipReason = "Chart already exists (use --force to override)"
			if opts.Verbose {
				v.logger.Printf("Skipping %s (already exists)", targetRef)
			}
			return result
		}
	}

	// Dry run - just simulate
	if opts.DryRun {
		result.Status = SyncStatusSuccess
		v.logger.Printf("DRY RUN: Would sync chart %s version %s -> %s", chartName, version, targetRef)
		return result
	}

	// Download chart to memory and push as OCI artifact
	err := v.syncHelmChartToOCI(ctx, repoURL, chartName, version, targetRef)
	if err != nil {
		result.Status = SyncStatusFailed
		result.Error = err
		return result
	}

	result.Status = SyncStatusSuccess
	if opts.Verbose {
		v.logger.Printf("Successfully synced chart %s -> %s", chartName, targetRef)
	}

	return result
}

// syncHelmChartToOCI downloads a Helm chart from a repository and pushes it to the target as an OCI artifact
func (v *Vendor) syncHelmChartToOCI(ctx context.Context, repoURL, chartName, version string, targetRef string) error {
	// Step 1: Fetch the Helm repository index to find the chart URL
	indexURL := strings.TrimSuffix(repoURL, "/") + "/index.yaml"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return fmt.Errorf("creating index request: %w", err)
	}

	client := v.getProxiedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("index fetch failed with status %d", resp.StatusCode)
	}

	// Parse the index
	indexData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading index: %w", err)
	}

	var index HelmRepoIndex
	if err := yaml.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("parsing index: %w", err)
	}

	// Step 2: Find the chart version and get its download URL
	chartVersions, exists := index.Entries[chartName]
	if !exists {
		return fmt.Errorf("chart %s not found in repository", chartName)
	}

	var chartURL string
	for _, cv := range chartVersions {
		if cv.Version == version {
			if len(cv.URLs) > 0 {
				chartURL = cv.URLs[0]
			}
			break
		}
	}

	if chartURL == "" {
		return fmt.Errorf("chart %s version %s not found or has no download URL", chartName, version)
	}

	// Make URL absolute if it's relative
	if !strings.HasPrefix(chartURL, "http://") && !strings.HasPrefix(chartURL, "https://") {
		chartURL = strings.TrimSuffix(repoURL, "/") + "/" + strings.TrimPrefix(chartURL, "/")
	}

	// Step 3: Download the chart .tgz file to memory
	chartReq, err := http.NewRequestWithContext(ctx, http.MethodGet, chartURL, nil)
	if err != nil {
		return fmt.Errorf("creating chart download request: %w", err)
	}

	client = v.getProxiedHTTPClient()
	chartResp, err := client.Do(chartReq)
	if err != nil {
		return fmt.Errorf("downloading chart: %w", err)
	}
	defer chartResp.Body.Close()

	if chartResp.StatusCode != http.StatusOK {
		return fmt.Errorf("chart download failed with status %d", chartResp.StatusCode)
	}

	// Read chart data into memory
	chartData, err := io.ReadAll(chartResp.Body)
	if err != nil {
		return fmt.Errorf("reading chart data: %w", err)
	}

	// Step 4: Push the chart to OCI registry using Helm's native registry client
	// Create Helm registry client
	helmClient, err := registry.NewClient()
	if err != nil {
		return fmt.Errorf("creating Helm registry client: %w", err)
	}

	// Configure authentication if available
	if v.config.Auth.Username != "" && v.config.Auth.Password != "" {
		helmClient, err = registry.NewClient(
			registry.ClientOptBasicAuth(v.config.Auth.Username, v.config.Auth.Password),
		)
		if err != nil {
			return fmt.Errorf("creating Helm registry client with auth: %w", err)
		}
	}

	// Push chart to OCI registry using Helm's Push method
	// The target ref should be in OCI format: oci://{registry}/{path}
	ociRef := fmt.Sprintf("oci://%s", targetRef)

	_, err = helmClient.Push(chartData, ociRef)
	if err != nil {
		return fmt.Errorf("pushing chart to OCI registry: %w", err)
	}

	return nil
}

// CheckImageExists checks if an image exists in a registry (public method)
func (v *Vendor) CheckImageExists(ctx context.Context, imageRef string) (bool, error) {
	return v.imageExists(ctx, imageRef)
}

// HelmRepoIndex represents the structure of a Helm repository index.yaml
type HelmRepoIndex struct {
	Entries map[string][]HelmChartVersion `yaml:"entries"`
}

// HelmChartVersion represents a single version entry in the Helm index
type HelmChartVersion struct {
	Version string   `yaml:"version"`
	URLs    []string `yaml:"urls"`
}

// CheckHelmChartExists checks if a Helm chart exists in a repository
// repoURL should be the Helm repository URL (e.g., "https://charts.jetstack.io")
// chartName is the chart name (e.g., "cert-manager")
// version is the chart version (e.g., "v1.15.2")
func (v *Vendor) CheckHelmChartExists(ctx context.Context, repoURL, chartName, version string) (bool, error) {
	// Fetch the repository index.yaml
	indexURL := strings.TrimSuffix(repoURL, "/") + "/index.yaml"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return false, fmt.Errorf("creating request: %w", err)
	}

	client := v.getProxiedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("fetching index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read and parse the index
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("reading index: %w", err)
	}

	var index HelmRepoIndex
	if err := yaml.Unmarshal(body, &index); err != nil {
		return false, fmt.Errorf("parsing index: %w", err)
	}

	// Check if the chart exists
	chartVersions, exists := index.Entries[chartName]
	if !exists {
		return false, nil
	}

	// Check if the specific version exists
	for _, cv := range chartVersions {
		if cv.Version == version {
			return true, nil
		}
	}

	return false, nil
}

// imageExists checks if an image exists in the target registry
func (v *Vendor) imageExists(ctx context.Context, imageRef string) (bool, error) {
	// Parse the image reference
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return false, fmt.Errorf("parsing image reference: %w", err)
	}

	// Try to get the manifest
	_, err = remote.Get(ref, v.getRemoteOptions()...)
	if err != nil {
		// If it's a "not found" error, the image doesn't exist
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		// Other errors are actual errors
		return false, err
	}

	return true, nil
}

// copyImage copies an image from source to target using crane
func (v *Vendor) copyImage(ctx context.Context, source, target string) error {
	// Use crane to copy the image
	err := crane.Copy(source, target, v.getCraneOptions()...)
	if err != nil {
		return fmt.Errorf("copying image: %w", err)
	}

	return nil
}

// getCraneOptions returns crane options based on configuration
func (v *Vendor) getCraneOptions() []crane.Option {
	var opts []crane.Option

	// Add authentication if configured
	if v.config.Auth.Username != "" && v.config.Auth.Password != "" {
		auth := &authn.Basic{
			Username: v.config.Auth.Username,
			Password: v.config.Auth.Password,
		}
		opts = append(opts, crane.WithAuth(auth))
	} else {
		// Fall back to default keychain (Docker credentials)
		opts = append(opts, crane.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// Add proxy transport for source operations only
	if v.config.SourceProxy != nil {
		transport := v.createSourceProxyTransport()
		opts = append(opts, crane.WithTransport(transport))
	}

	return opts
}

// getRemoteOptions returns remote options based on configuration
func (v *Vendor) getRemoteOptions() []remote.Option {
	var opts []remote.Option

	// Add authentication if configured
	if v.config.Auth.Username != "" && v.config.Auth.Password != "" {
		auth := &authn.Basic{
			Username: v.config.Auth.Username,
			Password: v.config.Auth.Password,
		}
		opts = append(opts, remote.WithAuth(auth))
	} else {
		// Fall back to default keychain (Docker credentials)
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// Add proxy transport for source operations only
	if v.config.SourceProxy != nil {
		transport := v.createSourceProxyTransport()
		opts = append(opts, remote.WithTransport(transport))
	}

	return opts
}

// createSourceProxyTransport creates an HTTP transport with proxy configuration for source requests
func (v *Vendor) createSourceProxyTransport() http.RoundTripper {
	transport := &http.Transport{
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	if v.config.SourceProxy != nil && v.config.SourceProxy.URL != "" {
		proxyURL, err := url.Parse(v.config.SourceProxy.URL)
		if err == nil {
			// Add proxy authentication if provided
			if v.config.SourceProxy.Username != "" {
				proxyURL.User = url.UserPassword(
					v.config.SourceProxy.Username,
					v.config.SourceProxy.Password,
				)
			}
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return transport
}

// shouldUseProxy determines if proxy should be used for a given source URL
func (v *Vendor) shouldUseProxy(sourceURL string) bool {
	if v.config.SourceProxy == nil {
		return false
	}

	// Check no_proxy list
	for _, noProxy := range v.config.SourceProxy.NoProxy {
		if strings.Contains(sourceURL, noProxy) {
			return false
		}
	}

	return true
}

// getProxiedHTTPClient creates an HTTP client with proxy configuration for Helm operations
func (v *Vendor) getProxiedHTTPClient() *http.Client {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	if v.config.SourceProxy != nil {
		client.Transport = v.createSourceProxyTransport()
	}

	return client
}

// ListTargetImages lists all images in the target registry under the vendor prefix
func (v *Vendor) ListTargetImages(ctx context.Context) ([]string, error) {
	var images []string

	// Use crane's Catalog function which properly handles authentication
	repos, err := crane.Catalog(v.config.TargetRegistry, v.getCraneOptions()...)
	if err != nil {
		return nil, fmt.Errorf("listing catalog: %w", err)
	}

	// Filter repositories that start with vendor prefix
	vendorPrefix := v.config.VendorPrefix + "/"
	for _, repo := range repos {
		if strings.HasPrefix(repo, vendorPrefix) {
			// For each repository, list all tags
			repoImages, err := v.listRepositoryTags(ctx, repo)
			if err != nil {
				v.logger.Printf("Warning: Could not list tags for %s: %v", repo, err)
				continue
			}
			images = append(images, repoImages...)
		}
	}

	return images, nil
}

// listRepositoryTags lists all tags for a specific repository
func (v *Vendor) listRepositoryTags(ctx context.Context, repo string) ([]string, error) {
	var images []string

	// Use crane's ListTags function which properly handles authentication
	fullRepo := fmt.Sprintf("%s/%s", v.config.TargetRegistry, repo)
	tags, err := crane.ListTags(fullRepo, v.getCraneOptions()...)
	if err != nil {
		return nil, fmt.Errorf("listing tags for %s: %w", repo, err)
	}

	// Build full image references
	for _, tag := range tags {
		imageRef := fmt.Sprintf("%s:%s", fullRepo, tag)
		images = append(images, imageRef)
	}

	return images, nil
}

// DeleteImage deletes an image from the target registry
func (v *Vendor) DeleteImage(ctx context.Context, imageRef string) error {
	// Parse the image reference
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parsing image reference: %w", err)
	}

	// Get the manifest digest first (required for deletion)
	desc, err := remote.Get(ref, v.getRemoteOptions()...)
	if err != nil {
		return fmt.Errorf("getting manifest: %w", err)
	}

	// Delete using the digest
	err = remote.Delete(ref, v.getRemoteOptions()...)
	if err != nil {
		return fmt.Errorf("deleting image: %w", err)
	}

	v.logger.Printf("Deleted %s (digest: %s)", imageRef, desc.Digest)
	return nil
}
