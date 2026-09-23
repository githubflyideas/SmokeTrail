//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Registering a service needs Administrator. The manifest asks for asInvoker,
// because the console and the portable copy must never raise a UAC prompt — they
// do not need privilege, and a tool that prompts on every ordinary run teaches
// people to click through prompts.
//
// So `install` and `uninstall` elevate themselves. Typing the command in a normal
// prompt produces a consent dialog, which is what a Windows administrator expects,
// rather than an error telling them to go and open a different window.
//
// The elevated child gets its own console, which closes the instant it finishes,
// so its output would otherwise be lost. It is therefore handed a log file and the
// waiting parent prints what it wrote. From the operator's side the command simply
// works in the window they typed it in.

const doneMarker = "\x00smoketrail-done:"

func isElevated() bool {
	tok := windows.Token(0) // pseudo-handle for the current process token
	return tok.IsElevated()
}

// elevateSelf re-launches this executable with the same arguments under the runas
// verb and waits for the result. It returns the child's exit code.
func elevateSelf(verb string, args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	exe, _ = filepath.Abs(exe)

	f, err := os.CreateTemp("", "smoketrail-elevated-*.log")
	if err != nil {
		return 1, err
	}
	logPath := f.Name()
	f.Close()
	defer os.Remove(logPath)

	full := append([]string{verb}, args...)
	full = append(full, "--log-file", logPath)

	fmt.Fprintf(os.Stderr, "%s needs Administrator — accept the prompt to continue.\n", verb)
	if err := windows.ShellExecute(0,
		windows.StringToUTF16Ptr("runas"),
		windows.StringToUTF16Ptr(exe),
		windows.StringToUTF16Ptr(quoteArgs(full)),
		nil, windows.SW_HIDE); err != nil {
		if err == windows.ERROR_CANCELLED {
			return 1, fmt.Errorf("the elevation prompt was declined")
		}
		return 1, fmt.Errorf("could not elevate: %w", err)
	}
	return tailUntilDone(logPath, 3*time.Minute)
}

// tailUntilDone streams the elevated child's log to our own stderr until it writes
// the terminator, so the operator sees the same output they would have seen had
// they started an elevated prompt themselves.
func tailUntilDone(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	var off int64
	for time.Now().Before(deadline) {
		f, err := os.Open(path)
		if err == nil {
			f.Seek(off, io.SeekStart)
			b, _ := io.ReadAll(f)
			off += int64(len(b))
			f.Close()
			text := string(b)
			if i := strings.Index(text, doneMarker); i >= 0 {
				os.Stderr.WriteString(text[:i])
				code := 0
				fmt.Sscanf(text[i+len(doneMarker):], "%d", &code)
				return code, nil
			}
			os.Stderr.WriteString(text)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 1, fmt.Errorf("the elevated step did not finish within %v; check the Application event log", timeout)
}

// quoteArgs builds a command line the CRT will split back into the same argv.
func quoteArgs(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if a == "" || strings.ContainsAny(a, " \t\"") {
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(a, `"`, `\"`))
			b.WriteByte('"')
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}

// redirectToLogFile points log output at the file the parent is tailing, and
// returns a closer that writes the terminator with the final exit code.
func redirectToLogFile(path string) func(code int) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return func(int) {}
	}
	setLogOutput(f)
	return func(code int) {
		fmt.Fprintf(f, "%s%d", doneMarker, code)
		f.Close()
	}
}
