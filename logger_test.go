package lumo

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// captureLog manipulates the GLOBAL logger state to capture output.
func captureLog(f func()) string {
	Close()
	var buf bytes.Buffer
	ChangeOutput(&buf)

	l.mu.Lock()
	l.stackOnWarn = false
	l.mu.Unlock()

	f()

	Close()
	ChangeOutput(os.Stdout)
	return buf.String()
}

func TestConfigure(t *testing.T) {
	l.minLevel = levelInfo
	EnableDebug()
	if l.minLevel != levelDebug {
		t.Error("Failed to set Debug level")
	}
}

func TestLogFormatting(t *testing.T) {
	ForceColors(false)
	out := captureLog(func() {
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
	ForceColors(false)
	out := captureLog(func() {
		err := WrapString("validation failed")
		err.Include("request_id", 12345)
		Error("Handler crash: %v", err)
	})

	if !strings.Contains(out, "included context:") {
		t.Error("Missing context header")
	}
	if !strings.Contains(out, "request_id: 12345") {
		t.Error("Missing integer context value")
	}
}

func TestStackParsing(t *testing.T) {
	ForceColors(false)
	out := captureLog(func() {
		func() {
			err := WrapString("deep error")
			Error("Boom: %v", err)
		}()
	})
	if !strings.Contains(out, "TestStackParsing") {
		t.Error("Stack trace missing current function name")
	}
}

func TestWarnStackToggle(t *testing.T) {
	ForceColors(false)

	// 1. Default: No stack on Warn
	out1 := captureLog(func() {
		Warn("Simple warning")
	})
	if strings.Contains(out1, "   at ") {
		t.Error("Warn should not have stack trace by default")
	}

	// 2. Enabled: Stack on Warn
	out2 := captureLog(func() {
		EnableStackOnWarns()
		Warn("Complex warning")
	})
	if !strings.Contains(out2, "   at ") {
		t.Error("Warn should have stack trace when enabled")
	}
}

// TestPanicExit checks if Panic() actually terminates the process
func TestPanicExit(t *testing.T) {
	// 1. Check if we are the subprocess being spawned
	if os.Getenv("LUMO_TEST_CRASH") == "1" {
		ForceColors(false)
		Panic("Critical Failure")
		return
	}

	// 2. Spawn the subprocess to run this test function again
	cmd := exec.Command(os.Args[0], "-test.run=TestPanicExit")
	cmd.Env = append(os.Environ(), "LUMO_TEST_CRASH=1")

	// Run and wait for it to crash
	err := cmd.Run()

	// 3. Check if it exited with an error (which means os.Exit(1) worked)
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // PASS: It crashed as expected
	}

	t.Fatalf("Process ran with err %v, expected exit status 1", err)
}
