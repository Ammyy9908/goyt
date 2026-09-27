package goyt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutorIntegration(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("integration test requires ffmpeg")
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("integration test requires ffprobe")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		60*time.Second,
	)
	defer cancel()

	run := func(program string, args ...string) []byte {
		t.Helper()

		output, err := exec.CommandContext(
			ctx,
			program,
			args...,
		).CombinedOutput()

		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, output)
		}

		return output
	}

	sourceDir := t.TempDir()
	outputDir := t.TempDir()

	videoPath := filepath.Join(sourceDir, "video.mp4")
	audioPath := filepath.Join(sourceDir, "audio.m4a")

	// Generate real video-only and audio-only inputs.
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
		videoPath,
	)

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
		audioPath,
	)

	// Serve the generated files over local HTTP.
	server := httptest.NewServer(
		http.FileServer(http.Dir(sourceDir)),
	)
	defer server.Close()

	height := 120
	media := &Media{
		ID:    "integration-demo",
		Title: "Integration demo",
		Formats: []Format{
			{
				ID:         "video",
				Protocol:   ProtocolHTTP,
				Container:  "mp4",
				VideoCodec: "h264",
				AudioCodec: "none",
				Height:     &height,
				Resource: Resource{
					URL: server.URL + "/video.mp4",
				},
			},
			{
				ID:         "audio",
				Protocol:   ProtocolHTTP,
				Container:  "m4a",
				VideoCodec: "none",
				AudioCodec: "aac",
				Language:   "en",
				Resource: Resource{
					URL: server.URL + "/audio.m4a",
				},
			},
		},
	}

	plan, err := Plan(media, Selection{
		Container:     "mp4",
		AudioLanguage: "en",
		AllowSeparate: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !plan.NeedsMerge {
		t.Fatal("expected a merge plan")
	}

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		processor,
	)
	if err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(outputDir, "output.mp4")

	result, err := executor.Execute(
		ctx,
		plan,
		outputPath,
		ExecuteOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.SizeBytes <= 0 {
		t.Fatal("output is empty")
	}

	if result.CleanupError != nil {
		t.Fatal(result.CleanupError)
	}

	// Verify the merged output contains the expected streams.
	probeJSON := run(
		ffprobePath,
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name",
		"-of", "json",
		result.Path,
	)

	var probe struct {
		Streams []struct {
			Type  string `json:"codec_type"`
			Codec string `json:"codec_name"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(probeJSON, &probe); err != nil {
		t.Fatal(err)
	}

	var videoCount, audioCount int

	for _, stream := range probe.Streams {
		switch stream.Type {
		case "video":
			videoCount++
			if stream.Codec != "h264" {
				t.Fatalf("unexpected video codec: %s", stream.Codec)
			}

		case "audio":
			audioCount++
			if stream.Codec != "aac" {
				t.Fatalf("unexpected audio codec: %s", stream.Codec)
			}
		}
	}

	if len(probe.Streams) != 2 ||
		videoCount != 1 ||
		audioCount != 1 {
		t.Fatalf("unexpected streams: %+v", probe.Streams)
	}

	// Decode both streams to catch media errors.
	run(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-xerror",
		"-i", result.Path,
		"-map", "0:v:0",
		"-map", "0:a:0",
		"-f", "null",
		"-",
	)

	leftovers, err := filepath.Glob(
		filepath.Join(outputDir, ".goyt-work-*"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(leftovers) != 0 {
		t.Fatalf("intermediate directories remain: %v", leftovers)
	}
}
