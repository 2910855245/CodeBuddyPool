package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

// ringLog 内存环形日志缓冲 (供 /admin/logs 查询最近日志)
type ringLog struct {
	mu   sync.Mutex
	buf  []string
	size int
}

var globalLog = newRingLog(500)

func newRingLog(size int) *ringLog {
	return &ringLog{size: size}
}

func (r *ringLog) add(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.size {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, msg)
}

// Recent 返回最近 n 条 (按时间正序)
func (r *ringLog) Recent(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.buf) {
		n = len(r.buf)
	}
	out := make([]string, n)
	copy(out, r.buf[len(r.buf)-n:])
	return out
}

// ringCore 包装 zap core: 日志同时写入环形缓冲
// 注意: 必须显式实现 Check, 把 ringCore 自身注册进 CheckedEntry。
// 否则会沿用内嵌底层 core 的 Check, 底层 core 把自己(而非 ringCore)注册进去,
// 导致 ringCore.Write 被完全绕过 (日志只写 stdout, 不进环形缓冲/文件)。
type ringCore struct {
	zapcore.Core
}

func (c ringCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

func (c ringCore) Write(e zapcore.Entry, fields []zapcore.Field) error {
	line := e.Time.Format(time.RFC3339) + " " + e.Level.String() + " " + e.Message
	globalLog.add(line)
	globalFileLog.write(line)
	return c.Core.Write(e, fields)
}

func (c ringCore) With(fields []zapcore.Field) zapcore.Core {
	return ringCore{c.Core.With(fields)}
}

// fileLogCore 文件日志核心: 大小封顶自动轮转, 只保留 1 个备份, 磁盘占用锁死
const (
	logMaxSizeBytes = 10 << 20 // 10 MB 封顶
	logFileName     = "proxy.log"
)

type fileLogCore struct {
	mu   sync.Mutex
	path string
	size int64
}

var globalFileLog *fileLogCore

// initFileLog 挂载文件日志: 写到 baseDir/logs/proxy.log, 超 10MB 时重命名为 proxy.log.1 (覆盖旧的)
func initFileLog(baseDir string) {
	dir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	p := filepath.Join(dir, logFileName)
	var sz int64
	if fi, err := os.Stat(p); err == nil {
		sz = fi.Size()
	}
	globalFileLog = &fileLogCore{path: p, size: sz}
}

func (f *fileLogCore) write(line string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.size >= logMaxSizeBytes {
		_ = os.Rename(f.path, f.path+".1") // 覆盖旧备份, 只留 1 份
		f.size = 0
	}
	fh, err := os.OpenFile(f.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	n, _ := fh.WriteString(line + "\n")
	_ = fh.Close()
	f.size += int64(n)
}
