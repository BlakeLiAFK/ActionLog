package ActionLog

import (
    "bytes"
    jsoniter "github.com/json-iterator/go"
    "sync"
)

// Fix #4: Use package-level json config
var jsonConfig = jsoniter.ConfigCompatibleWithStandardLibrary

// Fix #3: Pool for temporary maps
var formatterMapPool = sync.Pool{
    New: func() interface{} {
        return make(F, 16)
    },
}

type (
    Formatter interface {
        SetPrefix(string)
        Format(entry *Entry) ([]byte, error)
    }
    defaultFormatter struct {
        prefix []byte
    }
)

func (d *defaultFormatter) SetPrefix(s string) {
    d.prefix = []byte(s)
}

func (d defaultFormatter) Format(entry *Entry) ([]byte, error) {
    // Fix #3: Use pooled map
    data := formatterMapPool.Get().(F)
    defer func() {
        // Clear and return to pool
        for k := range data {
            delete(data, k)
        }
        formatterMapPool.Put(data)
    }()

    // Copy entry data
    for k, v := range entry.Data {
        data[k] = v
    }
    data["time"] = entry.Time.String()
    if entry.Message != "" {
        data["msg"] = entry.Message
    }

    // Fix #4: Use package-level jsonConfig
    bin, err := jsonConfig.Marshal(&data)
    if err != nil {
        return nil, err
    }
    if d.prefix == nil {
        return append(bin, '\n'), nil
    } else {
        buf := bytes.NewBuffer(d.prefix)
        buf.Write(bin)
        buf.WriteString("\n")
        return buf.Bytes(), nil
    }
}
