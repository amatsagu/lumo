package lumo

import (
	"path/filepath"
	"runtime"
	"strings"
)

const (
	maxStackFrames   = 5
	stackCacheKeyLen = 16
	maxCallerCache   = 4096
	maxStackCache    = 512
)

type parsedFrame struct {
	funcName string
	fileName string
	line     int
}

type callerLoc struct {
	file string
	line int
}

type stackCacheKey [stackCacheKeyLen]uintptr

type stackCacheVal struct {
	frames [maxStackFrames]parsedFrame
	count  int
}

func resolveCallerLoc(pc uintptr, cache map[uintptr]callerLoc) callerLoc {
	if pc == 0 {
		return callerLoc{file: "unknown", line: 0}
	}
	if loc, ok := cache[pc]; ok {
		return loc
	}

	var pcs [1]uintptr
	pcs[0] = pc
	frames := runtime.CallersFrames(pcs[:])
	frame, _ := frames.Next()

	loc := callerLoc{file: "unknown", line: frame.Line}
	if frame.File != "" {
		loc.file = filepath.Base(frame.File)
	}

	if len(cache) >= maxCallerCache {
		clear(cache)
	}
	cache[pc] = loc
	return loc
}

func resolveStackFrames(pcs []uintptr, cache map[stackCacheKey]stackCacheVal, out *[maxStackFrames]parsedFrame) int {
	if len(pcs) == 0 {
		return 0
	}

	var key stackCacheKey
	keyLen := min(len(pcs), stackCacheKeyLen)
	copy(key[:], pcs[:keyLen])

	if cached, ok := cache[key]; ok && (len(pcs) <= stackCacheKeyLen || cached.count == maxStackFrames) {
		*out = cached.frames
		return cached.count
	}

	count := collectFrames(pcs, out)

	if len(cache) >= maxStackCache {
		clear(cache)
	}
	cache[key] = stackCacheVal{frames: *out, count: count}
	return count
}

func collectFrames(pcs []uintptr, out *[maxStackFrames]parsedFrame) int {
	if len(pcs) == 0 {
		return 0
	}

	frames := runtime.CallersFrames(pcs)
	count := 0

	for count < maxStackFrames {
		frame, more := frames.Next()
		fullPath := frame.File
		if fullPath == "" && frame.Function == "" {
			if !more {
				break
			}
			continue
		}

		// Hide Go runtime and testing internals.
		if strings.Contains(fullPath, "runtime/") || strings.Contains(fullPath, "testing/") {
			if !more {
				break
			}
			continue
		}

		// Clean function name "path/to/pkg.Func" -> "pkg.Func".
		funcName := frame.Function
		if lastSlash := strings.LastIndexByte(funcName, '/'); lastSlash != -1 {
			funcName = funcName[lastSlash+1:]
		}

		fileName := "unknown"
		if fullPath != "" {
			fileName = filepath.Base(fullPath)
		}

		// Hide Lumo internals unless coming from a test file.
		if strings.HasPrefix(funcName, "lumo.") && !strings.HasSuffix(fileName, "_test.go") {
			if !more {
				break
			}
			continue
		}

		out[count] = parsedFrame{
			funcName: funcName,
			fileName: fileName,
			line:     frame.Line,
		}
		count++

		if !more {
			break
		}
	}

	return count
}

func formatFrameFunc(funcName string, hidePkgPrefix bool) string {
	if hidePkgPrefix {
		if dotIdx := strings.IndexByte(funcName, '.'); dotIdx != -1 {
			return funcName[dotIdx+1:]
		}
	}
	return funcName
}
