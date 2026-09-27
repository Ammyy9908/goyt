package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/ammyy9908/goyt"
)

func TestRootHelpAndVersion(t *testing.T) {
	versionFlags := []string{"-version", "--version", "version"}
	for _, arg := range versionFlags {
		t.Run("version_"+arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{arg}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.TrimSpace(stdout.String()); got != goyt.Version {
				t.Fatalf("version = %q; want %q", got, goyt.Version)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got: %q", stderr.String())
			}
		})
	}

	helpFlags := []string{"-help", "--help", "-h", "help"}
	for _, arg := range helpFlags {
		t.Run("help_"+arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{arg}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("expected usage in stdout, got: %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "download") ||
				!strings.Contains(stdout.String(), "inspect") ||
				!strings.Contains(stdout.String(), "hls") {
				t.Fatalf("expected subcommands in help output, got: %q", stdout.String())
			}
		})
	}

	t.Run("empty args", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on empty args, got nil")
		}
		if !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("expected usage in stderr, got: %q", stderr.String())
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"foobar"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on unknown command, got nil")
		}
		if !strings.Contains(err.Error(), "unknown command \"foobar\"") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if !strings.Contains(stderr.String(), "unknown command") {
			t.Fatalf("expected unknown command in stderr, got: %q", stderr.String())
		}
	})
}

func TestSubcommandHelp(t *testing.T) {
	subcommands := []struct {
		name         string
		expectedText string
	}{
		{"download", "Usage: goyt download -url URL"},
		{"inspect", "Usage: goyt inspect -url YOUTUBE_URL"},
		{"hls", "Usage: goyt hls -url PLAYLIST_URL"},
	}

	for _, sc := range subcommands {
		t.Run(sc.name+"_help", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{sc.name, "-help"}, &stdout, &stderr)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("expected ErrHelp for %s -help, got: %v", sc.name, err)
			}
			if !strings.Contains(stderr.String(), sc.expectedText) {
				t.Fatalf("expected %q in stderr, got: %q", sc.expectedText, stderr.String())
			}
		})
	}
}

func TestDownloadFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty download args",
			args:    []string{"download"},
			wantErr: "usage:",
		},
		{
			name:    "missing url",
			args:    []string{"download", "-transport", "http"},
			wantErr: "usage:",
		},
		{
			name:    "unexpected positional arg",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "extra"},
			wantErr: "usage:",
		},
		{
			name:    "invalid height",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-height", "0"},
			wantErr: "height must be positive",
		},
		{
			name:    "negative height",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-height", "-10"},
			wantErr: "height must be positive",
		},
		{
			name:    "invalid transport",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "ftp"},
			wantErr: "transport must be http or hls",
		},
		{
			name:    "audio language with http transport",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "http", "-audio-language", "en"},
			wantErr: "-audio-language currently requires -transport hls",
		},
		{
			name:    "non mp4 output extension",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-out", "video.mkv"},
			wantErr: "this command requires an .mp4 output",
		},
		{
			name:    "unsupported youtube url",
			args:    []string{"download", "-url", "https://example.com/video"},
			wantErr: "unsupported YouTube URL",
		},
		{
			name:    "unsupported music playlist url",
			args:    []string{"download", "-url", "https://music.youtube.com/playlist?list=PL1234567890"},
			wantErr: "unsupported YouTube URL",
		},
		{
			name:    "unsupported lookalike music host",
			args:    []string{"download", "-url", "https://music.youtube.com.example.org/watch?v=12345678901"},
			wantErr: "unsupported YouTube URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestInspectFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty inspect args",
			args:    []string{"inspect"},
			wantErr: "usage:",
		},
		{
			name:    "missing url",
			args:    []string{"inspect", "-client", "web"},
			wantErr: "usage:",
		},
		{
			name:    "invalid client",
			args:    []string{"inspect", "-url", "https://youtube.com/watch?v=123", "-client", "android"},
			wantErr: "client must be web, visionos, or all",
		},
		{
			name:    "unsupported youtube url",
			args:    []string{"inspect", "-url", "https://example.com/video"},
			wantErr: "unsupported YouTube video URL",
		},
		{
			name:    "unsupported music playlist url in inspect",
			args:    []string{"inspect", "-url", "https://music.youtube.com/playlist?list=PL1234567890"},
			wantErr: "unsupported YouTube video URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestHLSFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty hls args",
			args:    []string{"hls"},
			wantErr: "usage:",
		},
		{
			name:    "invalid height",
			args:    []string{"hls", "-url", "https://example.com/live.m3u8", "-height", "0"},
			wantErr: "usage:",
		},
		{
			name:    "non mp4 output extension",
			args:    []string{"hls", "-url", "https://example.com/live.m3u8", "-out", "video.ts"},
			wantErr: "this command requires an .mp4 output",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLegacyShorthandRouting(t *testing.T) {
	t.Run("legacy flag routes to download", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// Passing -url directly to root without "download" subcommand
		err := Run(context.Background(), []string{"-url", "https://example.com/video"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Should fail with download's validation error (unsupported YouTube URL)
		if !strings.Contains(err.Error(), "unsupported YouTube URL") {
			t.Fatalf("expected unsupported YouTube URL error, got: %v", err)
		}
	})

	t.Run("legacy invalid flags", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"-url", "https://youtube.com/watch?v=123", "-transport", "bad"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "transport must be http or hls") {
			t.Fatalf("expected transport error, got: %v", err)
		}
	})

	t.Run("legacy shorthand with audio-only", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"-url", "https://example.com/video", "-audio-only"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Should reach extractor validation for download command
		if !strings.Contains(err.Error(), "unsupported YouTube URL") {
			t.Fatalf("expected unsupported YouTube URL error, got: %v", err)
		}
	})
}

func TestAudioOnlyCLIFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "audio-format without audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-format", "mp3"},
			wantErr: "-audio-format, -audio-quality, and -audio-bitrate require -audio-only",
		},
		{
			name:    "audio-quality without audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-quality", "2"},
			wantErr: "-audio-format, -audio-quality, and -audio-bitrate require -audio-only",
		},
		{
			name:    "audio-bitrate without audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-bitrate", "128k"},
			wantErr: "-audio-format, -audio-quality, and -audio-bitrate require -audio-only",
		},
		{
			name:    "quality and bitrate mutually exclusive",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-quality", "2", "-audio-bitrate", "128k"},
			wantErr: "explicit -audio-quality and -audio-bitrate are mutually exclusive",
		},
		{
			name:    "explicit height with audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-height", "720"},
			wantErr: "-height is not supported in -audio-only mode",
		},
		{
			name:    "unsupported audio format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "wma"},
			wantErr: "unsupported audio format",
		},
		{
			name:    "audio quality for non-mp3 format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "aac", "-audio-quality", "2"},
			wantErr: "-audio-quality is only supported for mp3 format",
		},
		{
			name:    "audio bitrate for flac format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "flac", "-audio-bitrate", "128k"},
			wantErr: "-audio-bitrate is not supported for flac format",
		},
		{
			name:    "audio bitrate for wav format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "wav", "-audio-bitrate", "128k"},
			wantErr: "-audio-bitrate is not supported for wav format",
		},
		{
			name:    "audio bitrate for alac format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "alac", "-audio-bitrate", "128k"},
			wantErr: "-audio-bitrate is not supported for alac format",
		},
		{
			name:    "negative audio quality",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "mp3", "-audio-quality", "-1"},
			wantErr: "audio quality must be between 0 and 9",
		},
		{
			name:    "too high audio quality",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "mp3", "-audio-quality", "10"},
			wantErr: "audio quality must be between 0 and 9",
		},
		{
			name:    "invalid audio bitrate",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "mp3", "-audio-bitrate", "invalid"},
			wantErr: "invalid audio bitrate",
		},
		{
			name:    "non-flac output for flac mode",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "flac", "-out", "song.mp3"},
			wantErr: "audio-only flac mode requires a .flac output",
		},
		{
			name:    "non-m4a output for alac mode",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "alac", "-out", "song.alac"},
			wantErr: "audio-only alac mode requires a .m4a output",
		},
		{
			name:    "non-ogg output for vorbis mode",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "vorbis", "-out", "song.mp3"},
			wantErr: "audio-only vorbis mode requires a .ogg output",
		},
		{
			name:    "default height with audio-only is accepted up to URL validation",
			args:    []string{"download", "-url", "https://example.com/video", "-audio-only"},
			wantErr: "unsupported YouTube URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
