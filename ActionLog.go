package ActionLog

import (
    "bytes"
    "context"
    "io"
    "os"
    "sync"
    "time"
)

type (
    ActionLog struct {
        mutex          sync.RWMutex
        pool           sync.Pool
        standardFields F
        hooks          []Hook
        writer         io.Writer
        formatter      Formatter
        errorHandler   ErrorHandler
        closed         bool
        closeCh        chan struct{}
        closeOnce      sync.Once
    }
    F            map[string]interface{}
    ErrorHandler func(error)
)

func New(fields ...F) *ActionLog {
    L := &ActionLog{
        writer:         os.Stdout,
        formatter:      &defaultFormatter{},
        standardFields: map[string]interface{}{},
        closeCh:        make(chan struct{}),
    }
    for _, field := range fields {
        for k, v := range field {
            L.standardFields[k] = v
        }
    }
    L.pool.New = func() interface{} {
        return &Entry{
            Data:   make(F),
            Buffer: &bytes.Buffer{},
        }
    }
    return L
}
func (a *ActionLog) SetWriter(w io.Writer) {
    a.mutex.Lock()
    defer a.mutex.Unlock()
    a.writer = w
}

// SetErrorHandler sets a handler for errors that occur during logging
func (a *ActionLog) SetErrorHandler(handler ErrorHandler) {
    a.mutex.Lock()
    defer a.mutex.Unlock()
    a.errorHandler = handler
}

func (a *ActionLog) Info(fields F, args ...interface{}) {
    a.mutex.RLock()
    if a.closed {
        a.mutex.RUnlock()
        return
    }
    a.mutex.RUnlock()

    entry := a.allocEntry()
    entry.WithTime(time.Now()).WithFields(a.standardFields).WithFields(fields).Info(args...)
    a.fire(entry)
    a.write(entry)
    a.freeEntry(entry)
}

// InfoContext logs with context support (Defect #10)
func (a *ActionLog) InfoContext(ctx context.Context, fields F, args ...interface{}) {
    if ctx.Err() != nil {
        return
    }

    a.mutex.RLock()
    if a.closed {
        a.mutex.RUnlock()
        return
    }
    a.mutex.RUnlock()

    entry := a.allocEntry()
    entry.WithTime(time.Now()).WithFields(a.standardFields).WithFields(fields).Info(args...)

    // Add trace ID if present in context
    if trace := ctx.Value("trace_id"); trace != nil {
        entry.WithField("trace_id", trace)
    }

    a.fire(entry)
    a.write(entry)
    a.freeEntry(entry)
}
func (a *ActionLog) allocEntry() *Entry {
    entry := a.pool.Get().(*Entry)
    return entry
}

func (a *ActionLog) freeEntry(entry *Entry) {
    entry.Data = map[string]interface{}{}
    entry.Buffer.Reset()
    a.pool.Put(entry)
}
func (a *ActionLog) fire(entry *Entry) {
    a.mutex.RLock()
    defer a.mutex.RUnlock()
    if len(a.hooks) == 0 {
        return
    }
    // Fix Defect #6: Continue executing hooks even if one fails
    for _, hook := range a.hooks {
        err := hook.Fire(entry)
        if err != nil && a.errorHandler != nil {
            a.errorHandler(err)
        }
    }
}
func (a *ActionLog) AddHook(hook Hook) {
    if hook == nil {
        return
    }
    a.mutex.Lock()
    defer a.mutex.Unlock()
    a.hooks = append(a.hooks, hook)
}

func (a *ActionLog) write(entry *Entry) {
    a.mutex.Lock()
    defer a.mutex.Unlock()
    // Fix Defect #7: Report errors instead of silently ignoring them
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

// Shutdown gracefully shuts down the logger (Defect #11)
func (a *ActionLog) Shutdown(ctx context.Context) error {
    a.closeOnce.Do(func() {
        a.mutex.Lock()
        a.closed = true
        a.mutex.Unlock()
        close(a.closeCh)
    })

    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(100 * time.Millisecond):
        return nil
    }
}

func (a *ActionLog) Formatter() Formatter {
    return a.formatter
}
