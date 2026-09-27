package goyt

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ffmpegFixture(t *testing.T, directory, name string) string {
	t.Helper()

	path := filepath.Join(directory, name)

	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestFFmpegMerge(t *testing.T) {
	directory := t.TempDir()
	video := ffmpegFixture(t, directory, "video.mp4")
	audio := ffmpegFixture(t, directory, "audio.m4a")
	output := filepath.Join(directory, "merged.mp4")

	ffmpeg := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			expected := []string{
				"-hide_banner",
				"-loglevel", "error",
				"-nostdin",
				"-y",
				"-i", video,
				"-i", audio,
				"-map", "0:v:0",
				"-map", "1:a:0",
				"-c", "copy",
				"-f", "mp4",
			}

			if !reflect.DeepEqual(args[:len(args)-1], expected) {
				t.Fatalf("unexpected arguments: %v", args)
			}

			tempOutput := args[len(args)-1]
			if tempOutput == output {
				t.Fatal("FFmpeg must write to a temporary file")
			}

			return os.WriteFile(tempOutput, []byte("merged"), 0600)
		},
	}

	if err := ffmpeg.Merge(
		context.Background(),
		video,
		audio,
		output,
	); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(output)
	if err != nil || string(data) != "merged" {
		t.Fatalf("unexpected output: %q, error: %v", data, err)
	}

	for _, input := range []string{video, audio} {
		data, err := os.ReadFile(input)
		if err != nil || string(data) != "fixture" {
			t.Fatal("input file was changed")
		}
	}
}

func TestFFmpegRemux(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")
	output := filepath.Join(directory, "output.mkv")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			expected := []string{
				"-hide_banner",
				"-loglevel", "error",
				"-nostdin",
				"-y",
				"-i", input,
				"-map", "0:v:0",
				"-map", "0:a:0",
				"-c", "copy",
				"-f", "matroska",
			}

			if !reflect.DeepEqual(args[:len(args)-1], expected) {
				t.Fatalf("unexpected remux arguments: %v", args)
			}

			return os.WriteFile(
				args[len(args)-1],
				[]byte("remuxed"),
				0600,
			)
		},
	}

	if err := ffmpeg.Remux(
		context.Background(),
		input,
		output,
	); err != nil {
		t.Fatal(err)
	}
}

func TestFFmpegFailurePreservesDestination(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")
	output := ffmpegFixture(t, directory, "output.mkv")
	cause := errors.New("process failed")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			io.WriteString(stderr, "codec incompatible with container")
			return cause
		},
	}

	err := ffmpeg.Remux(context.Background(), input, output)

	var processErr *FFmpegError
	if !errors.As(err, &processErr) {
		t.Fatalf("got %v, want FFmpegError", err)
	}

	if !errors.Is(err, cause) {
		t.Fatal("underlying process error was lost")
	}

	if !strings.Contains(processErr.Details, "codec incompatible") {
		t.Fatal("missing FFmpeg diagnostics")
	}

	data, err := os.ReadFile(output)
	if err != nil || string(data) != "fixture" {
		t.Fatal("failed processing changed the destination")
	}

	leftovers, err := filepath.Glob(
		filepath.Join(directory, ".goyt-ffmpeg-*"),
	)
	if err != nil || len(leftovers) != 0 {
		t.Fatal("temporary output was not cleaned up")
	}
}

func TestFFmpegCancellation(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")
	output := filepath.Join(directory, "output.mkv")

	ctx, cancel := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	defer cancel()

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
	}

	err := ffmpeg.Remux(ctx, input, output)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}

	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation should not create the destination")
	}
}

func TestFFmpegRejectsSameInputAndOutput(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			context.Context,
			string,
			[]string,
			io.Writer,
		) error {
			t.Fatal("invalid paths should not reach FFmpeg")
			return nil
		},
	}

	if err := ffmpeg.Remux(
		context.Background(),
		input,
		input,
	); err == nil {
		t.Fatal("same input and output should be rejected")
	}
}

func TestFFmpegRejectsEmptyOutput(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			context.Context,
			string,
			[]string,
			io.Writer,
		) error {
			// Simulate a process that succeeds without writing anything.
			return nil
		},
	}

	err := ffmpeg.Remux(
		context.Background(),
		input,
		filepath.Join(directory, "output.mkv"),
	)

	if err == nil {
		t.Fatal("empty output should be rejected")
	}
}

func TestFFmpegMissingExecutable(t *testing.T) {
	_, err := NewFFmpeg(
		filepath.Join(t.TempDir(), "missing-ffmpeg"),
	)

	if !errors.Is(err, ErrFFmpegNotFound) {
		t.Fatalf("got %v, want ErrFFmpegNotFound", err)
	}
}

func TestFFmpegDiagnosticLimit(t *testing.T) {
	tail := &diagnosticTail{limit: 8}

	tail.Write([]byte("123456"))
	tail.Write([]byte("7890"))

	if string(tail.data) != "34567890" {
		t.Fatalf("unexpected diagnostic tail: %q", tail.data)
	}

	tail.Write([]byte("abcdefghijkl"))

	if string(tail.data) != "efghijkl" {
		t.Fatalf("unexpected diagnostic tail: %q", tail.data)
	}
}
