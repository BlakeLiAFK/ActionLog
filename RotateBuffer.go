package ActionLog

import (
    "bytes"
    "sync"
    "sync/atomic"
    "time"
)

type (
    RotateBuffer struct {
        mu         sync.Mutex
        buf        *bytes.Buffer
        MaxSize    int
        callbackMu sync.RWMutex  // Fix #13: Protect callbacks
        onRotate   func(buffer []byte, t0, t1 time.Time, num int)
        onWrite    func([]byte)
        t0         time.Time
        c          int32
        wg         *sync.WaitGroup
    }
)

func NewRotateBuffer() *RotateBuffer {
    return &RotateBuffer{
        buf:     &bytes.Buffer{},
        MaxSize: 100 * 1000 * 1000, // 100M
        wg:      &sync.WaitGroup{},
    }
}

func (b *RotateBuffer) Write(p []byte) (n int, err error) {
    b.mu.Lock()
    defer b.mu.Unlock()

    // Check if rotation is needed while holding the lock
    if b.buf.Len()+len(p) > b.MaxSize {
        b.rotateUnlocked()
    }

    num := atomic.AddInt32(&b.c, 1)
    if num == 1 {
        b.t0 = time.Now()
    }

    // Fix #13: Read callback with RLock
    b.callbackMu.RLock()
    onWrite := b.onWrite
    b.callbackMu.RUnlock()

    if onWrite != nil {
        onWrite(p)
    }
    return b.buf.Write(p)
}

func (b *RotateBuffer) rotate() {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.rotateUnlocked()
}

func (b *RotateBuffer) rotateUnlocked() {
    if b.buf.Len() > 0 {
        // Fix #13: Read callback with RLock
        b.callbackMu.RLock()
        onRotate := b.onRotate
        b.callbackMu.RUnlock()

        if onRotate != nil {
            rBuf := &bytes.Buffer{}
            data := b.buf.Bytes()
            // Fix: Check length before slicing to avoid panic
            if len(data) >= 2 {
                data = data[:len(data)-2]
            }
            rBuf.Write(data)
            b.wg.Add(1)
            num := int(b.c)
            t0 := b.t0
            t1 := time.Now()
            go func() {
                defer b.wg.Done()
                onRotate(rBuf.Bytes(), t0, t1, num)
            }()
        }
    }
    // Fix #5: Use Reset instead of reallocating
    b.buf.Reset()
    atomic.StoreInt32(&b.c, 0)
}

func (b *RotateBuffer) Flush() {
    b.rotate()
}
func (b *RotateBuffer) Close() {
    b.rotate()
    b.wg.Wait()
}

// Fix #6: Optimize Current() to avoid double copy
func (b *RotateBuffer) Current() []byte {
    b.mu.Lock()
    defer b.mu.Unlock()
    data := make([]byte, b.buf.Len())
    copy(data, b.buf.Bytes())
    return data
}

// Fix #13: Add lock protection
func (b *RotateBuffer) OnRotate(fn func(buffer []byte, t0, t1 time.Time, num int)) {
    b.callbackMu.Lock()
    defer b.callbackMu.Unlock()
    b.onRotate = fn
}

// Fix #13: Add lock protection
func (b *RotateBuffer) OnWrite(fn func([]byte)) {
    b.callbackMu.Lock()
    defer b.callbackMu.Unlock()
    b.onWrite = fn
}
