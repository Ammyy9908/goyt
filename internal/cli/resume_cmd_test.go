package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuotePOSIXArg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "''"},
		{"goyt", "goyt"},
		{"/usr/local/bin/goyt", "/usr/local/bin/goyt"},
		{"./bin/goyt", "./bin/goyt"},
		{"my-job_1.0:test", "my-job_1.0:test"},
		{"/path with/spaces/job", "'/path with/spaces/job'"},
		{"/path/with'quote/job", `'/path/with'\''quote/job'`},
		{"/path/with\"double/job", `'/path/with"double/job'`},
		{"/path/with$var/job", `'/path/with$var/job'`},
		{"/path/with\\backslash/job", `'/path/with\backslash/job'`},
		{"job;rm -rf /", `'job;rm -rf /'`},
	}

	for _, tt := range tests {
		got := QuotePOSIXArg(tt.input)
		if got != tt.want {
			t.Errorf("QuotePOSIXArg(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestQuoteWindowsArg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", `""`},
		{"goyt.exe", "goyt.exe"},
		{`C:\bin\goyt.exe`, `C:\bin\goyt.exe`},
		{`C:\Program Files\goyt\goyt.exe`, `"C:\Program Files\goyt\goyt.exe"`},
		{`C:\job\with"quote`, `"C:\job\with\"quote"`},
		{`C:\job\with space\`, `"C:\job\with space\\"`},
		{`C:\job\with space\"quote`, `"C:\job\with space\\\"quote"`},
	}

	for _, tt := range tests {
		got := QuoteWindowsArg(tt.input)
		if got != tt.want {
			t.Errorf("QuoteWindowsArg(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatResumeCommand(t *testing.T) {
	t.Run("Clean paths", func(t *testing.T) {
		cmd := FormatResumeCommand("/usr/local/bin/goyt", "/tmp/job-1")
		if cmd != "/usr/local/bin/goyt download -resume-job /tmp/job-1" {
			t.Fatalf("unexpected formatted command: %s", cmd)
		}
	})

	t.Run("Paths with spaces and quotes", func(t *testing.T) {
		cmd := FormatResumeCommand("/Applications/My Tools/goyt", "/Users/John's Files/job 1")
		if !strings.Contains(cmd, "download -resume-job") {
			t.Fatalf("missing subcommand in: %s", cmd)
		}
		if !strings.Contains(cmd, "My Tools") || !strings.Contains(cmd, "John") {
			t.Fatalf("missing path components in: %s", cmd)
		}
	})
}

func TestResolveExecutablePath(t *testing.T) {
	p := ResolveExecutablePath()
	if p == "" {
		t.Fatal("expected non-empty executable path")
	}
	// Verify that the path points to something executable or is a valid non-empty string
	if !filepath.IsAbs(p) && p != "goyt" && p != os.Args[0] {
		t.Fatalf("expected absolute path or fallback, got: %s", p)
	}
}
