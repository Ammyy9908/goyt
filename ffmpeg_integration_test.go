//go:build integration

package goyt

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFFmpegIntegration(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("integration test requires ffmpeg in PATH")
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("integration test requires ffprobe in PATH")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		60*time.Second,
	)
	defer cancel()

	directory := t.TempDir()
	video := filepath.Join(directory, "video.mp4")
	audio := filepath.Join(directory, "audio.m4a")
	merged := filepath.Join(directory, "merged.mp4")
	remuxed := filepath.Join(directory, "remuxed.mkv")

	run := func(program string, args ...string) []byte {
		t.Helper()

		output, err := exec.CommandContext(
			ctx,
			program,
			args...,
		).CombinedOutput()

		if err != nil {
			t.Fatalf(
				"command failed: %s\nerror: %v\noutput: %s",
				program,
				err,
				output,
			)
		}

		return output
	}

	// Generate one second of silent H.264 video.
	run(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
		"-f", "lavfi",
		"-i", "color=c=blue:s=160x120:r=25",
		"-t", "1",
		"-an",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		video,
	)

	// Generate one second of AAC audio.
	run(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=48000",
		"-t", "1",
		"-vn",
		"-c:a", "aac",
		audio,
	)

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}

	verify := func(path string) {
		t.Helper()

		output := run(
			ffprobePath,
			"-v", "error",
			"-show_entries", "stream=codec_type,codec_name",
			"-of", "json",
			path,
		)

		var probe struct {
			Streams []struct {
				CodecType string `json:"codec_type"`
				CodecName string `json:"codec_name"`
			} `json:"streams"`
		}

		if err := json.Unmarshal(output, &probe); err != nil {
			t.Fatalf("invalid ffprobe JSON: %v", err)
		}

		var videoCount, audioCount int

		for _, stream := range probe.Streams {
			switch stream.CodecType {
			case "video":
				videoCount++
				if stream.CodecName != "h264" {
					t.Fatalf("unexpected video codec: %s", stream.CodecName)
				}

			case "audio":
				audioCount++
				if stream.CodecName != "aac" {
					t.Fatalf("unexpected audio codec: %s", stream.CodecName)
				}
			}
		}

		if len(probe.Streams) != 2 ||
			videoCount != 1 ||
			audioCount != 1 {
			t.Fatalf(
				"expected one video and one audio stream: %+v",
				probe.Streams,
			)
		}

		// Decode both streams to check for media errors.
		run(
			ffmpegPath,
			"-hide_banner",
			"-loglevel", "error",
			"-nostdin",
			"-xerror",
			"-i", path,
			"-map", "0:v:0",
			"-map", "0:a:0",
			"-f", "null",
			"-",
		)
	}

	if err := processor.Merge(ctx, video, audio, merged); err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	verify(merged)

	verifier, err := NewVerifier(ffprobePath)
	if err != nil {
		t.Fatal(err)
	}

	expected := time.Second

	if _, err := verifier.VerifyMP4(ctx, merged, &expected); err != nil {
		t.Fatal(err)
	}

	if err := processor.CheckDecode(ctx, merged); err != nil {
		t.Fatal(err)
	}

	if err := processor.Remux(ctx, merged, remuxed); err != nil {
		t.Fatalf("remux failed: %v", err)
	}
	verify(remuxed)
}
