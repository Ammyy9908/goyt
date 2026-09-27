package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/ammyy9908/goyt"
)

func TestCLIHelpAndVersion(t *testing.T) {
	t.Run("version flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), []string{"-version"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != goyt.Version {
			t.Fatalf("version = %q; want %q", got, goyt.Version)
		}
	})

	t.Run("help flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), []string{"-help"}, &stdout, &stderr)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("expected ErrHelp, got: %v", err)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("expected usage in stderr, got: %q", stderr.String())
		}
	})
}

func TestCLIFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty args",
			args:    []string{},
			wantErr: "usage:",
		},
		{
			name:    "missing url",
			args:    []string{"-transport", "http"},
			wantErr: "usage:",
		},
		{
			name:    "unexpected positional arg",
			args:    []string{"-url", "https://youtube.com/watch?v=123", "extra"},
			wantErr: "usage:",
		},
		{
			name:    "invalid height",
			args:    []string{"-url", "https://youtube.com/watch?v=123", "-height", "0"},
			wantErr: "height must be positive",
		},
		{
			name:    "negative height",
			args:    []string{"-url", "https://youtube.com/watch?v=123", "-height", "-10"},
			wantErr: "height must be positive",
		},
		{
			name:    "invalid transport",
			args:    []string{"-url", "https://youtube.com/watch?v=123", "-transport", "ftp"},
			wantErr: "transport must be http or hls",
		},
		{
			name:    "non mp4 output extension",
			args:    []string{"-url", "https://youtube.com/watch?v=123", "-out", "video.mkv"},
			wantErr: "this command requires an .mp4 output",
		},
		{
			name:    "unsupported youtube url",
			args:    []string{"-url", "https://example.com/video"},
			wantErr: "unsupported YouTube URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
