package lumo

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type level int32

const (
	levelDebug level = iota
	levelInfo
	levelWarn
	levelError
	levelPanic // Treated as Fatal (Log + Sync logs + Exit)
)

const defaultTimeFormat = "02/01/2006 15:04:05 UTC"

type msgBuffer struct {
	data []byte
}

var (
	msgPool = sync.Pool{
		New: func() any {
			return &msgBuffer{data: make([]byte, 0, 256)}
		},
	}
	stackPool = sync.Pool{
		New: func() any {
			return new(stackBuffer)
		},
	}
)

type logger struct {
	minLevel          atomic.Int32
	stackOnWarn       atomic.Bool
	hidePackagePrefix atomic.Bool
	jsonMode          atomic.Bool
	noColor           atomic.Bool

	outputMu   sync.Mutex
	output     io.Writer
	outputSeq  atomic.Uint64
	timeFormat string

	mu           sync.RWMutex
	logQueue     chan logTask
	workerActive bool
	wg           sync.WaitGroup
}

func newLogger() *logger {
	lg := &logger{
		output:     os.Stdout,
		timeFormat: defaultTimeFormat,
	}
	lg.minLevel.Store(int32(levelInfo))
	return lg
}

var l = newLogger()

// ForceColors enables or disables ANSI colors in pretty log output.
func ForceColors(enable bool) { l.noColor.Store(!enable) }

// Close flushes pending logs and stops the background worker.
func Close() { l.stopWorker() }

// ChangeOutput updates the destination writer for subsequent log writes.
func ChangeOutput(w io.Writer) {
	if w == nil {
		return
	}
	l.outputMu.Lock()
	l.output = w
	l.outputSeq.Add(1)
	l.outputMu.Unlock()
}

// EnableDebug lowers the minimum log level to Debug.
func EnableDebug() { l.minLevel.Store(int32(levelDebug)) }

// EnableStackOnWarns attaches stack traces to Warn logs.
func EnableStackOnWarns() { l.stackOnWarn.Store(true) }

// HidePackagePrefix strips package prefixes from function names in stack traces.
func HidePackagePrefix() { l.hidePackagePrefix.Store(true) }

// SwitchToJSON switches log output from pretty formatting to JSON lines.
func SwitchToJSON() { l.jsonMode.Store(true) }

func Debug(msg any, args ...any) { l.emit(levelDebug, msg, args...) }
func Info(msg any, args ...any)  { l.emit(levelInfo, msg, args...) }
func Warn(msg any, args ...any)  { l.emit(levelWarn, msg, args...) }
func Error(msg any, args ...any) { l.emit(levelError, msg, args...) }
func Panic(msg any, args ...any) { l.emit(levelPanic, msg, args...) }

func (l *logger) emit(lvl level, msgOrErr any, args ...any) {
	if int32(lvl) < l.minLevel.Load() {
		return
	}

	now := time.Now().UTC()

	msg, msgBuf, errMsg, stack, ctx := extractMessageAndError(msgOrErr, args...)

	needStack := lvl >= levelError || (lvl == levelWarn && l.stackOnWarn.Load())
	if !needStack {
		stack = nil
	}

	var callerPC uintptr
	var stackBuf *stackBuffer

	if needStack && len(stack) == 0 {
		sb := stackPool.Get().(*stackBuffer)
		sb.n = runtime.Callers(3, sb.pcs[:])
		if sb.n > 0 {
			callerPC = sb.pcs[0]
			stackBuf = sb
		} else {
			stackPool.Put(sb)
		}
	} else {
		var pc [1]uintptr
		if runtime.Callers(3, pc[:]) > 0 {
			callerPC = pc[0]
		}
	}

	l.enqueue(logTask{
		level:    lvl,
		callerPC: callerPC,
		time:     now,
		msg:      msg,
		msgBuf:   msgBuf,
		errMsg:   errMsg,
		stack:    stack,
		stackBuf: stackBuf,
		context:  ctx,
	})

	if lvl == levelPanic {
		l.stopWorker()
		os.Exit(2)
	}
}

func extractMessageAndError(msgOrErr any, args ...any) (msg string, msgBuf *msgBuffer, errMsg string, stack []uintptr, ctx []contextItem) {
	if err, ok := msgOrErr.(error); ok {
		if !isNilError(err) {
			errMsg = err.Error()
			stack, ctx = extractLumoMetadata(err)
			return "", nil, errMsg, stack, ctx
		}
		return "<nil>", nil, "", nil, nil
	}

	var firstErr error
	var foundLumo bool
	for _, arg := range args {
		err, ok := arg.(error)
		if !ok || isNilError(err) {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		if !foundLumo {
			if s, c, ok := extractLumoMetadataCheck(err); ok {
				stack, ctx = s, c
				foundLumo = true
			}
		}
	}
	if firstErr != nil {
		errMsg = firstErr.Error()
	}

	format, isStr := msgOrErr.(string)
	if !isStr {
		if len(args) == 0 {
			mb := msgPool.Get().(*msgBuffer)
			mb.data = fmt.Append(mb.data[:0], msgOrErr)
			return "", mb, errMsg, stack, ctx
		}
		fmtArgs := stripTrailingErrors(args, 0)
		mb := msgPool.Get().(*msgBuffer)
		if len(fmtArgs) == 0 {
			mb.data = fmt.Append(mb.data[:0], msgOrErr)
			return "", mb, errMsg, stack, ctx
		}
		mb.data = fmt.Append(mb.data[:0], msgOrErr)
		mb.data = fmt.Append(mb.data, fmtArgs...)
		return "", mb, errMsg, stack, ctx
	}

	if len(args) == 0 {
		if strings.IndexByte(format, '%') == -1 {
			return format, nil, errMsg, stack, ctx
		}
		mb := msgPool.Get().(*msgBuffer)
		mb.data = fmt.Appendf(mb.data[:0], format, args...)
		return "", mb, errMsg, stack, ctx
	}

	expected := countFormatArgs(format)
	fmtArgs := stripTrailingErrors(args, expected)
	if len(fmtArgs) == 0 && strings.IndexByte(format, '%') == -1 {
		return format, nil, errMsg, stack, ctx
	}
	mb := msgPool.Get().(*msgBuffer)
	mb.data = fmt.Appendf(mb.data[:0], format, fmtArgs...)
	return "", mb, errMsg, stack, ctx
}

type singleUnwrapper interface {
	Unwrap() error
}

type multiUnwrapper interface {
	Unwrap() []error
}

type customAs interface {
	As(any) bool
}

func extractLumoMetadata(err error) ([]uintptr, []contextItem) {
	stack, ctx, _ := extractLumoMetadataCheck(err)
	return stack, ctx
}

func extractLumoMetadataCheck(err error) ([]uintptr, []contextItem, bool) {
	cur := err
	for cur != nil {
		if le, ok := cur.(*LumoError); ok {
			return lumoMetadataFrom(le)
		}
		switch u := cur.(type) {
		case singleUnwrapper:
			cur = u.Unwrap()
		case multiUnwrapper, customAs:
			var le *LumoError
			if !errors.As(err, &le) || le == nil {
				return nil, nil, false
			}
			return lumoMetadataFrom(le)
		default:
			return nil, nil, false
		}
	}
	return nil, nil, false
}

func lumoMetadataFrom(le *LumoError) ([]uintptr, []contextItem, bool) {
	if le == nil {
		return nil, nil, false
	}
	var ctx []contextItem
	if len(le.context) > 0 {
		ctx = slices.Clone(le.context)
	}
	return le.stack, ctx, true
}

func stripTrailingErrors(args []any, minKeep int) []any {
	cut := len(args)
	for cut > minKeep {
		if _, ok := args[cut-1].(error); ok {
			cut--
			continue
		}
		break
	}
	return args[:cut]
}

func countFormatArgs(format string) int {
	maxArg := 0
	argNum := 0
	i := 0
	n := len(format)

	for i < n {
		if format[i] != '%' {
			i++
			continue
		}
		i++
		if i >= n {
			break
		}
		if format[i] == '%' {
			i++
			continue
		}

		for i < n && (format[i] == '+' || format[i] == '-' || format[i] == '#' || format[i] == ' ' || format[i] == '0') {
			i++
		}

		explicitIdx, nextI, ok := parseArgIndex(format, i)
		if ok {
			argNum = explicitIdx
			i = nextI
		}

		if i < n && format[i] == '*' {
			if !ok {
				argNum++
			}
			if argNum > maxArg {
				maxArg = argNum
			}
			i++
		} else {
			for i < n && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}

		if i < n && format[i] == '.' {
			i++
			precIdx, nextP, precOk := parseArgIndex(format, i)
			if precOk {
				argNum = precIdx
				i = nextP
			}
			if i < n && format[i] == '*' {
				if !precOk {
					argNum++
				}
				if argNum > maxArg {
					maxArg = argNum
				}
				i++
			} else {
				for i < n && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
		}

		verbIdx, nextV, verbOk := parseArgIndex(format, i)
		if verbOk {
			argNum = verbIdx
			i = nextV
		}

		if i < n {
			if !verbOk {
				argNum++
			}
			if argNum > maxArg {
				maxArg = argNum
			}
			i++
		}
	}

	return maxArg
}

func parseArgIndex(s string, i int) (int, int, bool) {
	n := len(s)
	if i >= n || s[i] != '[' {
		return 0, i, false
	}
	j := i + 1
	if j >= n || s[j] < '1' || s[j] > '9' {
		return 0, i, false
	}
	val := 0
	for j < n && s[j] >= '0' && s[j] <= '9' {
		val = val*10 + int(s[j]-'0')
		j++
	}
	if j >= n || s[j] != ']' {
		return 0, i, false
	}
	return val, j + 1, true
}
