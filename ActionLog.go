// Package ActionLog provides a flexible and concurrent-safe logging framework
// with support for structured logging, hooks, and context propagation.
package ActionLog

import (
    "context"
    "io"
    "os"
    "sync"
    "time"
)

type (
    // ActionLog is the main logger instance. It is safe for concurrent use.
    // Use New() to create an instance with optional standard fields that will
    // be included in all log entries.
    //
    // Example:
    //     logger := ActionLog.New(ActionLog.F{"service": "api", "version": "1.0"})
    //     logger.Info(ActionLog.F{"user": "john"}, "User logged in")
    //
    // The logger supports graceful shutdown via Shutdown() which waits for
    // all in-flight log operations to complete.
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
        activeOps      sync.WaitGroup  // Fix #9: Track active operations
    }
    // F represents structured log fields as key-value pairs.
    // It is a convenience type for map[string]interface{}.
    //
    // Example:
    //     fields := ActionLog.F{"user_id": 123, "action": "login", "success": true}
    F            map[string]interface{}

    // ErrorHandler is called when an error occurs during logging operations
    // (e.g., write failures, hook failures, or formatting errors).
    //
    // IMPORTANT: The handler MUST be thread-safe as it may be called concurrently
    // from multiple goroutines. The handler should not block for extended periods
    // as it may impact logging performance.
    //
    // Example:
    //     logger.SetErrorHandler(func(err error) {
    //         fmt.Fprintf(os.Stderr, "Logging error: %v\n", err)
    //     })
    ErrorHandler func(error)
)

// New creates a new ActionLog instance with optional standard fields.
// Standard fields will be included in every log entry.
//
// Parameters:
//   - fields: Optional standard fields to include in all log entries
//
// Returns a configured logger that writes to os.Stdout by default.
// Use SetWriter() to change the output destination.
//
// Example:
//     logger := ActionLog.New(ActionLog.F{"service": "api", "env": "prod"})
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
            // Fix #17: Remove unused Buffer field
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

// Info logs a message with structured fields at INFO level.
// This is the primary logging method for general informational messages.
//
// Parameters:
//   - fields: Structured key-value pairs to include in this log entry
//   - args: Message components that will be concatenated (similar to fmt.Sprint)
//
// The log entry will include standard fields (set via New), the provided fields,
// a timestamp, and the message. All registered hooks will be invoked.
//
// Example:
//     logger.Info(ActionLog.F{"user_id": 123}, "User login successful")
//     logger.Info(ActionLog.F{"count": 42}, "Processed ", 42, " items")
//
// Note: This method is thread-safe and non-blocking. For context-aware logging
// with distributed tracing support, use InfoContext() instead.
func (a *ActionLog) Info(fields F, args ...interface{}) {
    a.mutex.RLock()
    if a.closed {
        a.mutex.RUnlock()
        return
    }
    a.activeOps.Add(1)  // Fix #9: Track operation
    // Fix #10: Allocate entry while holding lock to prevent race
    entry := a.allocEntryLocked()
    a.mutex.RUnlock()
    defer a.activeOps.Done()

    // standardFields already copied in allocEntryLocked()
    entry.WithTime(time.Now()).WithFields(fields).Info(args...)
    a.fire(entry)
    a.write(entry)
    a.freeEntry(entry)
}

// InfoContext logs a message with context support for distributed tracing.
// This method is preferred over Info() when working with context-based systems.
//
// Parameters:
//   - ctx: Context for cancellation and trace propagation
//   - fields: Structured key-value pairs to include in this log entry
//   - args: Message components that will be concatenated
//
// The method will:
//   1. Return immediately if the context is cancelled
//   2. Extract trace_id from context if present and include it in the log
//   3. Respect context deadlines and cancellation
//
// Example:
//     ctx := context.WithValue(r.Context(), "trace_id", "abc-123")
//     logger.InfoContext(ctx, ActionLog.F{"endpoint": "/api/users"}, "Request processed")
//
// Use Cases:
//   - HTTP request handlers: propagate request context for tracing
//   - Background jobs: respect cancellation signals
//   - Microservices: correlate logs across service boundaries
func (a *ActionLog) InfoContext(ctx context.Context, fields F, args ...interface{}) {
    if ctx.Err() != nil {
        return
    }

    a.mutex.RLock()
    if a.closed {
        a.mutex.RUnlock()
        return
    }
    a.activeOps.Add(1)  // Fix #9: Track operation
    // Fix #10: Allocate entry while holding lock to prevent race
    entry := a.allocEntryLocked()
    a.mutex.RUnlock()
    defer a.activeOps.Done()

    // standardFields already copied in allocEntryLocked()
    entry.WithTime(time.Now()).WithFields(fields).Info(args...)

    // Add trace ID if present in context
    if trace := ctx.Value("trace_id"); trace != nil {
        entry.WithField("trace_id", trace)
    }

    a.fire(entry)
    a.write(entry)
    a.freeEntry(entry)
}

func (a *ActionLog) allocEntry() *Entry {
    a.mutex.RLock()
    defer a.mutex.RUnlock()
    return a.allocEntryLocked()
}

// allocEntryLocked allocates entry with caller holding read lock
// Fix #10: Prevents race between closed check and entry allocation
func (a *ActionLog) allocEntryLocked() *Entry {
    entry := a.pool.Get().(*Entry)
    // Fix #2: Pre-copy standardFields to avoid WithFields overhead
    for k, v := range a.standardFields {
        entry.Data[k] = v
    }
    return entry
}

func (a *ActionLog) freeEntry(entry *Entry) {
    // Fix #1: Clear map instead of reallocating
    for k := range entry.Data {
        delete(entry.Data, k)
    }
    a.pool.Put(entry)
}

func (a *ActionLog) fire(entry *Entry) {
    a.mutex.RLock()
    defer a.mutex.RUnlock()
    if len(a.hooks) == 0 {
        return
    }
    // Continue executing hooks even if one fails
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
    // CRITICAL FIX: Use RLock instead of Lock - we only read formatter/writer
    a.mutex.RLock()
    formatter := a.formatter
    writer := a.writer
    errorHandler := a.errorHandler
    a.mutex.RUnlock()

    // Report errors instead of silently ignoring them
    data, err := formatter.Format(entry)
    if err != nil {
        if errorHandler != nil {
            errorHandler(err)
        }
        return
    }
    _, err = writer.Write(data)
    if err != nil {
        if errorHandler != nil {
            errorHandler(err)
        }
    }
}

// Shutdown gracefully shuts down the logger
// Fix #9: Wait for all active operations to complete
func (a *ActionLog) Shutdown(ctx context.Context) error {
    a.closeOnce.Do(func() {
        a.mutex.Lock()
        a.closed = true
        a.mutex.Unlock()
        close(a.closeCh)
    })

    // Wait for all active operations to complete
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

// Fix #14: Add lock protection
func (a *ActionLog) Formatter() Formatter {
    a.mutex.RLock()
    defer a.mutex.RUnlock()
    return a.formatter
}

// Fix #15: Add SetFormatter method
func (a *ActionLog) SetFormatter(f Formatter) {
    if f == nil {
        return
    }
    a.mutex.Lock()
    defer a.mutex.Unlock()
    a.formatter = f
}
