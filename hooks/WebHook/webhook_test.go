package WebHook

import (
	"fmt"
	"github.com/DGHeroin/ActionLog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ========== 验证问题7: Nil函数调用 ==========
func TestNilFunctionPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("❌ Fire调用nil函数会panic:", r)
			t.Log("确认存在bug: fn未初始化会导致panic")
		} else {
			fmt.Println("✅ 没有panic")
		}
	}()

	hook := NewWebHook(time.Second)
	// 不调用AddHook，fn为nil

	entry := &ActionLog.Entry{
		Data:    ActionLog.F{"test": "value"},
		Time:    time.Now(),
		Message: "test",
	}

	// 这应该会panic
	err := hook.Fire(entry)
	if err != nil {
		t.Log("Fire返回错误:", err)
	}
}

// ========== 验证问题6: HTTP连接泄漏 ==========
func TestHTTPConnectionLeak(t *testing.T) {
	requestCount := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	hook := NewWebHook(100 * time.Millisecond)
	hook.AddHook(server.URL, func(entry *ActionLog.Entry) bool {
		return true
	})

	// 发送多条日志
	for i := 0; i < 5; i++ {
		entry := &ActionLog.Entry{
			Data:    ActionLog.F{"code": i},
			Time:    time.Now(),
			Message: "test",
		}
		hook.Fire(entry)
	}

	time.Sleep(200 * time.Millisecond)
	hook.Drain()

	fmt.Printf("发送了 %d 个HTTP请求\n", atomic.LoadInt32(&requestCount))
	fmt.Println("⚠️  检查代码: HTTP响应体没有Close，存在连接泄漏风险")
}
