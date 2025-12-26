package lumo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/fatih/color"
)

type level int

const (
	levelDebug level = iota
	levelInfo
	levelWarn
	levelError
	levelPanic // Treated as Fatal (Log + Exit)
)

type logger struct {
	minLevel level
	output   io.Writer

	// Configuration flags
	stackOnWarn bool

	// Thread-safety for config & worker state
	mu           sync.Mutex
	logQueue     chan logTask
	workerActive bool
	wg           sync.WaitGroup
}

var l = &logger{
	minLevel: levelInfo,
	output:   os.Stdout,
}

// ForceColors affects the global color state.
func ForceColors(enable bool) {
	if enable {
		color.NoColor = false
	} else {
		fileInfo, _ := os.Stdout.Stat()
		isTerminal := (fileInfo.Mode() & os.ModeCharDevice) != 0
		color.NoColor = !isTerminal && os.Getenv("NO_COLOR") == ""
	}
	refreshColors()
}

// --- PUBLIC CONFIGURATION ---

func Close()                   { l.stopWorker() }
func ChangeOutput(w io.Writer) { l.mu.Lock(); l.output = w; l.mu.Unlock() }
func EnableDebug()             { l.mu.Lock(); l.minLevel = levelDebug; l.mu.Unlock() }

// EnableStackOnWarns configures the logger to attach stack traces to Warn logs.
func EnableStackOnWarns() {
	l.mu.Lock()
	l.stackOnWarn = true
	l.mu.Unlock()
}

// --- PUBLIC LOGGING METHODS ---

func Debug(format string, args ...any) { l.emit(levelDebug, cMagenta, "DEBUG", format, args...) }
func Info(format string, args ...any)  { l.emit(levelInfo, cGreen, " INFO", format, args...) }
func Warn(format string, args ...any)  { l.emit(levelWarn, cYellow, " WARN", format, args...) }
func Error(format string, args ...any) { l.emit(levelError, cRed, "ERROR", format, args...) }

// Panic logs the message with a stack trace, flushes the logger, and then EXITS the app (os.Exit 1).
func Panic(format string, args ...any) {
	l.emit(levelPanic, cRed, "PANIC", format, args...)
}

// --- INTERNAL LOGIC ---

func (l *logger) emit(lvl level, clr, label, format string, args ...any) {
	if lvl < l.minLevel {
		return
	}

	now := time.Now().UTC()

	// Caller Resolution
	fileName := "unknown"
	lineNum := 0

	// Scan stack to find user code
	for i := 2; i < 7; i++ {
		_, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		base := filepath.Base(file)
		if base == "logger.go" || base == "worker.go" {
			continue
		}
		fileName = base
		lineNum = line
		break
	}

	msg := fmt.Sprintf(format, args...)

	var stack []byte
	var ctx []contextItem

	// --- STACK TRACE LOGIC ---
	capture := false

	// Always capture stack for Error and Panic
	if lvl >= levelError {
		capture = true
	} else if lvl == levelWarn {
		l.mu.Lock()
		if l.stackOnWarn {
			capture = true
		}
		l.mu.Unlock()
	}

	if capture {
		// 1. Look for pre-existing LumoError
		for _, arg := range args {
			if le, ok := arg.(*LumoError); ok {
				stack = le.stack
				ctx = le.context
				break
			}
		}
		// 2. Capture now if missing
		if stack == nil {
			stack = captureStack()
		}
	}

	// Create task
	task := logTask{
		level:      lvl,
		levelColor: clr,
		label:      label,
		time:       now,
		file:       fileName,
		line:       lineNum,
		msg:        msg,
		stack:      stack,
		context:    ctx,
	}

	// Enqueue to worker
	l.enqueue(task)

	// --- PANIC / EXIT HANDLING ---
	if lvl == levelPanic {
		// 1. Force the worker to finish everything and shut down
		l.stopWorker()

		// 2. Exit the application with status code 1
		os.Exit(1)
	}
}
