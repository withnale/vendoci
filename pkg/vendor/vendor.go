package vendor

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/rhodesp/vendoci/pkg/config"
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
	Source    string
	Target    string
	Tag       string
	Status    SyncStatus
	Error     error
	Skipped   bool
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

// SyncAll syncs all images defined in the configuration
func (v *Vendor) SyncAll(ctx context.Context, opts SyncOptions) ([]SyncResult, error) {
	var results []SyncResult

	for _, imageConfig := range v.config.Images {
		imageResults, err := v.SyncImage(ctx, imageConfig, opts)
		if err != nil {
			return results, fmt.Errorf("syncing image %s: %w", imageConfig.Source, err)
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
	}

	return opts
}