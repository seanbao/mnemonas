// Package metrics provides request metrics collection for MnemoNAS
package metrics

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNewRequestMetrics(t *testing.T) {
	m := NewRequestMetrics()

	if m == nil {
		t.Fatal("NewRequestMetrics() returned nil")
	}

	if m.methodCounts == nil {
		t.Error("methodCounts should be initialized")
	}
}

func TestRecordRequest(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("GET", "/test", 200, 10*time.Millisecond, 100, 200)
	m.RecordRequest("POST", "/api", 201, 20*time.Millisecond, 500, 100)
	m.RecordRequest("GET", "/error", 500, 5*time.Millisecond, 0, 50)

	stats := m.GetStats()

	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", stats.TotalRequests)
	}

	if stats.MethodCounts["GET"] != 2 {
		t.Errorf("GET count = %d, want 2", stats.MethodCounts["GET"])
	}

	if stats.MethodCounts["POST"] != 1 {
		t.Errorf("POST count = %d, want 1", stats.MethodCounts["POST"])
	}

	if stats.Count2xx != 2 {
		t.Errorf("Count2xx = %d, want 2", stats.Count2xx)
	}

	if stats.Count5xx != 1 {
		t.Errorf("Count5xx = %d, want 1", stats.Count5xx)
	}
}

func TestErrorRate(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("GET", "/ok", 200, time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/ok", 200, time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/bad", 400, time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/err", 500, time.Millisecond, 0, 0)

	stats := m.GetStats()

	expectedErrorRate := 0.5
	if stats.ErrorRate != expectedErrorRate {
		t.Errorf("ErrorRate = %f, want %f", stats.ErrorRate, expectedErrorRate)
	}
}

func TestLatencyTracking(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("GET", "/fast", 200, 10*time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/slow", 200, 100*time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/med", 200, 50*time.Millisecond, 0, 0)

	stats := m.GetStats()

	if stats.MaxLatencyMs < 99 || stats.MaxLatencyMs > 101 {
		t.Errorf("MaxLatencyMs = %f, want ~100", stats.MaxLatencyMs)
	}

	expectedAvg := float64(10+100+50) / 3
	if stats.AvgLatencyMs < expectedAvg-1 || stats.AvgLatencyMs > expectedAvg+1 {
		t.Errorf("AvgLatencyMs = %f, want ~%f", stats.AvgLatencyMs, expectedAvg)
	}
}

func TestSlowRequests(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("GET", "/fast", 200, 50*time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/slow1", 200, 150*time.Millisecond, 0, 0)
	m.RecordRequest("GET", "/slow2", 200, 200*time.Millisecond, 0, 0)

	stats := m.GetStats()

	if len(stats.SlowRequests) != 2 {
		t.Errorf("SlowRequests count = %d, want 2", len(stats.SlowRequests))
	}
}

func TestSlowRequestsLimit(t *testing.T) {
	m := NewRequestMetrics()

	for i := 0; i < 15; i++ {
		m.RecordRequest("GET", "/slow", 200, 150*time.Millisecond, 0, 0)
	}

	stats := m.GetStats()

	if len(stats.SlowRequests) != 10 {
		t.Errorf("SlowRequests count = %d, want 10 (max)", len(stats.SlowRequests))
	}
}

func TestThroughput(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("GET", "/", 200, time.Millisecond, 1000, 2000)
	m.RecordRequest("POST", "/", 200, time.Millisecond, 5000, 100)

	stats := m.GetStats()

	if stats.BytesIn != 6000 {
		t.Errorf("BytesIn = %d, want 6000", stats.BytesIn)
	}

	if stats.BytesOut != 2100 {
		t.Errorf("BytesOut = %d, want 2100", stats.BytesOut)
	}
}

func TestRecordRequest_IgnoresUnknownByteCounts(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("POST", "/upload", 200, time.Millisecond, -1, -1)

	stats := m.GetStats()
	if stats.BytesIn != 0 {
		t.Errorf("BytesIn = %d, want 0 for unknown content length", stats.BytesIn)
	}
	if stats.BytesOut != 0 {
		t.Errorf("BytesOut = %d, want 0 for unknown response size", stats.BytesOut)
	}
}

func TestMetricsMiddleware_IgnoresUnknownRequestContentLength(t *testing.T) {
	globalMetrics.Reset()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := MetricsMiddleware(handler)
	req := httptest.NewRequest("POST", "/upload", http.NoBody)
	req.ContentLength = -1
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	stats := globalMetrics.GetStats()
	if stats.BytesIn != 0 {
		t.Errorf("BytesIn = %d, want 0 when ContentLength is unknown", stats.BytesIn)
	}
	if stats.BytesOut != 2 {
		t.Errorf("BytesOut = %d, want 2", stats.BytesOut)
	}
}

func TestReset(t *testing.T) {
	m := NewRequestMetrics()

	m.RecordRequest("GET", "/", 200, time.Millisecond, 100, 200)
	m.RecordRequest("POST", "/", 500, time.Millisecond, 0, 0)

	m.Reset()

	stats := m.GetStats()

	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests after reset = %d, want 0", stats.TotalRequests)
	}

	if stats.Count5xx != 0 {
		t.Errorf("Count5xx after reset = %d, want 0", stats.Count5xx)
	}

	if stats.BytesIn != 0 {
		t.Errorf("BytesIn after reset = %d, want 0", stats.BytesIn)
	}
}

func TestGlobal(t *testing.T) {
	g := Global()

	if g == nil {
		t.Fatal("Global() returned nil")
	}

	g2 := Global()
	if g != g2 {
		t.Error("Global() should return same instance")
	}
}

func TestMetricsMiddleware(t *testing.T) {
	globalMetrics.Reset()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})

	wrapped := MetricsMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}

	stats := globalMetrics.GetStats()

	if stats.TotalRequests < 1 {
		t.Error("Middleware should record request")
	}
}

func TestResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: w, status: 200}

	rw.WriteHeader(404)
	if rw.status != 404 {
		t.Errorf("status = %d, want 404", rw.status)
	}

	n, _ := rw.Write([]byte("test data"))
	if rw.bytesWritten != n {
		t.Errorf("bytesWritten = %d, want %d", rw.bytesWritten, n)
	}
}

func TestResponseWriterKeepsFirstStatus(t *testing.T) {
	w := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

	rw.WriteHeader(http.StatusCreated)
	rw.WriteHeader(http.StatusInternalServerError)

	if rw.status != http.StatusCreated {
		t.Fatalf("status = %d, want first status %d", rw.status, http.StatusCreated)
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("underlying status = %d, want %d", w.Code, http.StatusCreated)
	}
	if rw.Unwrap() != w {
		t.Fatal("expected Unwrap to return the underlying response writer")
	}
}

func TestUptime(t *testing.T) {
	m := NewRequestMetrics()
	m.startTimeUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())

	stats := m.GetStats()

	if stats.UptimeSecs < 1 {
		t.Fatalf("UptimeSecs = %d, want at least 1", stats.UptimeSecs)
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := NewRequestMetrics()
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				m.RecordRequest("GET", "/", 200, time.Millisecond, 10, 20)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	stats := m.GetStats()

	if stats.TotalRequests != 1000 {
		t.Errorf("TotalRequests = %d, want 1000", stats.TotalRequests)
	}
}

func TestConcurrentResetAndGetStats(t *testing.T) {
	m := NewRequestMetrics()
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			m.Reset()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = m.GetStats()
		}
	}()
	wg.Wait()
}
