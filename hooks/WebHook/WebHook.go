package WebHook

import (
    "github.com/DGHeroin/ActionLog"
    jsoniter "github.com/json-iterator/go"
    "io"
    "log"
    "net/http"
    "strings"
    "sync"
    "time"
)

var (
    RetryPost  = 3
    // Fix #7: Package-level JSON config to avoid repeated creation
    jsonConfig = jsoniter.ConfigCompatibleWithStandardLibrary
)

type (
    webHook struct {
        mu  sync.RWMutex  // Fix #15: Protect url and fn fields
        wg  sync.WaitGroup
        url string
        fn  func(*ActionLog.Entry) bool
        buf *buffer
    }
)

func NewWebHook(interval time.Duration) *webHook {
    h := &webHook{}
    h.buf = NewBuffer(WithFireInterval(interval), WithHandler(func(ts []T) {
        // Fix #7: Use package-level jsonConfig
        bin, err := jsonConfig.Marshal(ts)
        if err != nil {
            return
        }
        h.doPost(string(bin))
    }))
    return h
}
func (h *webHook) Fire(entry *ActionLog.Entry) error {
    // Fix #15: Read lock to protect fn access
    h.mu.RLock()
    fn := h.fn
    h.mu.RUnlock()

    // Fix Bug #3: Check if filter function is nil
    if fn == nil {
        return nil
    }
    // 过滤失败
    if !fn(entry) {
        return nil
    }
    data := make(ActionLog.F, len(entry.Data)+2)
    for k, v := range entry.Data {
        data[k] = v
    }
    data["time"] = entry.Time.String()
    if entry.Message != "" {
        data["msg"] = entry.Message
    }

    h.buf.Add(T(data))
    return nil
}
func (h *webHook) AddHook(url string, fn func(*ActionLog.Entry) bool) {
    // Fix #15: Write lock to protect url and fn
    h.mu.Lock()
    defer h.mu.Unlock()
    h.url = url
    h.fn = fn
}
func (h *webHook) Drain() {
    h.buf.Drain()
    h.wg.Wait()
}

// Stop stops the webhook and cleans up resources
func (h *webHook) Stop() {
    h.buf.Stop()
    h.wg.Wait()
}
func (h *webHook) doPost(body string) {
    h.wg.Add(1)
    defer func() {
        // Fix Bug #8: Log panic instead of silently recovering
        if r := recover(); r != nil {
            log.Printf("WebHook panic recovered: %v", r)
        }
        h.wg.Done()
    }()

    // Fix #15: Read lock to get URL
    h.mu.RLock()
    url := h.url
    h.mu.RUnlock()

    // 发送
    retry := 0
    for retry < RetryPost {
        // Fix #8: Use strings.NewReader instead of bytes.NewBufferString
        if resp, err := http.Post(url, "application/json", strings.NewReader(body)); err == nil {
            // Fix Bug #4: Close response body to prevent connection leak
            defer resp.Body.Close()
            // Fix #18: Use io.ReadAll instead of deprecated ioutil.ReadAll
            data, _ := io.ReadAll(resp.Body)
            log.Println(len(body), string(data))
            break
        }
        time.Sleep(time.Second)
        retry++
    }
}
