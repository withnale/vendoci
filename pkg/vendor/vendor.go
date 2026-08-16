package vendor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
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
	DryRun   bool
	Verbose  bool
	Force    bool // Force re-sync even if image exists
	Parallel int  // Number of syncs to run concurrently (defaults to 1 if < 1)
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

// syncUnit represents a single image:tag or chart:version to sync
type syncUnit struct {
	isHelm      bool
	chartName   string
	imageConfig config.ImageConfig
	tag         string
}

// SyncAllWithGroups syncs images and charts with optional group filtering.
// Sync units are processed concurrently, bounded by opts.Parallel (default 1).
func (v *Vendor) SyncAllWithGroups(ctx context.Context, opts SyncOptions, requestedGroups []string) ([]SyncResult, error) {
	// Get all images with their groups, respecting sync flags and group filters
	groupedImages := v.config.GetAllImagesFiltered(requestedGroups)

	var units []syncUnit
	for _, groupedImg := range groupedImages {
		for _, tag := range groupedImg.Image.Tags {
			units = append(units, syncUnit{
				isHelm:      groupedImg.Type == "helm",
				chartName:   groupedImg.ChartName,
				imageConfig: groupedImg.Image,
				tag:         tag,
			})
		}
	}

	results := make([]SyncResult, len(units))

	parallelism := opts.Parallel
	if parallelism < 1 {
		parallelism = 1
	}

	// Semaphore to bound concurrency
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	for i, unit := range units {
		wg.Add(1)
		go func(idx int, u syncUnit) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			if u.isHelm {
				results[idx] = v.syncSingleHelmChart(ctx, u.imageConfig.Source, u.chartName, u.tag, opts)
				if results[idx].Status == SyncStatusFailed && opts.Verbose {
					v.logger.Printf("Failed to sync chart %s:%s: %v", u.chartName, u.tag, results[idx].Error)
				}
			} else {
				results[idx] = v.syncSingleImage(ctx, u.imageConfig, u.tag, opts)
				if results[idx].Status == SyncStatusFailed && opts.Verbose {
					v.logger.Printf("Failed to sync %s:%s: %v", u.imageConfig.Source, u.tag, results[idx].Error)
				}
			}
		}(i, unit)
	}

	wg.Wait()

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
		targetBase = fmt.Sprintf("%s/%s/%s", v.config.Target.Registry, v.config.Target.Prefix, imageConfig.TargetOverride)
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
	targetBase := fmt.Sprintf("%s/%s/charts/%s", v.config.Target.Registry, v.config.Target.Prefix, chartName)
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

	client := v.getSourceProxiedHTTPClient()
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

	client = v.getSourceProxiedHTTPClient()
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
	// Create Helm registry client, routing pushes through the destination proxy/TLS settings if configured
	var helmClientOpts []registry.ClientOption
	if v.config.Target.Auth.Username != "" && v.config.Target.Auth.Password != "" {
		helmClientOpts = append(helmClientOpts, registry.ClientOptBasicAuth(v.config.Target.Auth.Username, v.config.Target.Auth.Password))
	}
	if needsCustomTransport(v.config.Target.Proxy, v.config.Target.TLSVerify) {
		helmClientOpts = append(helmClientOpts, registry.ClientOptHTTPClient(v.getDestinationProxiedHTTPClient()))
	}

	helmClient, err := registry.NewClient(helmClientOpts...)
	if err != nil {
		return fmt.Errorf("creating Helm registry client: %w", err)
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

// CheckImageExists checks if an image exists in the target registry (public method)
func (v *Vendor) CheckImageExists(ctx context.Context, imageRef string) (bool, error) {
	return v.imageExists(ctx, imageRef)
}

// CheckSourceImageExists checks if an image exists in a source registry (public method),
// using source proxy/TLS/auth settings rather than the target's
func (v *Vendor) CheckSourceImageExists(ctx context.Context, imageRef string) (bool, error) {
	return v.sourceImageExists(ctx, imageRef)
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

	client := v.getSourceProxiedHTTPClient()
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
	return imageExistsWithOptions(imageRef, v.getDestRemoteOptions())
}

// sourceImageExists checks if an image exists in a source registry, using
// source proxy/TLS/auth settings rather than the target's
func (v *Vendor) sourceImageExists(ctx context.Context, imageRef string) (bool, error) {
	return imageExistsWithOptions(imageRef, v.getSourceRemoteOptions())
}

// imageExistsWithOptions checks if an image exists in a registry using the given remote options
func imageExistsWithOptions(imageRef string, opts []remote.Option) (bool, error) {
	// Parse the image reference
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return false, fmt.Errorf("parsing image reference: %w", err)
	}

	// Try to get the manifest
	_, err = remote.Get(ref, opts...)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		// Other errors are actual errors
		return false, err
	}

	return true, nil
}

// isNotFound reports whether err represents a "doesn't exist" response from the
// registry (e.g. an unknown repository or manifest). Registries typically return
// a structured OCI distribution error body (NAME_UNKNOWN, MANIFEST_UNKNOWN, etc.)
// alongside HTTP 404, and go-containerregistry's transport.Error.Error() does NOT
// include the numeric status code in its string form when such a body is present -
// so this checks the typed StatusCode field rather than pattern-matching err.Error().
func isNotFound(err error) bool {
	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode == http.StatusNotFound
	}
	// Fall back to substring matching for errors that aren't a *transport.Error
	return strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found")
}

// copyImage copies an image from source to target, pulling through the source
// proxy (if configured) and pushing through the destination proxy (if configured)
func (v *Vendor) copyImage(ctx context.Context, source, target string) error {
	srcRef, err := name.ParseReference(source)
	if err != nil {
		return fmt.Errorf("parsing source reference: %w", err)
	}

	dstRef, err := name.ParseReference(target)
	if err != nil {
		return fmt.Errorf("parsing target reference: %w", err)
	}

	puller, err := remote.NewPuller(v.getSourceRemoteOptions()...)
	if err != nil {
		return fmt.Errorf("creating puller: %w", err)
	}

	desc, err := puller.Get(ctx, srcRef)
	if err != nil {
		return fmt.Errorf("fetching source image: %w", err)
	}

	pusher, err := remote.NewPusher(v.getDestRemoteOptions()...)
	if err != nil {
		return fmt.Errorf("creating pusher: %w", err)
	}

	if err := pusher.Push(ctx, dstRef, desc); err != nil {
		return fmt.Errorf("pushing target image: %w", err)
	}

	return nil
}

// getDestCraneOptions returns crane options for operations against the target registry
func (v *Vendor) getDestCraneOptions() []crane.Option {
	var opts []crane.Option

	// Add authentication if configured
	if v.config.Target.Auth.Username != "" && v.config.Target.Auth.Password != "" {
		auth := &authn.Basic{
			Username: v.config.Target.Auth.Username,
			Password: v.config.Target.Auth.Password,
		}
		opts = append(opts, crane.WithAuth(auth))
	} else {
		// Fall back to default keychain (Docker credentials)
		opts = append(opts, crane.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// Add proxy/TLS transport for destination operations only
	if needsCustomTransport(v.config.Target.Proxy, v.config.Target.TLSVerify) {
		transport := v.createDestinationProxyTransport()
		opts = append(opts, crane.WithTransport(transport))
	}

	return opts
}

// getSourceRemoteOptions returns remote options for operations against source registries
func (v *Vendor) getSourceRemoteOptions() []remote.Option {
	var opts []remote.Option

	// Add authentication if configured for the source registry. Most sources are
	// anonymous, so this intentionally does NOT fall back to the target's
	// credentials - those belong to a different registry entirely.
	if v.config.Source.Auth.Username != "" && v.config.Source.Auth.Password != "" {
		auth := &authn.Basic{
			Username: v.config.Source.Auth.Username,
			Password: v.config.Source.Auth.Password,
		}
		opts = append(opts, remote.WithAuth(auth))
	} else {
		// Fall back to default keychain (Docker credentials)
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// Add proxy/TLS transport for source operations only
	if needsCustomTransport(v.config.Source.Proxy, v.config.Source.TLSVerify) {
		transport := v.createSourceProxyTransport()
		opts = append(opts, remote.WithTransport(transport))
	}

	return opts
}

// getDestRemoteOptions returns remote options for operations against the target registry
func (v *Vendor) getDestRemoteOptions() []remote.Option {
	var opts []remote.Option

	// Add authentication if configured
	if v.config.Target.Auth.Username != "" && v.config.Target.Auth.Password != "" {
		auth := &authn.Basic{
			Username: v.config.Target.Auth.Username,
			Password: v.config.Target.Auth.Password,
		}
		opts = append(opts, remote.WithAuth(auth))
	} else {
		// Fall back to default keychain (Docker credentials)
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// Add proxy/TLS transport for destination operations only
	if needsCustomTransport(v.config.Target.Proxy, v.config.Target.TLSVerify) {
		transport := v.createDestinationProxyTransport()
		opts = append(opts, remote.WithTransport(transport))
	}

	return opts
}

// createSourceProxyTransport creates an HTTP transport for source requests, applying the
// configured source proxy and/or TLS verification overrides (source.tls_verify, source.proxy.tls_verify)
func (v *Vendor) createSourceProxyTransport() http.RoundTripper {
	return createTransport(v.config.Source.Proxy, v.config.Source.TLSVerify)
}

// createDestinationProxyTransport creates an HTTP transport for destination (target registry)
// requests, applying the configured destination proxy and/or TLS verification overrides
// (target.tls_verify, target.proxy.tls_verify)
func (v *Vendor) createDestinationProxyTransport() http.RoundTripper {
	return createTransport(v.config.Target.Proxy, v.config.Target.TLSVerify)
}

// needsCustomTransport determines whether a non-default http.Transport must be built,
// i.e. a proxy is configured or TLS verification has been explicitly overridden
func needsCustomTransport(proxyCfg *config.ProxyConfig, tlsVerify *bool) bool {
	if proxyCfg != nil {
		return true
	}
	return tlsVerify != nil && !*tlsVerify
}

// createTransport builds an http.Transport from a ProxyConfig and a site-level TLS
// verification override. siteTLSVerify set to false skips verification for the registry
// connection; proxyCfg.TLSVerify set to false additionally skips verification for the
// connection to the proxy itself (only meaningful when the proxy URL uses https).
func createTransport(proxyCfg *config.ProxyConfig, siteTLSVerify *bool) *http.Transport {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	insecure := siteTLSVerify != nil && !*siteTLSVerify

	if proxyCfg != nil {
		if proxyCfg.URL != "" {
			proxyURL, err := url.Parse(proxyCfg.URL)
			if err == nil {
				// Add proxy authentication if provided
				if proxyCfg.Username != "" {
					proxyURL.User = url.UserPassword(
						proxyCfg.Username,
						proxyCfg.Password,
					)
				}

				// Strip empty userinfo (e.g. a url of "http://:@proxy:8080", or
				// username/password both left unset) so Go's transport doesn't
				// send a present-but-empty Proxy-Authorization header - many
				// proxies reject that as a failed auth attempt rather than
				// treating it as anonymous. See net/http's connectMethod.proxyAuth,
				// which sends Basic auth whenever URL.User is non-nil, regardless
				// of whether the credentials inside it are actually empty.
				if u := proxyURL.User; u != nil {
					password, _ := u.Password()
					if u.Username() == "" && password == "" {
						proxyURL.User = nil
					}
				}

				transport.Proxy = http.ProxyURL(proxyURL)
			}
		}

		if proxyCfg.TLSVerify != nil && !*proxyCfg.TLSVerify {
			insecure = true
		}
	}

	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit opt-in via tls_verify: false
	}

	return transport
}

// shouldUseSourceProxy determines if the source proxy should be used for a given source URL
func (v *Vendor) shouldUseSourceProxy(sourceURL string) bool {
	return shouldUseProxy(v.config.Source.Proxy, sourceURL)
}

// shouldUseDestinationProxy determines if the destination proxy should be used for a given target URL
func (v *Vendor) shouldUseDestinationProxy(targetURL string) bool {
	return shouldUseProxy(v.config.Target.Proxy, targetURL)
}

// shouldUseProxy determines if a proxy should be used for a given URL, honoring no_proxy
func shouldUseProxy(proxyCfg *config.ProxyConfig, targetURL string) bool {
	if proxyCfg == nil {
		return false
	}

	// Check no_proxy list
	for _, noProxy := range proxyCfg.NoProxy {
		if strings.Contains(targetURL, noProxy) {
			return false
		}
	}

	return true
}

// getSourceProxiedHTTPClient creates an HTTP client with source proxy configuration for Helm operations
func (v *Vendor) getSourceProxiedHTTPClient() *http.Client {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	if needsCustomTransport(v.config.Source.Proxy, v.config.Source.TLSVerify) {
		client.Transport = v.createSourceProxyTransport()
	}

	return client
}

// getDestinationProxiedHTTPClient creates an HTTP client with destination proxy configuration for Helm operations
func (v *Vendor) getDestinationProxiedHTTPClient() *http.Client {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	if needsCustomTransport(v.config.Target.Proxy, v.config.Target.TLSVerify) {
		client.Transport = v.createDestinationProxyTransport()
	}

	return client
}

// ListTargetImages lists all images in the target registry under the vendor prefix
func (v *Vendor) ListTargetImages(ctx context.Context) ([]string, error) {
	var images []string

	// Use crane's Catalog function which properly handles authentication
	repos, err := crane.Catalog(v.config.Target.Registry, v.getDestCraneOptions()...)
	if err != nil {
		return nil, fmt.Errorf("listing catalog: %w", err)
	}

	// Filter repositories that start with vendor prefix
	vendorPrefix := v.config.Target.Prefix + "/"
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
	fullRepo := fmt.Sprintf("%s/%s", v.config.Target.Registry, repo)
	tags, err := crane.ListTags(fullRepo, v.getDestCraneOptions()...)
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
	desc, err := remote.Get(ref, v.getDestRemoteOptions()...)
	if err != nil {
		return fmt.Errorf("getting manifest: %w", err)
	}

	// Delete using the digest
	err = remote.Delete(ref, v.getDestRemoteOptions()...)
	if err != nil {
		return fmt.Errorf("deleting image: %w", err)
	}

	v.logger.Printf("Deleted %s (digest: %s)", imageRef, desc.Digest)
	return nil
}
