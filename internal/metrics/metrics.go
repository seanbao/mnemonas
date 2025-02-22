// Package metrics provides request metrics collection for MnemoNAS
package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// RequestMetrics tracks request statistics
type RequestMetrics struct {
	mu sync.RWMutex

	// Request counts by method
	methodCounts map[string]*atomic.Int64

	// Error counts by status code category
	errorCount2xx atomic.Int64
	errorCount4xx atomic.Int64
	errorCount5xx atomic.Int64

	// Latency tracking (in microseconds)
	latencySum   atomic.Int64
	latencyCount atomic.Int64
	latencyMax   atomic.Int64

	// Per-endpoint latency (top 10 slowest)
	slowRequests   []slowRequest
	slowRequestsMu sync.Mutex

	// Throughput tracking
	bytesIn  atomic.Int64
	bytesOut atomic.Int64

	// Start time for uptime calculation
	startTime time.Time
}

type slowRequest struct {
	Method   string
	Path     string
	Duration time.Duration
	Time     time.Time
}

// NewRequestMetrics creates a new metrics collector
func NewRequestMetrics() *RequestMetrics {
	return &RequestMetrics{
		methodCounts: make(map[string]*atomic.Int64),
		slowRequests: make([]slowRequest, 0, 10),
		startTime:    time.Now(),
	}
}

// RecordRequest records a request with its duration and status
func (m *RequestMetrics) RecordRequest(method, path string, status int, duration time.Duration, bytesIn, bytesOut int64) {
	if bytesIn < 0 {
		bytesIn = 0
	}
	if bytesOut < 0 {
		bytesOut = 0
	}

	// Count by method
	m.mu.Lock()
	counter, ok := m.methodCounts[method]
	if !ok {
		counter = &atomic.Int64{}
		m.methodCounts[method] = counter
	}
	m.mu.Unlock()
	counter.Add(1)

	// Count by status category
	switch {
	case status >= 200 && status < 300:
		m.errorCount2xx.Add(1)
	case status >= 400 && status < 500:
		m.errorCount4xx.Add(1)
	case status >= 500:
		m.errorCount5xx.Add(1)
	}

	// Track latency
	durationUs := duration.Microseconds()
	m.latencySum.Add(durationUs)
	m.latencyCount.Add(1)

	// Update max latency (compare and swap)
	for {
		oldMax := m.latencyMax.Load()
		if durationUs <= oldMax {
			break
		}
		if m.latencyMax.CompareAndSwap(oldMax, durationUs) {
			break
		}
	}

	// Track slow requests
	if duration > 100*time.Millisecond {
		m.recordSlowRequest(method, path, duration)
	}

	// Track throughput
	m.bytesIn.Add(bytesIn)
	m.bytesOut.Add(bytesOut)
}

func (m *RequestMetrics) recordSlowRequest(method, path string, duration time.Duration) {
	m.slowRequestsMu.Lock()
	defer m.slowRequestsMu.Unlock()

	req := slowRequest{
		Method:   method,
		Path:     path,
		Duration: duration,
		Time:     time.Now(),
	}

	// Keep only top 10 slowest requests
	if len(m.slowRequests) < 10 {
		m.slowRequests = append(m.slowRequests, req)
	} else {
		// Find the fastest request in the list
		minIdx := 0
		for i, r := range m.slowRequests {
			if r.Duration < m.slowRequests[minIdx].Duration {
				minIdx = i
			}
		}
		// Replace if this one is slower
		if duration > m.slowRequests[minIdx].Duration {
			m.slowRequests[minIdx] = req
		}
	}
}

// Stats returns current metrics snapshot
type Stats struct {
	// Request counts
	TotalRequests int64            `json:"total_requests"`
	MethodCounts  map[string]int64 `json:"method_counts"`

	// Error rates
	Count2xx  int64   `json:"count_2xx"`
	Count4xx  int64   `json:"count_4xx"`
	Count5xx  int64   `json:"count_5xx"`
	ErrorRate float64 `json:"error_rate"` // 4xx + 5xx / total

	// Latency (in milliseconds)
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	MaxLatencyMs float64 `json:"max_latency_ms"`

	// Throughput
	BytesIn       int64   `json:"bytes_in"`
	BytesOut      int64   `json:"bytes_out"`
	ThroughputMBs float64 `json:"throughput_mbs"` // MB/s average

	// Slow requests
	SlowRequests []SlowRequestInfo `json:"slow_requests,omitempty"`

	// Uptime
	UptimeSecs int64 `json:"uptime_secs"`
}

// SlowRequestInfo for JSON output
type SlowRequestInfo struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	DurationMs int64  `json:"duration_ms"`
	Time       string `json:"time"`
}

// GetStats returns current metrics
func (m *RequestMetrics) GetStats() Stats {
	m.mu.RLock()
	methodCounts := make(map[string]int64)
	for method, counter := range m.methodCounts {
		methodCounts[method] = counter.Load()
	}
	m.mu.RUnlock()

	var totalRequests int64
	for _, count := range methodCounts {
		totalRequests += count
	}

	count2xx := m.errorCount2xx.Load()
	count4xx := m.errorCount4xx.Load()
	count5xx := m.errorCount5xx.Load()

	latencySum := m.latencySum.Load()
	latencyCount := m.latencyCount.Load()
	latencyMax := m.latencyMax.Load()

	var avgLatencyMs float64
	if latencyCount > 0 {
		avgLatencyMs = float64(latencySum) / float64(latencyCount) / 1000
	}

	var errorRate float64
	if totalRequests > 0 {
		errorRate = float64(count4xx+count5xx) / float64(totalRequests)
	}

	bytesIn := m.bytesIn.Load()
	bytesOut := m.bytesOut.Load()
	uptime := time.Since(m.startTime).Seconds()
	var throughputMBs float64
	if uptime > 0 {
		throughputMBs = float64(bytesIn+bytesOut) / 1024 / 1024 / uptime
	}

	// Get slow requests
	m.slowRequestsMu.Lock()
	slowReqs := make([]SlowRequestInfo, len(m.slowRequests))
	for i, r := range m.slowRequests {
		slowReqs[i] = SlowRequestInfo{
			Method:     r.Method,
			Path:       r.Path,
			DurationMs: r.Duration.Milliseconds(),
			Time:       r.Time.Format(time.RFC3339),
		}
	}
	m.slowRequestsMu.Unlock()

	return Stats{
		TotalRequests: totalRequests,
		MethodCounts:  methodCounts,
		Count2xx:      count2xx,
		Count4xx:      count4xx,
		Count5xx:      count5xx,
		ErrorRate:     errorRate,
		AvgLatencyMs:  avgLatencyMs,
		MaxLatencyMs:  float64(latencyMax) / 1000,
		BytesIn:       bytesIn,
		BytesOut:      bytesOut,
		ThroughputMBs: throughputMBs,
		SlowRequests:  slowReqs,
		UptimeSecs:    int64(uptime),
	}
}

// Reset clears all metrics
func (m *RequestMetrics) Reset() {
	m.mu.Lock()
	m.methodCounts = make(map[string]*atomic.Int64)
	m.mu.Unlock()

	m.errorCount2xx.Store(0)
	m.errorCount4xx.Store(0)
	m.errorCount5xx.Store(0)
	m.latencySum.Store(0)
	m.latencyCount.Store(0)
	m.latencyMax.Store(0)
	m.bytesIn.Store(0)
	m.bytesOut.Store(0)

	m.slowRequestsMu.Lock()
	m.slowRequests = make([]slowRequest, 0, 10)
	m.slowRequestsMu.Unlock()

	m.startTime = time.Now()
}

// Global metrics instance
var globalMetrics = NewRequestMetrics()

// Global returns the global metrics instance
func Global() *RequestMetrics {
	return globalMetrics
}

// MetricsMiddleware wraps an http.Handler to collect metrics
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code and bytes written
		wrapped := &responseWriter{ResponseWriter: w, status: 200}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)

		// Record metrics
		globalMetrics.RecordRequest(
			r.Method,
			r.URL.Path,
			wrapped.status,
			duration,
			r.ContentLength,
			int64(wrapped.bytesWritten),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}
