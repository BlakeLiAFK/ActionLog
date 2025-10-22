# ActionLog 修复计划

## 📋 修复范围
- 5个严重Bug (会崩溃/泄漏)
- 6个设计缺陷 (影响可靠性)

---

## 🎯 详细修复计划

### 阶段1: 严重Bug修复 (P0)

#### Bug #1: RotateBuffer切片越界
**文件**: `RotateBuffer.go:61`
**问题**: `data[:len(data)-2]` 当len < 2时panic
**修复方案**:
```go
// 修复前
data = data[:len(data)-2]

// 修复后
if len(data) >= 2 {
    data = data[:len(data)-2]
}
```
**测试**: 用MaxSize=1触发边界情况

---

#### Bug #2: SetWriter竞态条件
**文件**: `ActionLog.go:42-44`
**问题**: SetWriter没有加锁，write方法有锁
**修复方案**:
```go
func (a *ActionLog) SetWriter(w io.Writer) {
    a.mutex.Lock()
    defer a.mutex.Unlock()
    a.writer = w
}
```
**测试**: `go test -race` 验证无竞态

---

#### Bug #3: WebHook nil函数调用
**文件**: `hooks/WebHook/WebHook.go:41`
**问题**: fn未初始化就调用
**修复方案**:
```go
func (h *webHook) Fire(entry *ActionLog.Entry) error {
    if h.fn == nil {
        return nil // 或返回error
    }
    if !h.fn(entry) {
        return nil
    }
    // ...
}
```
**测试**: 创建WebHook但不调用AddHook

---

#### Bug #4: HTTP响应体泄漏
**文件**: `hooks/WebHook/WebHook.go:73-77`
**问题**: resp.Body未关闭
**修复方案**:
```go
if resp, err := http.Post(...); err == nil {
    defer resp.Body.Close()
    data, _ := ioutil.ReadAll(resp.Body)
    log.Println(len(body), string(data))
    break
}
```
**测试**: 检查连接是否正确关闭

---

#### Bug #5: Goroutine泄漏
**文件**: `hooks/WebHook/Buffer.go:41-49`
**问题**: 永久运行的goroutine
**修复方案**:
```go
// 添加stop channel
type buffer struct {
    // ... 现有字段
    stopCh chan struct{}
    once   sync.Once
}

func NewBuffer(...) *buffer {
    buf := &buffer{
        stopCh: make(chan struct{}),
        // ...
    }
    go func() {
        ticker := time.NewTicker(opt.fireInterval)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                if buf.Count() > 0 {
                    buf.flush()
                }
            case <-buf.stopCh:
                return
            }
        }
    }()
    return buf
}

func (b *buffer) Stop() {
    b.once.Do(func() {
        close(b.stopCh)
    })
}
```
**测试**: 验证goroutine能正确停止

---

### 阶段2: 设计缺陷修复 (P1)

#### 缺陷 #6: Hook错误处理中断后续hooks
**文件**: `ActionLog.go:70-75`
**修复方案**:
```go
func (a *ActionLog) fire(entry *Entry) {
    a.mutex.RLock()
    defer a.mutex.RUnlock()
    if len(a.hooks) == 0 {
        return
    }
    for _, hook := range a.hooks {
        _ = hook.Fire(entry) // 忽略错误，继续执行
    }
}
```
**测试**: 多个hooks，其中一个失败

---

#### 缺陷 #7: 错误静默忽略
**修复方案**: 添加错误处理器
```go
type ErrorHandler func(error)

type ActionLog struct {
    // ... 现有字段
    errorHandler ErrorHandler
}

func (a *ActionLog) SetErrorHandler(handler ErrorHandler) {
    a.mutex.Lock()
    defer a.mutex.Unlock()
    a.errorHandler = handler
}

func (a *ActionLog) write(entry *Entry) {
    a.mutex.Lock()
    defer a.mutex.Unlock()
    data, err := a.formatter.Format(entry)
    if err != nil {
        if a.errorHandler != nil {
            a.errorHandler(err)
        }
        return
    }
    _, err = a.writer.Write(data)
    if err != nil {
        if a.errorHandler != nil {
            a.errorHandler(err)
        }
    }
}
```
**测试**: 设置errorHandler并验证调用

---

#### 缺陷 #8: Recover滥用
**文件**: `hooks/WebHook/WebHook.go:67`
**修复方案**:
```go
defer func() {
    if r := recover(); r != nil {
        log.Printf("WebHook panic recovered: %v", r)
    }
    h.wg.Done()
}()
```
**测试**: 触发panic并验证日志

---

#### 缺陷 #9: RotateBuffer.Write竞态窗口
**文件**: `RotateBuffer.go:31-52`
**修复方案**:
```go
func (b *RotateBuffer) Write(p []byte) (n int, err error) {
    b.mu.Lock()
    defer b.mu.Unlock()

    // 先检查是否需要rotate
    if b.buf.Len()+len(p) > b.MaxSize {
        b.rotateUnlocked() // 内部不加锁版本
    }

    num := atomic.AddInt32(&b.c, 1)
    if num == 1 {
        b.t0 = time.Now()
    }
    if b.onWrite != nil {
        b.onWrite(p)
    }
    return b.buf.Write(p)
}

func (b *RotateBuffer) rotateUnlocked() {
    // rotate逻辑，不加锁（调用者已加锁）
}
```
**测试**: 并发写入验证

---

#### 缺陷 #10: 添加Context支持
**修复方案**:
```go
func (a *ActionLog) InfoContext(ctx context.Context, fields F, args ...interface{}) {
    if ctx.Err() != nil {
        return // context已取消
    }

    entry := a.allocEntry()
    entry.WithTime(time.Now()).WithFields(a.standardFields).WithFields(fields).Info(args...)

    // 可以添加context的trace信息
    if trace := ctx.Value("trace_id"); trace != nil {
        entry.WithField("trace_id", trace)
    }

    a.fire(entry)
    a.write(entry)
    a.freeEntry(entry)
}
```
**测试**: 使用取消的context

---

#### 缺陷 #11: 添加优雅关闭机制
**修复方案**:
```go
type ActionLog struct {
    // ... 现有字段
    closed  bool
    closeCh chan struct{}
    closeOnce sync.Once
}

func New(fields ...F) *ActionLog {
    L := &ActionLog{
        closeCh: make(chan struct{}),
        // ... 现有初始化
    }
    return L
}

func (a *ActionLog) Shutdown(ctx context.Context) error {
    a.closeOnce.Do(func() {
        a.mutex.Lock()
        a.closed = true
        a.mutex.Unlock()
        close(a.closeCh)
    })

    // 等待hooks完成（如果有的话）
    // 可以添加超时控制
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(100 * time.Millisecond):
        return nil
    }
}

func (a *ActionLog) Info(fields F, args ...interface{}) {
    a.mutex.RLock()
    if a.closed {
        a.mutex.RUnlock()
        return
    }
    a.mutex.RUnlock()

    // ... 现有逻辑
}
```
**测试**: 验证shutdown后拒绝新日志

---

## 🧪 测试计划

### 单元测试
1. `TestRotateBufferEdgeCases` - 边界情况
2. `TestSetWriterRace` - race detector
3. `TestWebHookNilFunction` - nil函数调用
4. `TestHTTPConnectionLeak` - HTTP连接
5. `TestBufferGoroutineStop` - goroutine停止
6. `TestHookErrorHandling` - hook错误处理
7. `TestErrorHandler` - 错误回调
8. `TestRecoverLogging` - recover日志
9. `TestConcurrentWrite` - 并发写入
10. `TestContextSupport` - context支持
11. `TestShutdown` - 优雅关闭

### 集成测试
- 完整的日志流程测试
- 并发场景测试
- 压力测试

### Race Detector
```bash
go test -race ./...
```

---

## ✅ 验收标准

1. 所有现有测试通过
2. 新增测试全部通过
3. `go test -race` 无警告
4. 边界情况不panic
5. 资源正确释放
6. 错误能被捕获和报告

---

## 📝 执行顺序

1. 创建备份分支
2. 逐个修复Bug #1-5
3. 每修复一个，立即测试
4. 修复设计缺陷 #6-11
5. 每修复一个，立即测试
6. 运行完整测试套件
7. race detector验证
8. 提交代码

---

## 🔄 回滚计划

如果某个修复导致问题：
1. git stash当前修改
2. 回到上一个工作状态
3. 重新分析问题
4. 修改修复方案
5. 重新测试
