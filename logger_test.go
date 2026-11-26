package lumo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetLoggerForTest() {
	l.minLevel.Store(int32(levelInfo))
	l.stackOnWarn.Store(false)
	l.hidePackagePrefix.Store(false)
	l.jsonMode.Store(false)
	l.noColor.Store(false)
}

// captureLog manipulates the GLOBAL logger state to capture output.
func captureLog(f func()) string {
	Close()
	resetLoggerForTest()

	var buf bytes.Buffer
	ChangeOutput(&buf)

	f()

	Close()
	ChangeOutput(os.Stdout)
	resetLoggerForTest()
	return buf.String()
}

func TestConfigure(t *testing.T) {
	resetLoggerForTest()
	defer resetLoggerForTest()

	EnableDebug()
	if level(l.minLevel.Load()) != levelDebug {
		t.Error("Failed to set Debug level")
	}

	SwitchToJSON()
	if !l.jsonMode.Load() {
		t.Error("Failed to switch to JSON mode")
	}
}

func TestLogFormatting(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		Info("User logged in: %s", "Yuli")
	})

	if !strings.Contains(out, "INFO") {
		t.Error("Output missing level label")
	}
	if !strings.Contains(out, "User logged in: Yuli") {
		t.Error("Output missing formatted message")
	}
	if !strings.Contains(out, "_test.go") {
		t.Error("Output missing source file")
	}
}

func TestCustomErrorContext(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		err := WrapString("validation failed")
		err.Include("request_id", 12345)
		Error("Handler crash: %v", err)
	})

	if !strings.Contains(out, "Handler crash: validation failed") {
		t.Errorf("Missing message, got:\n%s", out)
	}
	if !strings.Contains(out, "   error: validation failed") {
		t.Errorf("Missing dedicated error line, got:\n%s", out)
	}
	if !strings.Contains(out, "included context:") {
		t.Error("Missing context header")
	}
	if !strings.Contains(out, "request_id: 12345") {
		t.Error("Missing integer context value")
	}
}

func TestRegularErrorMatchesLumoErrorPretty(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		Error("Handler crash", errors.New("connection refused"))
		Error("Percent 100%% failed for %s", "worker-1", errors.New("timeout"))
		Error(errors.New("standalone error"))
	})

	if strings.Contains(out, "%!(EXTRA") {
		t.Fatalf("Unexpected EXTRA formatting artifact in output:\n%s", out)
	}
	if !strings.Contains(out, "Handler crash\n   error: connection refused\n") {
		t.Errorf("Expected separate error line for regular error, got:\n%s", out)
	}
	if !strings.Contains(out, "Percent 100% failed for worker-1\n   error: timeout\n") {
		t.Errorf("Expected formatted message plus trailing error line, got:\n%s", out)
	}
	if !strings.Contains(out, "   at ") {
		t.Errorf("Expected stack trace for regular errors, got:\n%s", out)
	}

	// Standalone Error(err) should put error on main line without duplicating "   error:" line.
	lines := strings.Split(out, "\n")
	standaloneIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "standalone error") {
			standaloneIdx = i
			break
		}
	}
	if standaloneIdx == -1 {
		t.Fatalf("Missing standalone error in output:\n%s", out)
	}
	if standaloneIdx+1 < len(lines) && strings.Contains(lines[standaloneIdx+1], "   error:") {
		t.Errorf("Standalone Error(err) should not emit duplicate error line, got:\n%s", out)
	}
}

func TestWrappedLumoErrorUnwrapping(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		inner := func() error {
			return WrapString("bad token").Include("user", "alice")
		}()
		wrapped := fmt.Errorf("auth failed: %w", inner)
		Error("Request aborted", wrapped)
	})

	if !strings.Contains(out, "   error: auth failed: bad token") {
		t.Errorf("Expected outer error text, got:\n%s", out)
	}
	if !strings.Contains(out, "included context:") || !strings.Contains(out, "user: alice") {
		t.Errorf("Expected inner LumoError context via errors.As, got:\n%s", out)
	}
	if !strings.Contains(out, "TestWrappedLumoErrorUnwrapping") {
		t.Errorf("Expected stack trace from inner LumoError, got:\n%s", out)
	}
}

func TestErrorOnInfoAndWarnLevels(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		lErr := WrapString("cache miss").Include("key", "cfg")
		Info("Fallback used", lErr)
		Warn("Slow path", errors.New("timeout"))
	})

	if !strings.Contains(out, "   error: cache miss") {
		t.Errorf("Expected error line on Info, got:\n%s", out)
	}
	if !strings.Contains(out, "key: cfg") {
		t.Errorf("Expected included context on Info for LumoError, got:\n%s", out)
	}
	if !strings.Contains(out, "   error: timeout") {
		t.Errorf("Expected error line on Warn, got:\n%s", out)
	}
	if strings.Contains(out, "   at ") {
		t.Errorf("Expected no stack trace on Info or Warn when stackOnWarn is false, got:\n%s", out)
	}
}

func TestStackParsing(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		func() {
			err := WrapString("deep error")
			Error("Boom: %v", err)
		}()
	})
	if !strings.Contains(out, "TestStackParsing") {
		t.Errorf("Stack trace missing current function name, got:\n%s", out)
	}
}

func TestHidePackagePrefix(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		HidePackagePrefix()
		Error("boom")
	})

	if strings.Contains(out, "lumo.TestHidePackagePrefix") {
		t.Errorf("Expected package prefix to be hidden, got:\n%s", out)
	}
	if !strings.Contains(out, "at TestHidePackagePrefix") {
		t.Errorf("Expected stripped function name in stack, got:\n%s", out)
	}
}

func TestWarnStackToggle(t *testing.T) {
	// 1. Default: No stack on Warn
	out1 := captureLog(func() {
		ForceColors(false)
		Warn("Simple warning")
	})
	if strings.Contains(out1, "   at ") {
		t.Error("Warn should not have stack trace by default")
	}

	// 2. Enabled: Stack on Warn
	out2 := captureLog(func() {
		ForceColors(false)
		EnableStackOnWarns()
		Warn("Complex warning")
	})
	if !strings.Contains(out2, "   at ") {
		t.Error("Warn should have stack trace when enabled")
	}
}

func TestJSONMode(t *testing.T) {
	out := captureLog(func() {
		ForceColors(true) // JSON mode must never emit ANSI color codes
		SwitchToJSON()
		EnableDebug()
		HidePackagePrefix()

		Debug("debug %d", 1)
		Info("hello \"world\"\nline2")
		Error("failed to connect", errors.New("dial tcp: timeout"))
		Error(errors.New("standalone json err"))

		lErr := WrapString("invalid payload").
			Include("count", 42).
			Include("meta", map[string]any{"ok": true, "tag": "a&b"}).
			Include("nested_err", errors.New("bad json")).
			Include("nan", math.NaN()).
			Include("fn", func() {})
		Error("crash", lErr)
	})

	if strings.Contains(out, "\033[") {
		t.Fatalf("JSON output must not contain ANSI escape codes:\n%s", out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 {
		t.Fatalf("Expected 5 JSON lines, got %d:\n%s", len(lines), out)
	}

	type jsonEntry struct {
		Time    string         `json:"time"`
		Level   string         `json:"level"`
		Source  string         `json:"source"`
		Msg     *string        `json:"msg"`
		Error   *string        `json:"error"`
		Context map[string]any `json:"context"`
		Stack   *string        `json:"stack"`
	}

	var entries []jsonEntry
	for i, line := range lines {
		var e jsonEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("Line %d invalid JSON (%v): %s", i, err, line)
		}
		if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
			t.Errorf("Line %d invalid RFC3339Nano time %q: %v", i, e.Time, err)
		}
		if !strings.HasPrefix(e.Source, "logger_test.go:") {
			t.Errorf("Line %d unexpected source %q", i, e.Source)
		}
		entries = append(entries, e)
	}

	// 0: Debug
	if entries[0].Level != "DEBUG" || *entries[0].Msg != "debug 1" {
		t.Errorf("Unexpected debug entry: %+v", entries[0])
	}
	if entries[0].Error != nil || entries[0].Stack != nil || entries[0].Context != nil {
		t.Errorf("Debug entry should omit error/stack/context: %+v", entries[0])
	}

	// 1: Info with escapes
	if entries[1].Level != "INFO" || *entries[1].Msg != "hello \"world\"\nline2" {
		t.Errorf("Unexpected info entry: %+v", entries[1])
	}

	// 2: Regular error with msg
	if entries[2].Level != "ERROR" || *entries[2].Msg != "failed to connect" || *entries[2].Error != "dial tcp: timeout" {
		t.Errorf("Unexpected regular error entry: %+v", entries[2])
	}
	if entries[2].Stack == nil || !strings.Contains(*entries[2].Stack, "TestJSONMode logger_test.go:") {
		t.Errorf("Regular error entry missing stack: %+v", entries[2])
	}
	if entries[2].Context != nil {
		t.Errorf("Regular error entry should not have context: %+v", entries[2])
	}

	// 3: Standalone Error(err) omits msg
	if entries[3].Msg != nil {
		t.Errorf("Standalone Error(err) should omit msg, got %q", *entries[3].Msg)
	}
	if entries[3].Error == nil || *entries[3].Error != "standalone json err" {
		t.Errorf("Standalone Error(err) missing error field: %+v", entries[3])
	}
	if entries[3].Stack == nil {
		t.Errorf("Standalone Error(err) missing stack")
	}

	// 4: LumoError with context
	if entries[4].Error == nil || *entries[4].Error != "invalid payload" {
		t.Errorf("LumoError entry missing error field: %+v", entries[4])
	}
	if entries[4].Context["count"] != float64(42) {
		t.Errorf("Expected numeric context count=42, got %#v", entries[4].Context["count"])
	}
	meta, ok := entries[4].Context["meta"].(map[string]any)
	if !ok || meta["ok"] != true || meta["tag"] != "a&b" {
		t.Errorf("Expected native JSON map in context, got %#v", entries[4].Context["meta"])
	}
	if entries[4].Context["nested_err"] != "bad json" {
		t.Errorf("Expected error context to serialize to string, got %#v", entries[4].Context["nested_err"])
	}
	if entries[4].Context["nan"] != "NaN" {
		t.Errorf("Expected NaN fallback string, got %#v", entries[4].Context["nan"])
	}
	if _, ok := entries[4].Context["fn"].(string); !ok {
		t.Errorf("Expected unmarshalable func fallback string, got %#v", entries[4].Context["fn"])
	}
	if entries[4].Stack == nil || strings.Contains(*entries[4].Stack, "lumo.") {
		t.Errorf("Expected stripped package prefix in JSON stack, got %v", entries[4].Stack)
	}
}

func TestNilAndEdgeCases(t *testing.T) {
	out := captureLog(func() {
		ForceColors(false)
		var typedNil *LumoError
		Error("typed nil", typedNil)
		Error(nil)
		if got := WrapError(typedNil); got != nil {
			t.Errorf("WrapError(typedNil) should return nil")
		}
		if typedNil.Error() != "" || typedNil.Unwrap() != nil || typedNil.Include("a", 1) != nil {
			t.Errorf("Nil *LumoError methods should be nil-safe")
		}
	})

	if !strings.Contains(out, "typed nil") {
		t.Errorf("Expected typed nil message, got:\n%s", out)
	}
	if strings.Contains(out, "   error:") {
		t.Errorf("Typed nil error should not emit error line, got:\n%s", out)
	}
}

func TestDynamicChangeOutputAndConcurrency(t *testing.T) {
	Close()
	resetLoggerForTest()
	defer func() {
		Close()
		ChangeOutput(os.Stdout)
		resetLoggerForTest()
	}()

	var buf1, buf2 bytes.Buffer
	var safeBuf1 = &syncBuffer{buf: &buf1}
	var safeBuf2 = &syncBuffer{buf: &buf2}

	ForceColors(false)
	ChangeOutput(safeBuf1)
	Info("first writer")

	// Give worker a moment to process, then swap output and format dynamically.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 25 {
				Info("msg %d-%d", id, j)
				if j == 10 && id == 0 {
					EnableDebug()
					EnableStackOnWarns()
					HidePackagePrefix()
					SwitchToJSON()
					ChangeOutput(safeBuf2)
				}
				Warn("warn %d-%d", id, j, errors.New("boom"))
			}
		}(i)
	}
	wg.Wait()
	Close()

	if safeBuf1.Len() == 0 && safeBuf2.Len() == 0 {
		t.Fatal("Expected output in buffers")
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf *bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}

// TestOversizedScratchTrim verifies oversized worker buffers shrink right after writing.
func TestOversizedScratchTrim(t *testing.T) {
	ws := workerScratch{buf: make([]byte, 0, defaultBufCap)}
	ws.buf = make([]byte, 0, maxWorkerBufCap+1024)
	ws.jsonScratch.Grow(maxWorkerBufCap + 1024)

	ws.trim()

	if cap(ws.buf) != defaultBufCap {
		t.Fatalf("expected buf cap to shrink to %d, got %d", defaultBufCap, cap(ws.buf))
	}
	if ws.jsonScratch.Cap() != 0 {
		t.Fatalf("expected jsonScratch cap to reset to 0, got %d", ws.jsonScratch.Cap())
	}

	// Buffers within the cap must be retained for zero-alloc reuse.
	ws.buf = make([]byte, 0, maxWorkerBufCap)
	ws.trim()
	if cap(ws.buf) != maxWorkerBufCap {
		t.Fatalf("expected buf cap %d to be retained, got %d", maxWorkerBufCap, cap(ws.buf))
	}

	largePayload := strings.Repeat("x", 32*1024)
	out := captureLog(func() {
		SwitchToJSON()
		Info("big: %s", largePayload)
		lErr := WrapString("big ctx").Include("blob", largePayload)
		Error("big err", lErr)
		Info("small follow-up")
	})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSON lines, got %d", len(lines))
	}
	for i, line := range lines {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
	}
}

// TestPanicExit checks if Panic() actually terminates the process
func TestPanicExit(t *testing.T) {
	if os.Getenv("LUMO_TEST_CRASH") == "1" {
		ForceColors(false)
		Panic("Critical Failure")
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestPanicExit")
	cmd.Env = append(os.Environ(), "LUMO_TEST_CRASH=1")

	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}

	t.Fatalf("Process ran with err %v, expected exit status 2", err)
}

func BenchmarkInfoPretty(b *testing.B) {
	Close()
	resetLoggerForTest()
	ForceColors(false)
	ChangeOutput(io.Discard)
	defer func() {
		Close()
		ChangeOutput(os.Stdout)
		resetLoggerForTest()
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		Info("User logged in: %s", "Yuli")
	}
	Close()
}

func BenchmarkInfoJSON(b *testing.B) {
	Close()
	resetLoggerForTest()
	SwitchToJSON()
	ChangeOutput(io.Discard)
	defer func() {
		Close()
		ChangeOutput(os.Stdout)
		resetLoggerForTest()
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		Info("User logged in: %s", "Yuli")
	}
	Close()
}

func BenchmarkErrorJSON(b *testing.B) {
	Close()
	resetLoggerForTest()
	SwitchToJSON()
	ChangeOutput(io.Discard)
	defer func() {
		Close()
		ChangeOutput(os.Stdout)
		resetLoggerForTest()
	}()

	err := errors.New("connection reset by peer")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		Error("Request failed", err)
	}
	Close()
}
