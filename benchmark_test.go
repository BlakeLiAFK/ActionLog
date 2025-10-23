package ActionLog

import (
	"context"
	"io"
	"testing"
)

// BenchmarkInfo measures the performance of basic Info logging
func BenchmarkInfo(b *testing.B) {
	logger := New(F{"service": "test", "version": "1.0"})
	logger.SetWriter(io.Discard) // Discard output for pure logging benchmark

	fields := F{"user_id": 123, "action": "test"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logger.Info(fields, "Test message")
	}
}

// BenchmarkInfoContext measures the performance of context-aware logging
func BenchmarkInfoContext(b *testing.B) {
	logger := New(F{"service": "test", "version": "1.0"})
	logger.SetWriter(io.Discard)

	ctx := context.WithValue(context.Background(), "trace_id", "abc-123")
	fields := F{"user_id": 123, "action": "test"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logger.InfoContext(ctx, fields, "Test message")
	}
}

// BenchmarkInfoWithHook measures performance impact of hooks
func BenchmarkInfoWithHook(b *testing.B) {
	logger := New(F{"service": "test"})
	logger.SetWriter(io.Discard)

	// Add a simple hook that does nothing
	logger.AddHook(&noopHook{})

	fields := F{"user_id": 123}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logger.Info(fields, "Test message")
	}
}

// BenchmarkFormatter measures formatting performance
func BenchmarkFormatter(b *testing.B) {
	formatter := &defaultFormatter{}
	entry := &Entry{
		Data: F{
			"user_id": 123,
			"action":  "login",
			"success": true,
			"ip":      "192.168.1.1",
		},
		Message: "User login successful",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		formatter.Format(entry)
	}
}

// BenchmarkConcurrentInfo measures performance under concurrent load
func BenchmarkConcurrentInfo(b *testing.B) {
	logger := New(F{"service": "test"})
	logger.SetWriter(io.Discard)

	fields := F{"user_id": 123}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info(fields, "Concurrent test message")
		}
	})
}

// BenchmarkInfoWithStandardFields measures overhead of standard fields
func BenchmarkInfoWithStandardFields(b *testing.B) {
	// Test with many standard fields
	logger := New(F{
		"service":     "test",
		"version":     "1.0",
		"environment": "prod",
		"host":        "server1",
		"region":      "us-west",
		"datacenter":  "dc1",
		"pod":         "pod-123",
		"container":   "app-1",
	})
	logger.SetWriter(io.Discard)

	fields := F{"user_id": 123}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logger.Info(fields, "Test message")
	}
}

// noopHook is a hook that does nothing, for benchmarking
type noopHook struct{}

func (h *noopHook) Fire(entry *Entry) error {
	return nil
}
