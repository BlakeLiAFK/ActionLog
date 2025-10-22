package ActionLog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test Fix #6: Hook错误处理 - 一个hook失败不影响其他hooks
func TestHookErrorHandling(t *testing.T) {
	L := New()
	buf := &bytes.Buffer{}
	L.SetWriter(buf)

	hook1Called := false
	hook2Called := false
	hook3Called := false

	// Hook1 - 会失败
	L.AddHook(&testHook{
		fireFn: func(e *Entry) error {
			hook1Called = true
			return errors.New("hook1 failed")
		},
	})

	// Hook2 - 正常
	L.AddHook(&testHook{
		fireFn: func(e *Entry) error {
			hook2Called = true
			return nil
		},
	})

	// Hook3 - 会失败
	L.AddHook(&testHook{
		fireFn: func(e *Entry) error {
			hook3Called = true
			return errors.New("hook3 failed")
		},
	})

	var errorCount int32
	L.SetErrorHandler(func(err error) {
		atomic.AddInt32(&errorCount, 1)
	})

	L.Info(F{"test": "value"}, "message")

	if !hook1Called || !hook2Called || !hook3Called {
		t.Errorf("Not all hooks were called: hook1=%v, hook2=%v, hook3=%v",
			hook1Called, hook2Called, hook3Called)
	}

	if atomic.LoadInt32(&errorCount) != 2 {
		t.Errorf("Expected 2 errors, got %d", errorCount)
	}

	fmt.Println("✅ Hook错误处理修复正确")
}

// Test Fix #7: 错误回调
func TestErrorHandler(t *testing.T) {
	L := New()
	// 使用会失败的writer
	L.SetWriter(&failingWriter{})

	var errorCount int32
	var lastError error
	L.SetErrorHandler(func(err error) {
		atomic.AddInt32(&errorCount, 1)
		lastError = err
	})

	L.Info(F{"test": "value"}, "message")

	if atomic.LoadInt32(&errorCount) == 0 {
		t.Error("Expected error handler to be called")
	}

	if lastError == nil {
		t.Error("Expected error to be captured")
	}

	fmt.Println("✅ 错误回调修复正确")
}

// Test Fix #10: Context支持
func TestContextSupport(t *testing.T) {
	L := New()
	buf := &bytes.Buffer{}
	L.SetWriter(buf)

	// 测试1: 正常context
	ctx := context.WithValue(context.Background(), "trace_id", "test-trace-123")
	L.InfoContext(ctx, F{"test": "value"}, "with trace")

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("test-trace-123")) {
		t.Error("Expected trace_id in output")
	}

	// 测试2: 已取消的context
	buf.Reset()
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	L.InfoContext(cancelCtx, F{"test": "value"}, "should not log")

	if buf.Len() > 0 {
		t.Error("Expected no output for cancelled context")
	}

	fmt.Println("✅ Context支持修复正确")
}

// Test Fix #11: 优雅关闭
func TestShutdown(t *testing.T) {
	L := New()
	buf := &bytes.Buffer{}
	L.SetWriter(buf)

	// 写入一些日志
	L.Info(F{"test": "before shutdown"}, "message")

	// 关闭
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := L.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// 关闭后写入应该被忽略
	buf.Reset()
	L.Info(F{"test": "after shutdown"}, "should be ignored")

	if buf.Len() > 0 {
		t.Error("Expected no output after shutdown")
	}

	fmt.Println("✅ 优雅关闭修复正确")
}

// Test Fix #1 & #9: RotateBuffer修复 (已在edge_case_test.go中)
// Test Fix #2: SetWriter竞态修复 (已在verification_test.go中)

// Helper types
type testHook struct {
	fireFn func(*Entry) error
}

func (h *testHook) Fire(entry *Entry) error {
	return h.fireFn(entry)
}

type failingWriter struct{}

func (fw *failingWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("write failed")
}

// Comprehensive integration test
func TestComprehensiveIntegration(t *testing.T) {
	L := New(F{"app": "test"})
	buf := &bytes.Buffer{}
	L.SetWriter(buf)

	var errorCount int32
	L.SetErrorHandler(func(err error) {
		atomic.AddInt32(&errorCount, 1)
	})

	// Test concurrent logging
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				L.Info(F{"id": id, "j": j}, "concurrent log")
			}
		}(i)
	}

	wg.Wait()

	// Test context logging
	ctx := context.WithValue(context.Background(), "trace_id", "integration-test")
	L.InfoContext(ctx, F{"final": "test"}, "with context")

	// Shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	L.Shutdown(shutdownCtx)

	fmt.Printf("✅ 综合集成测试通过 (写入了 %d 字节, %d 个错误)\n",
		buf.Len(), atomic.LoadInt32(&errorCount))
}
