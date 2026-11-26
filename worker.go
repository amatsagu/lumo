package lumo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"
	"unsafe"
)

const (
	queueSize       = 4096
	workerCooldown  = 10 * time.Second
	maxPooledMsgCap = 4096
	defaultBufCap   = 512
	maxWorkerBufCap = 8192
)

type workerScratch struct {
	buf         []byte
	jsonScratch bytes.Buffer
}

func (ws *workerScratch) trim() {
	if cap(ws.buf) > maxWorkerBufCap {
		ws.buf = make([]byte, 0, defaultBufCap)
	}
	if ws.jsonScratch.Cap() > maxWorkerBufCap {
		ws.jsonScratch = bytes.Buffer{}
	}
}

var (
	prettyLabels = [...]string{"DEBUG", " INFO", " WARN", "ERROR", "PANIC"}
	jsonLabels   = [...]string{"DEBUG", "INFO", "WARN", "ERROR", "PANIC"}
)

type logTask struct {
	level    level
	callerPC uintptr
	time     time.Time
	msg      string
	msgBuf   *msgBuffer
	errMsg   string
	stack    []uintptr
	stackBuf *stackBuffer
	context  []contextItem
}

func (l *logger) ensureWorkerStarted() {
	l.mu.RLock()
	if l.workerActive {
		l.mu.RUnlock()
		return
	}
	l.mu.RUnlock()

	l.mu.Lock()
	if l.workerActive {
		l.mu.Unlock()
		return
	}

	l.logQueue = make(chan logTask, queueSize)
	l.wg.Add(1)
	l.workerActive = true
	go l.processQueue()

	l.mu.Unlock()
}

func (l *logger) stopWorker() {
	l.mu.Lock()
	if l.workerActive {
		close(l.logQueue)
		l.workerActive = false
		l.mu.Unlock()

		l.wg.Wait()

		l.mu.Lock()
		l.logQueue = nil
	}
	l.mu.Unlock()
}

func (l *logger) enqueue(task logTask) {
	for {
		l.ensureWorkerStarted()

		l.mu.RLock()
		if !l.workerActive || l.logQueue == nil {
			l.mu.RUnlock()
			// Retry in case the worker just exited on idle timeout.
			continue
		}

		l.logQueue <- task
		l.mu.RUnlock()
		return
	}
}

func (l *logger) processQueue() {
	l.outputMu.Lock()
	currentOutput := l.output
	lastSeq := l.outputSeq.Load()
	l.outputMu.Unlock()

	writer := bufio.NewWriter(currentOutput)
	timer := time.NewTimer(workerCooldown)
	scratch := workerScratch{
		buf: make([]byte, 0, defaultBufCap),
	}

	callerCache := make(map[uintptr]callerLoc, 64)
	stackCache := make(map[stackCacheKey]stackCacheVal, 32)

	syncOutput := func() {
		seq := l.outputSeq.Load()
		if seq == lastSeq {
			return
		}
		writer.Flush()
		l.outputMu.Lock()
		currentOutput = l.output
		lastSeq = l.outputSeq.Load()
		l.outputMu.Unlock()
		writer.Reset(currentOutput)
	}

	writeTask := func(task logTask) {
		syncOutput()
		scratch.buf = scratch.buf[:0]

		loc := resolveCallerLoc(task.callerPC, callerCache)

		msg := task.msg
		if task.msgBuf != nil {
			data := task.msgBuf.data
			if len(data) > 0 {
				msg = unsafe.String(unsafe.SliceData(data), len(data))
			}
		}

		stack := task.stack
		if task.stackBuf != nil {
			stack = task.stackBuf.pcs[:task.stackBuf.n]
		}

		var frames [maxStackFrames]parsedFrame
		frameCount := 0
		if len(stack) > 0 {
			frameCount = resolveStackFrames(stack, stackCache, &frames)
		}

		if l.jsonMode.Load() {
			scratch.buf = l.appendJSONLog(scratch.buf, &scratch.jsonScratch, task, loc, msg, frames[:frameCount])
		} else {
			scratch.buf = l.appendPrettyLog(scratch.buf, task, loc, msg, frames[:frameCount])
		}

		if task.msgBuf != nil {
			if cap(task.msgBuf.data) <= maxPooledMsgCap {
				task.msgBuf.data = task.msgBuf.data[:0]
				msgPool.Put(task.msgBuf)
			}
		}
		if task.stackBuf != nil {
			task.stackBuf.n = 0
			stackPool.Put(task.stackBuf)
		}

		writer.Write(scratch.buf)
		scratch.trim()
	}

	for {
		select {
		case task, ok := <-l.logQueue:
			if !ok {
				timer.Stop()
				writer.Flush()
				l.wg.Done()
				return
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(workerCooldown)

			writeTask(task)

			n := len(l.logQueue)
			for range n {
				writeTask(<-l.logQueue)
			}
			writer.Flush()

		case <-timer.C:
			l.mu.Lock()
			if len(l.logQueue) > 0 {
				l.mu.Unlock()
				timer.Reset(workerCooldown)
				continue
			}
			l.workerActive = false
			l.logQueue = nil
			l.mu.Unlock()

			timer.Stop()
			writer.Flush()
			l.wg.Done()
			return
		}
	}
}

func (l *logger) appendPrettyLog(buf []byte, task logTask, loc callerLoc, msg string, frames []parsedFrame) []byte {
	var p colorPalette
	var lvlColor string
	if !l.noColor.Load() {
		p = ansiPalette
		lvlColor = levelANSIColor(task.level)
	}

	buf = append(buf, p.gray...)
	buf = task.time.AppendFormat(buf, l.timeFormat)
	buf = append(buf, p.reset...)
	buf = append(buf, ' ')
	buf = append(buf, lvlColor...)
	buf = append(buf, prettyLabels[task.level]...)
	buf = append(buf, p.reset...)
	buf = append(buf, ' ')
	buf = append(buf, p.gray...)
	buf = append(buf, loc.file...)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(loc.line), 10)
	buf = append(buf, p.reset...)
	buf = append(buf, ' ')

	if msg != "" {
		buf = append(buf, msg...)
	} else if task.errMsg != "" {
		buf = append(buf, task.errMsg...)
	}
	buf = append(buf, '\n')

	if msg != "" && task.errMsg != "" {
		buf = append(buf, p.gray...)
		buf = append(buf, "   error: "...)
		buf = append(buf, p.red...)
		buf = append(buf, task.errMsg...)
		buf = append(buf, p.reset...)
		buf = append(buf, '\n')
	}

	if len(task.context) > 0 {
		buf = append(buf, p.gray...)
		buf = append(buf, "   included context:"...)
		buf = append(buf, p.reset...)
		buf = append(buf, '\n')
		for _, item := range task.context {
			buf = append(buf, p.white...)
			buf = append(buf, "      "...)
			buf = append(buf, item.Label...)
			buf = append(buf, ':')
			buf = append(buf, p.gray...)
			buf = append(buf, ' ')
			buf = appendPrettyValue(buf, item.Value)
			buf = append(buf, p.reset...)
			buf = append(buf, '\n')
		}
	}

	if len(frames) > 0 {
		hidePkg := l.hidePackagePrefix.Load()
		for _, f := range frames {
			buf = append(buf, p.gray...)
			buf = append(buf, "   at "...)
			buf = append(buf, p.white...)
			buf = append(buf, formatFrameFunc(f.funcName, hidePkg)...)
			buf = append(buf, p.gray...)
			buf = append(buf, ' ')
			buf = append(buf, f.fileName...)
			buf = append(buf, ':')
			buf = strconv.AppendInt(buf, int64(f.line), 10)
			buf = append(buf, p.reset...)
			buf = append(buf, '\n')
		}
	}

	return buf
}

func (l *logger) appendJSONLog(buf []byte, scratch *bytes.Buffer, task logTask, loc callerLoc, msg string, frames []parsedFrame) []byte {
	buf = append(buf, `{"time":"`...)
	buf = task.time.AppendFormat(buf, time.RFC3339Nano)
	buf = append(buf, `","level":"`...)
	buf = append(buf, jsonLabels[task.level]...)
	buf = append(buf, `","source":"`...)
	buf = appendJSONEscaped(buf, loc.file)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(loc.line), 10)
	buf = append(buf, '"')

	if msg != "" {
		buf = append(buf, `,"msg":"`...)
		buf = appendJSONEscaped(buf, msg)
		buf = append(buf, '"')
	}

	if task.errMsg != "" {
		buf = append(buf, `,"error":"`...)
		buf = appendJSONEscaped(buf, task.errMsg)
		buf = append(buf, '"')
	}

	if len(task.context) > 0 {
		buf = append(buf, `,"context":{`...)
		for i, item := range task.context {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = appendJSONEscaped(buf, item.Label)
			buf = append(buf, `":`...)
			buf = appendJSONValue(buf, scratch, item.Value)
		}
		buf = append(buf, '}')
	}

	if len(frames) > 0 {
		hidePkg := l.hidePackagePrefix.Load()
		buf = append(buf, `,"stack":"`...)
		for i, f := range frames {
			if i > 0 {
				buf = append(buf, `\n`...)
			}
			buf = appendJSONEscaped(buf, formatFrameFunc(f.funcName, hidePkg))
			buf = append(buf, ' ')
			buf = appendJSONEscaped(buf, f.fileName)
			buf = append(buf, ':')
			buf = strconv.AppendInt(buf, int64(f.line), 10)
		}
		buf = append(buf, '"')
	}

	buf = append(buf, "}\n"...)
	return buf
}

func appendPrettyValue(buf []byte, v any) []byte {
	switch val := v.(type) {
	case string:
		return append(buf, val...)
	case bool:
		return strconv.AppendBool(buf, val)
	case int:
		return strconv.AppendInt(buf, int64(val), 10)
	case int64:
		return strconv.AppendInt(buf, val, 10)
	case uint:
		return strconv.AppendUint(buf, uint64(val), 10)
	case uint64:
		return strconv.AppendUint(buf, val, 10)
	default:
		return fmt.Appendf(buf, "%+v", v)
	}
}

func appendJSONValue(buf []byte, scratch *bytes.Buffer, v any) []byte {
	if v == nil {
		return append(buf, "null"...)
	}

	switch val := v.(type) {
	case string:
		buf = append(buf, '"')
		buf = appendJSONEscaped(buf, val)
		return append(buf, '"')
	case bool:
		return strconv.AppendBool(buf, val)
	case int:
		return strconv.AppendInt(buf, int64(val), 10)
	case int8:
		return strconv.AppendInt(buf, int64(val), 10)
	case int16:
		return strconv.AppendInt(buf, int64(val), 10)
	case int32:
		return strconv.AppendInt(buf, int64(val), 10)
	case int64:
		return strconv.AppendInt(buf, val, 10)
	case uint:
		return strconv.AppendUint(buf, uint64(val), 10)
	case uint8:
		return strconv.AppendUint(buf, uint64(val), 10)
	case uint16:
		return strconv.AppendUint(buf, uint64(val), 10)
	case uint32:
		return strconv.AppendUint(buf, uint64(val), 10)
	case uint64:
		return strconv.AppendUint(buf, val, 10)
	case uintptr:
		return strconv.AppendUint(buf, uint64(val), 10)
	case float32:
		f := float64(val)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return appendJSONFallbackString(buf, val)
		}
		return strconv.AppendFloat(buf, f, 'g', -1, 32)
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return appendJSONFallbackString(buf, val)
		}
		return strconv.AppendFloat(buf, val, 'g', -1, 64)
	case error:
		if _, isMarshaler := val.(json.Marshaler); !isMarshaler {
			if isNilError(val) {
				return append(buf, "null"...)
			}
			buf = append(buf, '"')
			buf = appendJSONEscaped(buf, val.Error())
			return append(buf, '"')
		}
	}

	scratch.Reset()
	enc := json.NewEncoder(scratch)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return appendJSONFallbackString(buf, v)
	}
	encoded := bytes.TrimSuffix(scratch.Bytes(), []byte{'\n'})
	return append(buf, encoded...)
}

func appendJSONFallbackString(buf []byte, v any) []byte {
	buf = append(buf, '"')
	buf = appendJSONEscaped(buf, fmt.Sprintf("%+v", v))
	return append(buf, '"')
}

const hexDigits = "0123456789abcdef"

func appendJSONEscaped(buf []byte, s string) []byte {
	start := 0
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			if b >= 0x20 && b != '"' && b != '\\' {
				i++
				continue
			}
			if start < i {
				buf = append(buf, s[start:i]...)
			}
			switch b {
			case '"':
				buf = append(buf, `\"`...)
			case '\\':
				buf = append(buf, `\\`...)
			case '\b':
				buf = append(buf, `\b`...)
			case '\f':
				buf = append(buf, `\f`...)
			case '\n':
				buf = append(buf, `\n`...)
			case '\r':
				buf = append(buf, `\r`...)
			case '\t':
				buf = append(buf, `\t`...)
			default:
				buf = append(buf, `\u00`...)
				buf = append(buf, hexDigits[b>>4], hexDigits[b&0x0f])
			}
			i++
			start = i
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			if start < i {
				buf = append(buf, s[start:i]...)
			}
			buf = append(buf, `\ufffd`...)
			i += size
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			if start < i {
				buf = append(buf, s[start:i]...)
			}
			buf = append(buf, `\u202`...)
			buf = append(buf, hexDigits[r&0x0f])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		buf = append(buf, s[start:]...)
	}
	return buf
}
