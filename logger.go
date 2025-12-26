package lumo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type level int

const (
	levelDebug level = iota
	levelInfo
	levelWarn
	levelError
	levelPanic // Treated as Fatal (Log + Sync logs + Exit)
)

type logger struct {
	minLevel          level
	output            io.Writer
	stackOnWarn       bool
	hidePackagePrefix bool
	timeFormat        string

	// Thread-safety for config & worker state
	mu           sync.RWMutex
	logQueue     chan logTask
	workerActive bool
	wg           sync.WaitGroup
}

var l = &logger{
	minLevel:   levelInfo,
	output:     os.Stdout,
	timeFormat: "02/01/2006 15:04:05 UTC",
}

// ForceColors affects the global color state.
func ForceColors(enable bool) {
	noColor = !enable
	refreshColors()
}

func Close()                   { l.stopWorker() }
func ChangeOutput(w io.Writer) { l.mu.Lock(); l.output = w; l.mu.Unlock() }
func EnableDebug()             { l.mu.Lock(); l.minLevel = levelDebug; l.mu.Unlock() }

// Makes logger attach stack traces below warn logs.
func EnableStackOnWarns() { l.mu.Lock(); l.stackOnWarn = true; l.mu.Unlock() }

// Makes logger only display function names in stack traces.
func HidePackagePrefix()               { l.mu.Lock(); l.hidePackagePrefix = true; l.mu.Unlock() }
func Debug(format string, args ...any) { l.emit(levelDebug, cMagenta, "DEBUG", format, args...) }
func Info(format string, args ...any)  { l.emit(levelInfo, cGreen, " INFO", format, args...) }
func Warn(format string, args ...any)  { l.emit(levelWarn, cYellow, " WARN", format, args...) }
func Error(format string, args ...any) { l.emit(levelError, cRed, "ERROR", format, args...) }
func Panic(format string, args ...any) { l.emit(levelPanic, cRed, "PANIC", format, args...) }

func (l *logger) emit(lvl level, clr, label, format string, args ...any) {
	if lvl < l.minLevel {
		return
	}

	now := time.Now().UTC()
	fileName := "unknown"
	lineNum := 0

	_, file, line, ok := runtime.Caller(2)
	if ok {
		fileName = filepath.Base(file)
		lineNum = line
	}

	msg := fmt.Sprintf(format, args...)

	var stack []byte
	var ctx []contextItem

	capture := false
	if lvl >= levelError {
		capture = true
	} else if lvl == levelWarn {
		l.mu.RLock()
		if l.stackOnWarn {
			capture = true
		}
		l.mu.RUnlock()
	}

	if capture {
		for _, arg := range args {
			if le, ok := arg.(*LumoError); ok {
				stack = le.stack
				ctx = le.context
				break
			}
		}

		if stack == nil {
			stack = captureStack()
		}
	}

	l.enqueue(logTask{
		level:      lvl,
		levelColor: clr,
		label:      label,
		time:       now,
		file:       fileName,
		line:       lineNum,
		msg:        msg,
		stack:      stack,
		context:    ctx,
	})

	if lvl == levelPanic {
		l.stopWorker()
		os.Exit(2)
	}
}
