# VendOCI

A Go library and CLI tool for vendoring OCI images from public registries into private registries with structured naming conventions.

## Overview

VendOCI helps you systematically mirror container images from public registries (like Docker Hub, GCR, Quay) into your private registry. It transforms the image names to create a clean vendor hierarchy.

### Image Name Transformation

VendOCI transforms source image names into a vendored format:

| Source | Target |
|--------|--------|
| `docker.io/ubuntu:latest` | `myregistry.com/vendor/docker.io/ubuntu:latest` |
| `gcr.io/google-containers/pause:3.8` | `myregistry.com/vendor/gcr.io/google-containers/pause:3.8` |
| `quay.io/prometheus/prometheus:v2.45.0` | `myregistry.com/vendor/quay.io/prometheus/prometheus:v2.45.0` |

## Installation

### From Source

```bash
git clone https://github.com/rhodesp/vendoci.git
cd vendoci
go build -o vendoci ./cmd/vendoci
```

### Using Go Install

```bash
go install github.com/rhodesp/vendoci/cmd/vendoci@latest
```

## Configuration

Create a `.vendoci.yaml` configuration file:

```yaml
# Target registry where images will be vendored
target_registry: "myregistry.example.com"

# Prefix for vendored images (default: "vendor")
vendor_prefix: "vendor"

# Authentication for the target registry
auth:
  username: "your-username"
  password_env: "VENDOCI_PASSWORD"  # Use environment variable for password

# Images to vendor
images:
  - source: "docker.io/ubuntu"
    tags:
      - "latest"
      - "20.04"
      - "22.04"

  - source: "docker.io/nginx"
    tags:
      - "latest"
      - "alpine"
```

See [.vendoci.yaml.example](./.vendoci.yaml.example) for a complete example.

## Usage

### List Images

List all images and tags defined in your configuration:

```bash
# Basic listing
vendoci list

# Show target image names
vendoci list --show-targets
```

### Check Image Presence

Check whether source and destination images exist:

```bash
# Check all images
vendoci check

# Check with parallel requests for faster results
vendoci check --parallel 10

# Check with verbose error output
vendoci check -v
```

### Sync Images

Sync all images defined in your configuration:

```bash
# Basic sync
vendoci sync

# Dry run - see what would be synced
vendoci sync --dry-run

# Force re-sync even if images exist
vendoci sync --force

# Verbose output
vendoci sync --verbose
```

### Sync Specific Images

Override configuration to sync specific images:

```bash
# Sync specific images with default tags
vendoci sync --images docker.io/ubuntu,docker.io/nginx

# Sync specific images with specific tags
vendoci sync --images docker.io/ubuntu --tags latest,20.04
```

## Configuration Reference

### Main Configuration

| Field | Type | Description |
|-------|------|-------------|
| `target_registry` | string | Target registry URL (required) |
| `vendor_prefix` | string | Prefix for vendored images (default: "vendor") |
| `images` | array | List of image configurations |
| `auth` | object | Authentication configuration |

### Image Configuration

| Field | Type | Description |
|-------|------|-------------|
| `source` | string | Source image repository (e.g., "docker.io/ubuntu") |
| `tags` | array | List of tags to vendor |
| `target_override` | string | Optional custom target name instead of transformed source |

### Authentication Configuration

| Field | Type | Description |
|-------|------|-------------|
| `username` | string | Username for target registry |
| `password` | string | Password (not recommended) |
| `password_env` | string | Environment variable containing password |
| `token` | string | Authentication token (not recommended) |
| `token_env` | string | Environment variable containing token |

## Examples

### Corporate Environment

```yaml
target_registry: "artifacts.company.com"
vendor_prefix: "vendor"

auth:
  username: "build-system"
  password_env: "REGISTRY_PASSWORD"

images:
  # Base images for CI/CD
  - source: "docker.io/ubuntu"
    tags: ["20.04", "22.04"]

  - source: "docker.io/golang"
    tags: ["1.21", "1.21-alpine"]

  # Application dependencies
  - source: "docker.io/postgres"
    tags: ["15", "15-alpine"]

  - source: "docker.io/redis"
    tags: ["7-alpine"]
    target_override: "cache.redis"
```

### Development Environment

```yaml
target_registry: "localhost:5000"
vendor_prefix: "dev"

images:
  - source: "docker.io/nginx"
    tags: ["latest"]

  - source: "docker.io/mysql"
    tags: ["8.0"]
```

## Command Reference

### `vendoci list`

Lists all images and tags defined in the configuration file.

**Flags:**
- `--show-targets`: Show target image names in addition to source images
- `--config`: Path to configuration file
- `--verbose, -v`: Verbose output

**Example Output:**
```
SOURCE IMAGE       TAG      OVERRIDE
------------       ---      --------
docker.io/alpine   3.23.0   -
                   latest   
docker.io/ubuntu   22.04    -
                   24.04    

Total: 2 images, 4 tags
```

### `vendoci check`

Checks whether source and destination images exist in their respective registries.

**Flags:**
- `--parallel`: Number of parallel checks to perform (default: 5)
- `--config`: Path to configuration file
- `--verbose, -v`: Verbose output including errors

**Example Output:**
```
SOURCE IMAGE              TAG      SOURCE    DEST      TARGET IMAGE
------------              ---      ------    ----      ------------
docker.io/alpine:3.23.0   3.23.0   PRESENT   MISSING   ghcr.io/myorg/vendor/docker.io/alpine:3.23.0
docker.io/ubuntu:22.04    22.04    PRESENT   PRESENT   ghcr.io/myorg/vendor/docker.io/ubuntu:22.04

Summary:
  Both present: 1
  Source only: 1 (needs sync)
  Target only: 0 (orphaned)
  Both missing: 0
  Total: 2
```

**Status Values:**
- `PRESENT`: Image exists and is accessible
- `MISSING`: Image does not exist
- `UNKNOWN`: Unable to determine (check errors with `-v`)
- `ERROR`: Authentication or network error (verbose mode only)

### `vendoci sync`

Synchronizes OCI images to the target registry.

**Flags:**
- `--dry-run`: Show what would be synced without actually doing it
- `--force`: Force re-sync even if target images already exist
- `--images`: Comma-separated list of specific images to sync
- `--tags`: Comma-separated list of specific tags to sync (requires --images)
- `--config`: Path to configuration file
- `--verbose, -v`: Verbose output

## Authentication

VendOCI supports multiple authentication methods:

### Environment Variables

```bash
export VENDOCI_PASSWORD="your-registry-password"
vendoci sync
```

### Docker Config

VendOCI can use existing Docker authentication:

```bash
docker login myregistry.example.com
vendoci sync
```

## Development

### Building

```bash
go build -o vendoci ./cmd/vendoci
```

### Testing

```bash
go test ./...
```

### Live Reload Development

Using Air for live reloading during development:

```bash
air
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Dependencies

- [spf13/cobra](https://github.com/spf13/cobra) - CLI framework
- [spf13/viper](https://github.com/spf13/viper) - Configuration management
- [google/go-containerregistry](https://github.com/google/go-containerregistry) - OCI/Docker registry client
- [gopkg.in/yaml.v3](https://gopkg.in/yaml.v3) - YAML parsing