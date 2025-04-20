// Package api provides REST API and gRPC API
package api

// API limits and configuration constants
const (
	// DefaultMaxConcurrentRequests is the maximum number of concurrent requests
	DefaultMaxConcurrentRequests = 100

	// DefaultMaxUploadSize is the maximum upload file size (10GB)
	DefaultMaxUploadSize = 10 * 1024 * 1024 * 1024

	// DefaultScrubRequestBodyLimit is the maximum scrub request body size (1MB)
	DefaultScrubRequestBodyLimit = 1 * 1024 * 1024

	// DefaultJSONRequestBodyLimit is the maximum JSON request body size for non-upload endpoints (1MB)
	DefaultJSONRequestBodyLimit = 1 * 1024 * 1024

	// DefaultRequestTimeout is the default request timeout (30 minutes)
	DefaultRequestTimeout = 30 * 60

	// DefaultScrubTimeout is the scrub operation timeout (30 minutes)
	DefaultScrubTimeout = 30 * 60

	// DefaultGCTimeout is the garbage collection timeout (30 minutes)
	DefaultGCTimeout = 30 * 60

	// DefaultDataplaneConnectTimeout is the dataplane connection timeout (5 seconds)
	DefaultDataplaneConnectTimeout = 5

	// DefaultHealthCheckTimeout is the health check timeout (2 seconds)
	DefaultHealthCheckTimeout = 2

	// DefaultStatsTimeout is the stats query timeout (5 seconds)
	DefaultStatsTimeout = 5

	// DefaultListObjectsTimeout is the list objects timeout (30 seconds)
	DefaultListObjectsTimeout = 30

	// AppName is the application name
	AppName = "MnemoNAS"

	// AppVersion is the fallback application version when the binary did not inject build metadata.
	AppVersion = "dev"

	// AppBuildTime is the fallback build time when the binary did not inject build metadata.
	AppBuildTime = "unknown"
)
