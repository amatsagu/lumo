package lumo

import (
	"fmt"
	"runtime/debug"
)

// Custom error wrapper that holds a stack trace and simple, additional context.
type LumoError struct {
	err     error
	stack   []byte
	context []contextItem
}

type contextItem struct {
	Label string
	Value any
}

func (e *LumoError) Error() string { return e.err.Error() }
func (e *LumoError) Unwrap() error { return e.err }

func (e *LumoError) Include(label string, data any) *LumoError {
	e.context = append(e.context, contextItem{Label: label, Value: data})
	return e
}

func WrapString(format string, a ...any) *LumoError {
	return &LumoError{
		err:   fmt.Errorf(format, a...),
		stack: captureStack(),
	}
}

func WrapError(err error) *LumoError {
	if err == nil {
		return nil
	}

	if le, ok := err.(*LumoError); ok {
		return le
	}

	return &LumoError{
		err:   err,
		stack: captureStack(),
	}
}

func captureStack() []byte {
	return debug.Stack()
}
