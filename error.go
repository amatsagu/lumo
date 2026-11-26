package lumo

import (
	"fmt"
	"reflect"
	"runtime"
)

const maxCaptureDepth = 32

type stackBuffer struct {
	pcs [maxCaptureDepth]uintptr
	n   int
}

// LumoError wraps an error with a captured stack trace and optional key-value context.
type LumoError struct {
	err     error
	stack   []uintptr
	context []contextItem
}

type contextItem struct {
	Label string
	Value any
}

func (e *LumoError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *LumoError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Include attaches a labeled value to the error for structured logging.
func (e *LumoError) Include(label string, data any) *LumoError {
	if e == nil {
		return nil
	}
	e.context = append(e.context, contextItem{Label: label, Value: data})
	return e
}

// WrapString creates a new LumoError from a formatted message and captures the stack trace.
func WrapString(format string, a ...any) *LumoError {
	return &LumoError{
		err:   fmt.Errorf(format, a...),
		stack: captureStack(3),
	}
}

// WrapError wraps an existing error with a stack trace, returning it directly if already a *LumoError.
func WrapError(err error) *LumoError {
	if isNilError(err) {
		return nil
	}

	if le, ok := err.(*LumoError); ok {
		return le
	}

	return &LumoError{
		err:   err,
		stack: captureStack(3),
	}
}

func captureStack(skip int) []uintptr {
	var buf [maxCaptureDepth]uintptr
	n := runtime.Callers(skip, buf[:])
	if n == 0 {
		return nil
	}
	pcs := make([]uintptr, n)
	copy(pcs, buf[:n])
	return pcs
}

func isNilError(err error) bool {
	if err == nil {
		return true
	}
	if le, ok := err.(*LumoError); ok {
		return le == nil
	}
	v := reflect.ValueOf(err)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}
