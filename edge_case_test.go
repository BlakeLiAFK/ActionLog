package ActionLog

import (
	"fmt"
	"testing"
	"time"
)

// 测试极端边界情况
func TestRotateBufferEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		maxSize   int
		data      []string
		expectOk  bool
	}{
		{
			name:     "空buffer",
			maxSize:  1,
			data:     []string{},
			expectOk: true,
		},
		{
			name:     "1字节",
			maxSize:  1,
			data:     []string{"A"},
			expectOk: false, // 应该panic
		},
		{
			name:     "2字节",
			maxSize:  2,
			data:     []string{"AB"},
			expectOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r != nil && tt.expectOk {
					t.Errorf("❌ 意外panic: %v", r)
					fmt.Printf("❌ %s: panic=%v\n", tt.name, r)
				} else if r == nil && !tt.expectOk {
					fmt.Printf("⚠️  %s: 预期panic但没有\n", tt.name)
				} else if r != nil {
					fmt.Printf("✅ %s: 预期panic，确实panic了: %v\n", tt.name, r)
				} else {
					fmt.Printf("✅ %s: 正常\n", tt.name)
				}
			}()

			buf := NewRotateBuffer()
			buf.MaxSize = tt.maxSize

			buf.OnRotate(func(data []byte, t0, t1 time.Time, num int) {
				fmt.Printf("  Rotate: len(data)=%d, data=%q\n", len(data), data)
			})

			for _, d := range tt.data {
				buf.Write([]byte(d))
			}

			buf.Close()
		})
	}
}

// 测试rotate函数中的切片操作
func TestRotateSliceBug(t *testing.T) {
	buf := NewRotateBuffer()
	buf.MaxSize = 1

	panicCount := 0
	buf.OnRotate(func(data []byte, t0, t1 time.Time, num int) {
		defer func() {
			if r := recover(); r != nil {
				panicCount++
				fmt.Printf("❌ OnRotate中panic: %v, len(data)=%d\n", r, len(data))
			}
		}()
		fmt.Printf("OnRotate: len=%d, data=%q\n", len(data), data)
		// rotate函数内部会执行 data[:len(data)-2]
	})

	// 写入1字节，强制rotate
	buf.Write([]byte("X"))
	buf.Write([]byte("Y")) // 触发rotate

	buf.Close()

	if panicCount > 0 {
		fmt.Printf("❌ 检测到 %d 次panic\n", panicCount)
	}
}
