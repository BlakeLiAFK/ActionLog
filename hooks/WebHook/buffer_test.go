package WebHook

import (
    "testing"
    "time"
)

func TestBuffer(t *testing.T) {
    buf := NewBuffer(WithHandler(func(ts []T) {
        t.Log(ts)
    }))
    for i := 0; i < 5; i++ {
        buf.Add(T{"index": i})
    }
    time.Sleep(time.Second * 2)
    for i := 10; i < 16; i++ {
        buf.Add(T{"index": i})
    }
    buf.Drain()
}
