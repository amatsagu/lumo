package lumo

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strings"
)

func printParsedStack(w *bufio.Writer, stack []byte) {
	lines := strings.Split(string(stack), "\n")

	var currentFunc string
	framesPrinted := 0
	const maxFrames = 5

	for i, line := range lines {
		if len(line) == 0 {
			continue
		}

		if i%2 == 1 {
			// Clean function name "path/to/pkg.Func" -> "pkg.Func"
			if idx := strings.LastIndexByte(line, '('); idx != -1 {
				currentFunc = line[:idx]
			} else {
				currentFunc = line
			}
			if lastSlash := strings.LastIndexByte(currentFunc, '/'); lastSlash != -1 {
				currentFunc = currentFunc[lastSlash+1:]
			}
			continue
		}

		if i%2 == 0 {
			if framesPrinted >= maxFrames {
				break
			}

			if !strings.HasPrefix(line, "\t") {
				continue
			}

			line = strings.TrimSpace(line)
			if idx := strings.LastIndex(line, " +"); idx != -1 {
				line = line[:idx]
			}

			colonIdx := strings.LastIndexByte(line, ':')
			if colonIdx == -1 {
				continue
			}

			fullPath := line[:colonIdx]
			lineNum := line[colonIdx+1:]
			fileName := filepath.Base(fullPath)

			// Hide Runtime & Testing internals
			if strings.Contains(fullPath, "runtime/") || strings.Contains(fullPath, "testing/") {
				continue
			}

			// Hide Lumo Internals
			if strings.HasPrefix(currentFunc, "lumo.") {
				// ...UNLESS it's coming from a test file!
				if !strings.HasSuffix(fileName, "_test.go") {
					continue
				}
			}

			// Optionally remove package prefix for display
			// "lumo.TestStackParsing" -> "TestStackParsing"
			l.mu.RLock()
			displayFunc := currentFunc
			if dotIdx := strings.IndexByte(displayFunc, '.'); l.hidePackagePrefix && dotIdx != -1 {
				displayFunc = displayFunc[dotIdx+1:]
			}
			l.mu.RUnlock()

			fmt.Fprintf(w, "%s   at %s%s %s%s:%s%s\n",
				cGray,
				cWhite, displayFunc,
				cGray,
				fileName, lineNum,
				cReset,
			)

			framesPrinted++
		}
	}
}
