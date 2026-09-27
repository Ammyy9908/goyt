package goyt

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalHLSFFmpegArguments(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "master.m3u8")
	output := filepath.Join(dir, "output.mp4")

	if err := os.WriteFile(input, []byte("#EXTM3U\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var captured []string
	processor := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(
			_ context.Context,
			_ string,
			args []string,
			_ io.Writer,
		) error {
			captured = append([]string(nil), args...)
			return os.WriteFile(args[len(args)-1], []byte("output"), 0600)
		},
	}

	err := processor.process(
		context.Background(),
		"local-hls",
		[]string{input},
		[]string{"-map", "0:v:0", "-map", "0:a:0"},
		output,
	)
	if err != nil {
		t.Fatal(err)
	}

	hasPair := func(key, value string) bool {
		for i := 0; i+1 < len(captured); i++ {
			if captured[i] == key && captured[i+1] == value {
				return true
			}
		}
		return false
	}

	for _, pair := range [][2]string{
		{"-protocol_whitelist", "file"},
		{"-allowed_extensions", "m3u8,ts,aac"},
		{"-f", "hls"},
		{"-map", "0:v:0"},
		{"-map", "0:a:0"},
		{"-c", "copy"},
	} {
		if !hasPair(pair[0], pair[1]) {
			t.Fatalf("missing %v in arguments: %v", pair, captured)
		}
	}

	inputCount := 0
	copyTS, startAtZero := false, false
	for _, arg := range captured {
		switch arg {
		case "-i":
			inputCount++
		case "-copyts":
			copyTS = true
		case "-start_at_zero":
			startAtZero = true
		}
	}

	if inputCount != 1 || !copyTS || !startAtZero {
		t.Fatalf("unexpected timestamp/input arguments: %v", captured)
	}
}

func TestLocalHLSFFmpegFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "master.m3u8")
	output := filepath.Join(dir, "output.mp4")

	if err := os.WriteFile(input, []byte("#EXTM3U\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("processing failed")
	processor := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(
			_ context.Context,
			_ string,
			_ []string,
			_ io.Writer,
		) error {
			return failure
		},
	}

	err := processor.process(
		context.Background(),
		"local-hls",
		[]string{input},
		[]string{"-map", "0:v:0", "-map", "0:a:0"},
		output,
	)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v; want processing failure", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatal("failed processing replaced the destination")
	}
}
