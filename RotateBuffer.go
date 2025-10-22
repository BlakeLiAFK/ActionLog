package ActionLog

import (
    "bytes"
    "sync"
    "sync/atomic"
    "time"
)

type (
    RotateBuffer struct {
        mu       sync.Mutex
        buf      *bytes.Buffer
        MaxSize  int
        onRotate func(buffer []byte, t0, t1 time.Time, num int)
        onWrite  func([]byte)
        t0       time.Time
        c        int32
        wg       *sync.WaitGroup
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
    if b.onWrite != nil {
        b.onWrite(p)
    }
    return b.buf.Write(p)
}

func (b *RotateBuffer) rotate() {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.rotateUnlocked()
}

func (b *RotateBuffer) rotateUnlocked() {
    if b.onRotate != nil && b.buf.Len() > 0 {
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
            b.onRotate(rBuf.Bytes(), t0, t1, num)
        }()
    }
    b.buf = &bytes.Buffer{}
    atomic.StoreInt32(&b.c, 0)
}

func (b *RotateBuffer) Flush() {
    b.rotate()
}
func (b *RotateBuffer) Close() {
    b.rotate()
    b.wg.Wait()
}
func (b *RotateBuffer) Current() []byte {
    b.mu.Lock()
    defer b.mu.Unlock()
    buf := &bytes.Buffer{}
    buf.Write(b.buf.Bytes())
    return buf.Bytes()
}

func (b *RotateBuffer) OnRotate(fn func(buffer []byte, t0, t1 time.Time, num int)) {
    b.onRotate = fn
}
func (b *RotateBuffer) OnWrite(fn func([]byte)) {
    b.onWrite = fn
}
