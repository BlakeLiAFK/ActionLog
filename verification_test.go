package ActionLog

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ========== 验证问题1: Formatter值接收者 ==========
func TestFormatterPrefixWorks(t *testing.T) {
	L := New()
	buf := &bytes.Buffer{}
	L.SetWriter(buf)

	// 设置prefix
	L.Formatter().SetPrefix("PREFIX:")

	// 写入日志
	L.Info(F{"test": "value"}, "message")

	output := buf.String()
	fmt.Println("Formatter output:", output)

	// 检查prefix是否存在
	if bytes.Contains([]byte(output), []byte("PREFIX:")) {
		fmt.Println("✅ Formatter prefix WORKS - 我的分析错误")
	} else {
		fmt.Println("❌ Formatter prefix BROKEN - 我的分析正确")
	}
}

// ========== 验证问题2: RotateBuffer数组越界 ==========
func TestRotateBufferPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("❌ RotateBuffer PANICS:", r)
		} else {
			fmt.Println("✅ RotateBuffer不会panic - 但逻辑可能有问题")
		}
	}()

	buf := NewRotateBuffer()
	buf.MaxSize = 10 // 很小的size，强制rotate

	rotateCount := 0
	buf.OnRotate(func(data []byte, t0, t1 time.Time, num int) {
		rotateCount++
		fmt.Printf("Rotated %d: len=%d, data[:min(10,len)]=%q\n",
			rotateCount, len(data), data[:min(len(data), 10)])
	})

	L := New()
	L.SetWriter(buf)

	// 写入少量数据触发rotate
	L.Info(F{"code": 1}, "test")
	L.Info(F{"code": 2}, "test")

	buf.Close()
	fmt.Printf("Total rotations: %d\n", rotateCount)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ========== 验证问题3: RotateBuffer竞态 ==========
func TestRotateBufferRaceCondition(t *testing.T) {
	buf := NewRotateBuffer()
	buf.MaxSize = 100 // 很小

	rotateCount := 0
	var mu sync.Mutex

	buf.OnRotate(func(data []byte, t0, t1 time.Time, num int) {
		mu.Lock()
		rotateCount++
		mu.Unlock()
	})

	L := New()
	L.SetWriter(buf)

	// 并发写入
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				L.Info(F{"id": id, "j": j}, "concurrent test")
			}
		}(i)
	}

	wg.Wait()
	buf.Close()

	fmt.Printf("Concurrent test: %d rotations\n", rotateCount)
	fmt.Println("✅ 没有panic，但可能存在竞态条件")
}

// ========== 验证问题5: WebHook Buffer nil slice ==========
func TestWebHookBufferNilSlice(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("❌ WebHook Buffer PANICS:", r)
		} else {
			fmt.Println("✅ WebHook Buffer不会panic - append(nil)是安全的")
		}
	}()

	// 模拟buffer行为
	buffers := make([][]int, 50) // 50个nil slice
	buffer := buffers[0]          // nil

	// append到nil slice
	buffer = append(buffer, 1, 2, 3)

	fmt.Printf("Append到nil slice成功: len=%d, cap=%d, data=%v\n",
		len(buffer), cap(buffer), buffer)
}

// ========== 验证问题8: SetWriter竞态 ==========
func TestSetWriterRace(t *testing.T) {
	L := New()

	// 启动写入goroutine
	stop := make(chan bool)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				L.Info(F{"test": "value"}, "message")
			}
		}
	}()

	// 并发修改writer
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 5; i++ {
		L.SetWriter(&bytes.Buffer{})
		time.Sleep(5 * time.Millisecond)
	}

	close(stop)
	fmt.Println("✅ SetWriter竞态测试完成 (用go test -race检测)")
}

// ========== 验证Map复制的必要性 ==========
func TestWhyMapCopyNeeded(t *testing.T) {
	L := New()
	buf := &bytes.Buffer{}
	L.SetWriter(buf)

	// 第一次写入
	L.Info(F{"code": 1}, "first")

	// 第二次写入
	L.Info(F{"code": 2}, "second")

	fmt.Println("Output:")
	fmt.Println(buf.String())
	fmt.Println("✅ 如果不复制Map，entry复用时会包含上次的time/msg字段")
}

// ========== 验证RotateBuffer的-2逻辑 ==========
func TestRotateBufferSliceLogic(t *testing.T) {
	buf := NewRotateBuffer()
	buf.MaxSize = 50

	rotatedData := [][]byte{}
	buf.OnRotate(func(data []byte, t0, t1 time.Time, num int) {
		rotatedData = append(rotatedData, data)
		fmt.Printf("Rotate #%d: len=%d\n", len(rotatedData), len(data))
		if len(data) >= 2 {
			fmt.Printf("  最后2字节: %q\n", data[len(data)-2:])
		}
	})

	// 写入一些数据
	buf.Write([]byte("AAAAAAAAAA\n")) // 11字节
	buf.Write([]byte("BBBBBBBBBB\n")) // 11字节
	buf.Write([]byte("CCCCCCCCCC\n")) // 11字节
	buf.Write([]byte("DDDDDDDDDD\n")) // 11字节
	buf.Write([]byte("EEEEEEEEEE\n")) // 11字节 - 应该触发rotate

	buf.Close()

	for i, data := range rotatedData {
		fmt.Printf("\nRotated buffer %d: len=%d\n", i, len(data))
		if len(data) >= 2 {
			fmt.Printf("  完整内容: %q\n", data)
			fmt.Printf("  去掉最后2字节会得到: %q\n", data[:len(data)-2])
		} else {
			fmt.Printf("  ⚠️  长度小于2会panic: %q\n", data)
		}
	}
}
