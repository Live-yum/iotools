package engine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"os"
	"sync"
	"time"
)

var modbusLogMutex sync.Mutex

type ModbusWriteLogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	OperationID string    `json:"operation_id"`
	Phase       string    `json:"phase"`
	Endpoint    string    `json:"endpoint"`
	Unit        int       `json:"unit"`
	Address     int       `json:"address"`
	Count       int       `json:"count"`
	Type        string    `json:"type"`
	Previous    any       `json:"previous"`
	Value       any       `json:"value"`
	Function    string    `json:"function"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
}

func appendModbusLog(f *os.File, entry ModbusWriteLogEntry) error {
	b, e := json.Marshal(entry)
	if e != nil {
		return e
	}
	if len(b) > 64<<10 {
		return fmt.Errorf("写日志条目超过64KiB")
	}
	b = append(b, '\n')
	modbusLogMutex.Lock()
	defer modbusLogMutex.Unlock()
	if _, e = f.Write(b); e != nil {
		return e
	}
	return f.Sync()
}
func runModbusLogged(ctx context.Context, r config.Request, emit Emit) error {
	path := r.String("write_log_file", "")
	if len(path) > 4096 {
		return fmt.Errorf("写日志路径过长")
	}
	if !r.Mutates() || path == "" {
		return runModbus(ctx, r, emit)
	}
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("写日志必须是普通文件，拒绝符号链接")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if e != nil {
		return fmt.Errorf("未能打开写日志，设备操作未执行：%w", e)
	}
	defer f.Close()
	opened, statErr := f.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return fmt.Errorf("日志文件身份发生变化，设备操作未执行")
	}
	id := make([]byte, 16)
	if _, e = rand.Read(id); e != nil {
		return e
	}
	value := r.Params["value"]
	if values, ok := r.Params["values"]; ok {
		value = values
	}
	if raw, ok := r.Params["pdu_hex"]; ok {
		value = raw
	}
	space := "holding"
	if r.Action == "write-coil" || r.Action == "write-coils" {
		space = "coil"
	}
	count := 1
	if values, ok := r.Params["values"].([]any); ok {
		count = len(values)
	}
	if r.Action == "write-typed" {
		count = reprWidth(r.String("value_type", ""))
	}
	if r.Action == "write-raw" {
		count = r.Int("count", 1)
	}
	entry := ModbusWriteLogEntry{Timestamp: time.Now().UTC(), OperationID: hex.EncodeToString(id), Phase: "attempt", Endpoint: r.Endpoint, Unit: r.Int("unit", 0), Address: r.Int("address", 0), Count: count, Type: space, Previous: r.Params["write_log_previous"], Value: value, Function: r.Action, Status: "pending"}
	if e = appendModbusLog(f, entry); e != nil {
		return fmt.Errorf("未能记录写入尝试，设备操作未执行：%w", e)
	}
	operationErr := runModbus(ctx, r, emit)
	entry.Timestamp = time.Now().UTC()
	entry.Phase = "result"
	entry.Status = "success"
	if operationErr != nil {
		entry.Status = "failed_or_unknown"
		entry.Error = operationErr.Error()
	}
	if e = appendModbusLog(f, entry); e != nil {
		return fmt.Errorf("设备操作已尝试，结果日志失败（不要自动重试写入）：%w", e)
	}
	return operationErr
}

// ReadModbusWriteLog reads a bounded tail; it never changes or rotates the log.
func ReadModbusWriteLog(path string) ([]ModbusWriteLogEntry, error) {
	modbusLogMutex.Lock()
	defer modbusLogMutex.Unlock()
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("写日志必须为普通文件")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil {
		return nil, e
	}
	current, e := os.Lstat(path)
	if e != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("读取时日志文件已被替换")
	}
	info = opened
	start := int64(0)
	if info.Size() > 2<<20 {
		start = info.Size() - (2 << 20)
	}
	if _, e = f.Seek(start, io.SeekStart); e != nil {
		return nil, e
	}
	scanner := bufio.NewScanner(io.LimitReader(f, info.Size()-start))
	scanner.Buffer(make([]byte, 4096), 65537)
	if start > 0 {
		scanner.Scan()
	}
	out := []ModbusWriteLogEntry{}
	for scanner.Scan() {
		var entry ModbusWriteLogEntry
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		if e = decoder.Decode(&entry); e != nil {
			return nil, fmt.Errorf("写日志含无效JSON条目：%w", e)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return nil, fmt.Errorf("写日志每行只能有一个JSON对象")
		}
		out = append(out, entry)
		if len(out) > 1000 {
			out = out[1:]
		}
	}
	return out, scanner.Err()
}
