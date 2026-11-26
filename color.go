package lumo

const (
	ansiReset   = "\033[0m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiMagenta = "\033[35m"
	ansiGray    = "\033[90m"
	ansiWhite   = "\033[37m"
)

type colorPalette struct {
	reset, red, green, yellow, magenta, gray, white string
}

var ansiPalette = colorPalette{
	reset:   ansiReset,
	red:     ansiRed,
	green:   ansiGreen,
	yellow:  ansiYellow,
	magenta: ansiMagenta,
	gray:    ansiGray,
	white:   ansiWhite,
}

func levelANSIColor(lvl level) string {
	switch lvl {
	case levelDebug:
		return ansiMagenta
	case levelInfo:
		return ansiGreen
	case levelWarn:
		return ansiYellow
	case levelError, levelPanic:
		return ansiRed
	default:
		return ""
	}
}
