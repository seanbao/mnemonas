package api

import "testing"

func TestConstants(t *testing.T) {
	// Verify constants are properly defined
	tests := []struct {
		name  string
		value int
		min   int
	}{
		{"MaxConcurrentRequests", DefaultMaxConcurrentRequests, 1},
		{"MaxUploadSize", DefaultMaxUploadSize, 1024},
		{"ScrubRequestBodyLimit", DefaultScrubRequestBodyLimit, 1024},
		{"RequestTimeout", DefaultRequestTimeout, 1},
		{"ScrubTimeout", DefaultScrubTimeout, 1},
		{"GCTimeout", DefaultGCTimeout, 1},
		{"DataplaneConnectTimeout", DefaultDataplaneConnectTimeout, 1},
		{"HealthCheckTimeout", DefaultHealthCheckTimeout, 1},
		{"StatsTimeout", DefaultStatsTimeout, 1},
		{"ListObjectsTimeout", DefaultListObjectsTimeout, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value < tt.min {
				t.Errorf("%s = %d, want >= %d", tt.name, tt.value, tt.min)
			}
		})
	}
}

func TestAppMetadata(t *testing.T) {
	if AppName == "" {
		t.Error("AppName should not be empty")
	}
	if AppVersion == "" {
		t.Error("AppVersion should not be empty")
	}
}
