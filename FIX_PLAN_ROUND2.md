# ActionLog 第二轮修复计划

## 📋 修复范围
- 3个正确性问题 (P0)
- 5个性能问题 (P1)
- 12个其他优化 (P2)

---

## 🎯 详细修复计划

### 阶段1: 正确性问题修复 (P0)

#### 问题 #11: Buffer.Drain竞态和重复发送
**文件**: `hooks/WebHook/Buffer.go:93-100`
**问题**:
1. Count()和Lock之间有竞态窗口
2. Drain后没有清空buffer，可能重复发送

**修复方案**:
```go
func (b *buffer) Drain() {
    b.mutex.Lock()
    defer b.mutex.Unlock()

    if len(b.buffer) == 0 {
        return
    }

    // 调用handler并清空buffer
    b.opt.fn(b.buffer)
    b.buffer = b.buffer[:0]  // 清空但保留容量
}
```

**测试**: 验证Drain不会重复发送

---

#### 问题 #13: OnRotate/OnWrite数据竞态
**文件**: `RotateBuffer.go:93-98`
**问题**: Write读取onRotate/onWrite时没有锁保护

**修复方案**:
```go
type RotateBuffer struct {
    mu       sync.Mutex
    buf      *bytes.Buffer
    MaxSize  int

    callbackMu sync.RWMutex  // 新增: 保护回调
    onRotate   func(buffer []byte, t0, t1 time.Time, num int)
    onWrite    func([]byte)

    t0       time.Time
    c        int32
    wg       *sync.WaitGroup
}

func (b *RotateBuffer) OnRotate(fn func(...)) {
    b.callbackMu.Lock()
    defer b.callbackMu.Unlock()
    b.onRotate = fn
}

func (b *RotateBuffer) OnWrite(fn func([]byte)) {
    b.callbackMu.Lock()
    defer b.callbackMu.Unlock()
    b.onWrite = fn
}

// Write中读取时使用RLock
func (b *RotateBuffer) Write(p []byte) (n int, err error) {
    // ...
    b.callbackMu.RLock()
    onWrite := b.onWrite
    b.callbackMu.RUnlock()

    if onWrite != nil {
        onWrite(p)
    }
    // ...
}
```

**测试**: `go test -race` 无警告

---

#### 问题 #9: Shutdown不等待完成
**文件**: `ActionLog.go:155-169`
**问题**: 只等待固定100ms，不管hooks是否完成

**修复方案**:
```go
type ActionLog struct {
    // ... 现有字段
    activeOps sync.WaitGroup  // 跟踪活动操作
}

func (a *ActionLog) Info(fields F, args ...interface{}) {
    a.mutex.RLock()
    if a.closed {
        a.mutex.RUnlock()
        return
    }
    a.activeOps.Add(1)
    a.mutex.RUnlock()
    defer a.activeOps.Done()

    // ... 现有逻辑
}

func (a *ActionLog) Shutdown(ctx context.Context) error {
    a.closeOnce.Do(func() {
        a.mutex.Lock()
        a.closed = true
        a.mutex.Unlock()
        close(a.closeCh)
    })

    // 等待所有活动操作完成
    done := make(chan struct{})
    go func() {
        a.activeOps.Wait()
        close(done)
    }()

    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-done:
        return nil
    }
}
```

**测试**: TestShutdown验证等待完成

---

### 阶段2: 性能问题修复 (P1)

#### 问题 #1: Map重新分配
**文件**: `ActionLog.go:108`
**修复**:
```go
func (a *ActionLog) freeEntry(entry *Entry) {
    // 清空而非重新分配
    for k := range entry.Data {
        delete(entry.Data, k)
    }
    entry.Buffer.Reset()
    a.pool.Put(entry)
}
```

**性能目标**: 减少50% map分配

---

#### 问题 #2: standardFields复制
**文件**: `ActionLog.go:71`
**修复**: 在Formatter中合并，而非在Entry中复制
```go
// Formatter.Format中直接添加standardFields
func (d *defaultFormatter) Format(entry *Entry, standardFields F) ([]byte, error) {
    data := make(F, len(entry.Data)+len(standardFields)+2)

    // 先复制standardFields
    for k, v := range standardFields {
        data[k] = v
    }

    // 再复制entry.Data (会覆盖同名字段)
    for k, v := range entry.Data {
        data[k] = v
    }

    // 添加time和msg
    data["time"] = entry.Time.String()
    if entry.Message != "" {
        data["msg"] = entry.Message
    }

    // ...
}
```

但这需要修改Formatter接口，改为更简单的方案:
```go
// 在New时就将standardFields添加到每个Entry的初始Data中
// 这样只复制一次而非每次log都复制
```

实际上，当前实现WithFields已经是最优的，问题在于我们不应该每次都复制standardFields。

更好的方案：Formatter在序列化时直接读取两个map
```go
type Entry struct {
    Data           F
    StandardFields F  // 引用，不复制
    Time           time.Time
    Message        string
}

// Info中不调用WithFields(standardFields)
func (a *ActionLog) Info(fields F, args ...interface{}) {
    entry := a.allocEntry()
    entry.StandardFields = a.standardFields  // 引用
    entry.WithTime(time.Now()).WithFields(fields).Info(args...)
    // ...
}

// Formatter中合并两个map
```

---

#### 问题 #3: Formatter分配Map
**文件**: `Formatter.go:23`
**修复**: 使用sync.Pool复用临时map
```go
var formatterMapPool = sync.Pool{
    New: func() interface{} {
        return make(F, 16)
    },
}

func (d defaultFormatter) Format(entry *Entry) ([]byte, error) {
    data := formatterMapPool.Get().(F)
    defer func() {
        // 清空并归还
        for k := range data {
            delete(data, k)
        }
        formatterMapPool.Put(data)
    }()

    for k, v := range entry.Data {
        data[k] = v
    }
    // ...
}
```

---

#### 问题 #5: Buffer重新分配
**文件**: `RotateBuffer.go:74`
**修复**:
```go
func (b *RotateBuffer) rotateUnlocked() {
    // ...
    b.buf.Reset()  // 而非 b.buf = &bytes.Buffer{}
    atomic.StoreInt32(&b.c, 0)
}
```

---

#### 问题 #12: 回调在锁内
**文件**: `hooks/WebHook/Buffer.go:73, 86`
**修复**: 在goroutine中调用handler
```go
func (b *buffer) flush() {
    b.mutex.Lock()

    if len(b.buffer) == 0 {
        b.mutex.Unlock()
        return
    }

    oldBuffer := b.buffer
    b.buffer = b.buffers[0]
    b.buffers = b.buffers[1:]
    if len(b.buffers) == 0 {
        b.buffers = make([][]T, b.opt.maxBuffers)
    }

    b.mutex.Unlock()  // 释放锁后调用

    // 在goroutine中调用，避免阻塞
    go b.opt.fn(oldBuffer)
}
```

---

### 阶段3: 其他优化 (P2)

#### 问题 #4: JSON配置重复创建
**修复**:
```go
var jsonConfig = jsoniter.ConfigCompatibleWithStandardLibrary

func (d defaultFormatter) Format(entry *Entry) ([]byte, error) {
    // ...
    bin, err := jsonConfig.Marshal(&data)
}
```

#### 问题 #6: Current()双重复制
**修复**:
```go
func (b *RotateBuffer) Current() []byte {
    b.mu.Lock()
    defer b.mu.Unlock()
    data := make([]byte, b.buf.Len())
    copy(data, b.buf.Bytes())
    return data
}
```

#### 问题 #7: WebHook重复JSON配置
**修复**: 同问题#4

#### 问题 #8: bytes.NewBufferString
**修复**:
```go
http.Post(h.url, "application/json", strings.NewReader(body))
```

#### 问题 #10: closed检查竞态
**修复**: 已由问题#9的activeOps解决

#### 问题 #14: Formatter()无锁
**修复**: 添加RLock
```go
func (a *ActionLog) Formatter() Formatter {
    a.mutex.RLock()
    defer a.mutex.RUnlock()
    return a.formatter
}
```

#### 问题 #15: AddHook无锁
**修复**:
```go
type webHook struct {
    wg  sync.WaitGroup
    mu  sync.RWMutex  // 新增
    url string
    fn  func(*ActionLog.Entry) bool
    buf *buffer
}

func (h *webHook) AddHook(url string, fn func(...) bool) {
    h.mu.Lock()
    defer h.mu.Unlock()
    h.url = url
    h.fn = fn
}

func (h *webHook) Fire(entry *ActionLog.Entry) error {
    h.mu.RLock()
    fn := h.fn
    url := h.url
    h.mu.RUnlock()

    if fn == nil {
        return nil
    }
    // ...
}
```

#### 问题 #16: errorHandler并发调用
**修复**: 添加文档说明
```go
// ErrorHandler is called when an error occurs during logging.
// The handler must be safe for concurrent use.
type ErrorHandler func(error)
```

#### 问题 #17: Entry.Buffer未使用
**修复**: 删除字段
```go
type Entry struct {
    Data    F
    Time    time.Time
    Message string
    // Buffer字段已删除
}

// pool.New中不再分配Buffer
```

#### 问题 #18: ioutil.ReadAll
**修复**:
```go
import "io"
// ...
data, _ := io.ReadAll(resp.Body)
```

#### 问题 #19: 文档注释
**修复**: 为所有导出类型添加GoDoc

#### 问题 #20: 重复序列化
**修复**: 提取公共方法或复用Formatter

---

## 🧪 测试计划

### 单元测试
1. TestBufferDrainNoRace - Drain竞态
2. TestOnRotateRace - OnRotate竞态
3. TestShutdownWaits - Shutdown等待
4. TestMapReuse - Map复用
5. TestFormatterPooling - Formatter池化

### 基准测试
1. BenchmarkMapReuse - 对比重新分配
2. BenchmarkStandardFields - 对比复制
3. BenchmarkFormatter - 对比Map池化
4. BenchmarkOverall - 整体性能

### Race检测
```bash
go test -race ./...
```

---

## 📊 性能目标

| 优化项 | 当前 | 目标 | 预期提升 |
|--------|------|------|----------|
| Map分配 | 24 allocs/op | 12 allocs/op | -50% |
| standardFields | 42 allocs/op | 26 allocs/op | -38% |
| Formatter | 2790 ns/op | 1800 ns/op | -35% |
| 整体吞吐 | 基线 | +25% | +25% |

---

## ✅ 验收标准

1. 所有测试通过
2. `go test -race` 无警告
3. 性能提升达标 (>20%)
4. 向后兼容
5. 文档完整

---

## 📝 执行顺序

1. 创建分支备份
2. 修复P0问题 (#11, #13, #9)
3. 每个修复后测试
4. 修复P1问题 (#1, #2, #3, #5, #12)
5. 每个修复后基准测试
6. 修复P2问题
7. 全面测试
8. 性能对比
9. 提交代码
