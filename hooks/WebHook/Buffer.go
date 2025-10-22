package WebHook

import (
    "github.com/DGHeroin/ActionLog"
    "sync"
    "time"
)

type (
    T      ActionLog.F
    buffer struct {
        mutex   sync.RWMutex
        buffer  []T
        buffers [][]T
        opt     *option
        stopCh  chan struct{}
        once    sync.Once
    }
    option struct {
        maxBuffers    uint
        fireThreshold int
        fireInterval  time.Duration
        fn            func([]T)
    }
    Option func(*option)
)

func NewBuffer(opts ...Option) *buffer {
    opt := &option{
        maxBuffers:    50,
        fireThreshold: 10,
        fireInterval:  time.Second * 5,
        fn:            func([]T) {},
    }
    for _, fn := range opts {
        fn(opt)
    }
    buf := &buffer{
        opt:    opt,
        stopCh: make(chan struct{}),
    }
    buf.buffer = make([]T, 0, opt.fireThreshold)
    buf.buffers = make([][]T, opt.maxBuffers)
    // Fix Bug #5: Add stop channel to prevent goroutine leak
    go func() {
        ticker := time.NewTicker(opt.fireInterval)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                if buf.Count() == 0 {
                    continue
                }
                buf.flush()
            case <-buf.stopCh:
                return
            }
        }
    }()
    return buf
}
func (b *buffer) Add(s T) {
    b.mutex.Lock()
    b.buffer = append(b.buffer, s)
    var oldBuffer []T
    if len(b.buffer) >= b.opt.fireThreshold {
        oldBuffer = b.buffer
        b.buffer = b.buffers[0]
        b.buffers = b.buffers[1:]
        if len(b.buffers) == 0 {
            b.buffers = make([][]T, b.opt.maxBuffers)
        }
    }
    b.mutex.Unlock()

    // Fix #12: Call handler outside lock to prevent blocking
    if oldBuffer != nil {
        b.opt.fn(oldBuffer)
    }
}
func (b *buffer) flush() {
    b.mutex.Lock()
    oldBuffer := b.buffer
    b.buffer = b.buffers[0]
    b.buffers = b.buffers[1:]
    if len(b.buffers) == 0 {
        b.buffers = make([][]T, b.opt.maxBuffers)
    }
    b.mutex.Unlock()

    // Fix #12: Call handler outside lock to prevent blocking
    b.opt.fn(oldBuffer)
}
func (b *buffer) Count() int {
    b.mutex.RLock()
    defer b.mutex.RUnlock()
    return len(b.buffer)
}
// Drain flushes any remaining buffered items and clears the buffer
func (b *buffer) Drain() {
    b.mutex.Lock()
    defer b.mutex.Unlock()

    if len(b.buffer) == 0 {
        return
    }

    // Call handler and clear buffer to prevent duplicate sends
    b.opt.fn(b.buffer)
    b.buffer = b.buffer[:0]  // Clear but retain capacity
}

// Stop stops the background goroutine
func (b *buffer) Stop() {
    b.once.Do(func() {
        close(b.stopCh)
    })
}

func WithHandler(fn func([]T)) Option {
    return func(o *option) {
        o.fn = fn
    }
}
func WithBufferSize(n uint) Option {
    return func(o *option) {
        o.maxBuffers = n
    }
}
func WithFireThreshold(n int) Option {
    return func(o *option) {
        o.fireThreshold = n
    }
}
func WithFireInterval(duration time.Duration) Option {
    return func(o *option) {
        o.fireInterval = duration
    }
}
