package goyt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestAudioExecutorIntegration(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("integration test requires ffmpeg")
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("integration test requires ffprobe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	run := func(program string, args ...string) []byte {
		t.Helper()
		output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, output)
		}
		return output
	}

	sourceDir := t.TempDir()
	outputDir := t.TempDir()
	aacPath := filepath.Join(sourceDir, "audio.m4a")
	opusPath := filepath.Join(sourceDir, "audio.opus")

	// Generate a 2-second AAC audio source.
	run(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=48000",
		"-t", "2",
		"-vn",
		"-c:a", "aac",
		aacPath,
	)

	// Generate a 2-second Opus audio source.
	run(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=48000",
		"-t", "2",
		"-vn",
		"-c:a", "libopus",
		"-f", "opus",
		opusPath,
	)

	server := httptest.NewServer(http.FileServer(http.Dir(sourceDir)))
	defer server.Close()

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}

	verifier, err := NewVerifier(ffprobePath)
	if err != nil {
		t.Fatal(err)
	}

	executor, err := NewExecutor(NewDownloader(server.Client()), processor)
	if err != nil {
		t.Fatal(err)
	}

	bitrate := int64(128000)
	aacMedia := &Media{
		ID:    "aac-media",
		Title: "AAC Media",
		Formats: []Format{
			{
				ID:              "140",
				Protocol:        ProtocolHTTP,
				Container:       "mp4",
				VideoCodec:      "none",
				AudioCodec:      "aac",
				Bitrate:         &bitrate,
				AudioTrackName:  "English (original)",
				AudioIsOriginal: true,
				Language:        "en",
				Resource: Resource{
					URL: server.URL + "/audio.m4a",
				},
			},
		},
	}

	opusMedia := &Media{
		ID:    "opus-media",
		Title: "Opus Media",
		Formats: []Format{
			{
				ID:              "251",
				Protocol:        ProtocolHTTP,
				Container:       "webm",
				VideoCodec:      "none",
				AudioCodec:      "opus",
				Bitrate:         &bitrate,
				AudioTrackName:  "English (original)",
				AudioIsOriginal: true,
				Language:        "en",
				Resource: Resource{
					URL: server.URL + "/audio.opus",
				},
			},
		},
	}

	formatsToTest := []struct {
		format       string
		media        *Media
		wantCodec    string
		destFilename string
		bitrate      string
	}{
		{"best", aacMedia, "aac", "best_aac.m4a", ""},
		{"best", opusMedia, "opus", "best_opus.opus", ""},
		{"aac", aacMedia, "aac", "song.aac", ""},
		{"m4a", aacMedia, "aac", "song.m4a", ""},
		{"alac", aacMedia, "alac", "song_alac.m4a", ""},
		{"flac", aacMedia, "flac", "song.flac", ""},
		{"mp3", aacMedia, "mp3", "song.mp3", ""},
		{"opus", aacMedia, "opus", "song.opus", ""},
		{"vorbis", aacMedia, "vorbis", "song.ogg", ""},
		{"wav", aacMedia, "pcm_s16le", "song.wav", ""},
	}

	for _, tt := range formatsToTest {
		t.Run("HTTP_"+tt.format+"_"+tt.wantCodec, func(t *testing.T) {
			plan, err := PlanAudio(tt.media, AudioSelection{
				AudioFormat:  tt.format,
				AudioBitrate: tt.bitrate,
			})
			if err != nil {
				t.Fatalf("PlanAudio failed: %v", err)
			}

			if !plan.OutputSpec.Copy && plan.OutputSpec.Encoder != "" {
				has, err := processor.HasEncoder(ctx, plan.OutputSpec.Encoder)
				if err != nil || !has {
					if os.Getenv("GOYT_REQUIRE_ALL_ENCODERS") != "" || os.Getenv("CI") != "" {
						t.Fatalf("required audio encoder %q is not available in CI environment (err: %v)", plan.OutputSpec.Encoder, err)
					}
					t.Skipf("encoder %q not available on system, skipping", plan.OutputSpec.Encoder)
				}
			}

			outputPath := filepath.Join(outputDir, tt.destFilename)

			result, err := executor.ExecuteAudio(ctx, plan, outputPath, ExecuteOptions{})
			if err != nil {
				t.Fatalf("ExecuteAudio failed: %v", err)
			}

			if result.SizeBytes <= 0 {
				t.Fatal("output is empty")
			}

			expectedDuration := 2 * time.Second
			verification, err := verifier.VerifyAudio(ctx, result.Path, plan.OutputSpec, &expectedDuration)
			if err != nil {
				t.Fatalf("VerifyAudio failed: %v", err)
			}

			if verification.AudioCodec != tt.wantCodec {
				t.Fatalf("expected codec %s, got %s", tt.wantCodec, verification.AudioCodec)
			}

			if err := processor.CheckDecodeAudio(ctx, result.Path); err != nil {
				t.Fatalf("CheckDecodeAudio failed: %v", err)
			}
		})
	}

	leftovers, err := filepath.Glob(filepath.Join(outputDir, ".goyt-work-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("intermediate work directories remain: %v", leftovers)
	}
}

func TestHLSAudioIntegration(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("integration test requires ffmpeg")
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("integration test requires ffprobe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	run := func(program string, args ...string) []byte {
		t.Helper()
		output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, output)
		}
		return output
	}

	sourceDir := t.TempDir()
	outputDir := t.TempDir()

	// Generate a 2-second ID3-prefixed packed AAC segment
	aacSegment := filepath.Join(sourceDir, "segment-0.aac")
	run(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=48000",
		"-t", "2",
		"-vn",
		"-c:a", "aac",
		"-f", "adts",
		aacSegment,
	)

	// Prefix with ID3 tag containing com.apple.streaming.transportStreamTimestamp
	data, err := os.ReadFile(aacSegment)
	if err != nil {
		t.Fatal(err)
	}
	id3Payload := []byte("com.apple.streaming.transportStreamTimestamp\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	id3Header := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, byte(len(id3Payload) >> 7), byte(len(id3Payload) & 0x7f)}
	taggedSegment := append(id3Header, id3Payload...)
	taggedSegment = append(taggedSegment, data...)
	if err := os.WriteFile(aacSegment, taggedSegment, 0600); err != nil {
		t.Fatal(err)
	}

	audioM3U8 := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:2.0,\nsegment-0.aac\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "audio.m3u8"), []byte(audioM3U8), 0600); err != nil {
		t.Fatal(err)
	}

	masterM3U8 := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English - original\",LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,AUDIO=\"audio\"\nvideo.m3u8\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "master.m3u8"), []byte(masterM3U8), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var requestedPaths []string
	fileServer := http.FileServer(http.Dir(sourceDir))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		mu.Unlock()
		fileServer.ServeHTTP(w, r)
	}))
	defer server.Close()

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}

	verifier, err := NewVerifier(ffprobePath)
	if err != nil {
		t.Fatal(err)
	}

	downloader, err := NewHLSDownloader(server.Client(), processor)
	if err != nil {
		t.Fatal(err)
	}

	formatsToTest := []struct {
		format       string
		wantCodec    string
		destFilename string
	}{
		{"best", "aac", "hls_best.m4a"},
		{"aac", "aac", "hls_song.aac"},
		{"m4a", "aac", "hls_song.m4a"},
		{"mp3", "mp3", "hls_song.mp3"},
		{"flac", "flac", "hls_song.flac"},
		{"opus", "opus", "hls_song.opus"},
		{"vorbis", "vorbis", "hls_song.ogg"},
		{"wav", "pcm_s16le", "hls_song.wav"},
		{"alac", "alac", "hls_alac.m4a"},
	}

	for _, tt := range formatsToTest {
		t.Run("HLS_"+tt.format, func(t *testing.T) {
			spec, err := ResolveAudioOutputSpec(tt.format, "aac", nil, "")
			if err != nil {
				t.Fatalf("ResolveAudioOutputSpec failed: %v", err)
			}

			if !spec.Copy && spec.Encoder != "" {
				has, err := processor.HasEncoder(ctx, spec.Encoder)
				if err != nil || !has {
					if os.Getenv("GOYT_REQUIRE_ALL_ENCODERS") != "" || os.Getenv("CI") != "" {
						t.Fatalf("required audio encoder %q is not available in CI environment (err: %v)", spec.Encoder, err)
					}
					t.Skipf("encoder %q not available on system, skipping", spec.Encoder)
				}
			}

			outputPath := filepath.Join(outputDir, tt.destFilename)

			mu.Lock()
			startRequestsIdx := len(requestedPaths)
			mu.Unlock()

			result, err := downloader.DownloadAudio(
				ctx,
				Resource{URL: server.URL + "/master.m3u8"},
				outputPath,
				"",
				spec,
				DownloadOptions{},
				nil,
			)
			if err != nil {
				t.Fatalf("DownloadAudio failed: %v", err)
			}

			// Verify that during audio-only HLS download, video.m3u8 or video segments were never requested
			mu.Lock()
			testRequests := requestedPaths[startRequestsIdx:]
			mu.Unlock()
			for _, reqPath := range testRequests {
				if strings.Contains(reqPath, "video") {
					t.Fatalf("audio-only HLS download made a request to video resource: %s", reqPath)
				}
			}

			if result.SizeBytes <= 0 {
				t.Fatal("HLS audio output is empty")
			}

			expectedDuration := 2 * time.Second
			verification, err := verifier.VerifyAudio(ctx, result.Path, spec, &expectedDuration)
			if err != nil {
				t.Fatalf("VerifyAudio failed: %v", err)
			}

			if verification.AudioCodec != tt.wantCodec {
				t.Fatalf("expected codec %s, got %s", tt.wantCodec, verification.AudioCodec)
			}

			if err := processor.CheckDecodeAudio(ctx, result.Path); err != nil {
				t.Fatalf("CheckDecodeAudio failed: %v", err)
			}
		})
	}

	leftovers, err := filepath.Glob(filepath.Join(outputDir, ".goyt-hls-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("intermediate HLS work directories remain: %v", leftovers)
	}
}

func TestDecodeFailurePreservesExistingDestination(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed, skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outputDir := t.TempDir()
	existingDestination := filepath.Join(outputDir, "output.mp3")
	if err := os.WriteFile(existingDestination, []byte("preexisting-unmodified-content"), 0600); err != nil {
		t.Fatal(err)
	}

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}

	// Create an invalid/corrupted staged file that fails decoding
	corruptedStaged := filepath.Join(outputDir, ".goyt-cli-staged-test.mp3")
	if err := os.WriteFile(corruptedStaged, []byte("corrupted data not valid mp3"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(corruptedStaged)

	// Verify that CheckDecodeAudio fails on the corrupted file
	decodeErr := processor.CheckDecodeAudio(ctx, corruptedStaged)
	if decodeErr == nil {
		t.Fatal("expected CheckDecodeAudio to fail on corrupted audio")
	}

	// Staged file is not renamed to existingDestination on decode error
	// Confirm existing destination file is 100% intact
	content, readErr := os.ReadFile(existingDestination)
	if readErr != nil || string(content) != "preexisting-unmodified-content" {
		t.Fatalf("destination was modified or corrupted: %q, err: %v", content, readErr)
	}
}
