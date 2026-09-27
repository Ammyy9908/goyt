package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveExecutablePath returns a reliable absolute or relative path to the current executable.
// It prioritizes os.Executable(), resolving symlinks where possible, and falls back to os.Args[0] or "goyt".
func ResolveExecutablePath() string {
	if execPath, err := os.Executable(); err == nil && execPath != "" {
		if eval, err := filepath.EvalSymlinks(execPath); err == nil && eval != "" {
			return eval
		}
		return filepath.Clean(execPath)
	}
	if len(os.Args) > 0 && os.Args[0] != "" {
		if abs, err := filepath.Abs(os.Args[0]); err == nil && abs != "" {
			return abs
		}
		return os.Args[0]
	}
	return "goyt"
}

// QuotePOSIXArg quotes a string for safe usage in POSIX shells (sh, bash, zsh, dash, ksh).
func QuotePOSIXArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if isPOSIXSafe(arg) {
		return arg
	}
	// In POSIX shells, enclosing in single quotes preserves everything literally.
	// The only character that cannot appear inside '...' is ' itself, which is escaped via '\''
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func isPOSIXSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == '/' || r == '-' || r == ':' || r == '=' || r == '+' || r == '@':
		default:
			return false
		}
	}
	return true
}

// QuoteWindowsArg quotes a string for safe usage in Windows cmd / CommandLineToArgvW.
func QuoteWindowsArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if isWindowsSafe(arg) {
		return arg
	}

	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		if r == '\\' {
			backslashes++
		} else if r == '"' {
			// Preceding backslashes must be doubled, plus one for the double quote itself
			for i := 0; i < backslashes*2+1; i++ {
				b.WriteByte('\\')
			}
			b.WriteByte('"')
			backslashes = 0
		} else {
			for i := 0; i < backslashes; i++ {
				b.WriteByte('\\')
			}
			backslashes = 0
			b.WriteRune(r)
		}
	}
	for i := 0; i < backslashes*2; i++ {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}

func isWindowsSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == '-' || r == '\\' || r == '/' || r == ':':
		default:
			return false
		}
	}
	return true
}

// QuoteShellArg quotes an argument according to the current host runtime OS.
func QuoteShellArg(arg string) string {
	if runtime.GOOS == "windows" {
		return QuoteWindowsArg(arg)
	}
	return QuotePOSIXArg(arg)
}

// FormatResumeCommand formats a directly runnable CLI command to resume a persistent job.
func FormatResumeCommand(execPath, absJobDir string) string {
	quotedExec := QuoteShellArg(execPath)
	quotedDir := QuoteShellArg(absJobDir)
	return fmt.Sprintf("%s download -resume-job %s", quotedExec, quotedDir)
}
