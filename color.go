package lumo

import (
	"github.com/fatih/color"
)

var (
	cReset, cRed, cGreen, cYellow, cMagenta, cGray, cWhite string
)

// init runs automatically. fatih/color's own init() ensures Windows support is enabled.
func init() {
	refreshColors()
}

// refreshColors sets the raw ANSI strings based on what fatih/color detects.
func refreshColors() {
	if color.NoColor {
		cReset, cRed, cGreen, cYellow, cMagenta, cGray, cWhite = "", "", "", "", "", "", ""
		return
	}

	cReset = "\033[0m"
	cRed = "\033[31m"
	cGreen = "\033[32m"
	cYellow = "\033[33m"
	cMagenta = "\033[35m"
	cGray = "\033[90m"
	cWhite = "\033[37m"
}
