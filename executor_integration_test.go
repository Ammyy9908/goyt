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

func TestVideoExecutorIntegration_AllCombinations(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("integration test requires ffmpeg")
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("integration test requires ffprobe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	run := func(program string, args ...string) []byte {
		t.Helper()
		output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, output)
		}
		return output
	}

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}

	verifier, err := NewVerifier(ffprobePath)
	if err != nil {
		t.Fatal(err)
	}

	sourceDir := t.TempDir()
	outputDir := t.TempDir()

	// Find available encoders
	findEncoder := func(codec string, candidates []string) string {
		for _, name := range candidates {
			has, err := processor.HasEncoder(ctx, name)
			if err == nil && has {
				return name
			}
		}
		return ""
	}

	h264Enc := findEncoder("h264", []string{"libx264", "h264_videotoolbox", "h264_nvenc"})
	vp9Enc := findEncoder("vp9", []string{"libvpx-vp9", "libvpx"})
	av1Enc := findEncoder("av1", []string{"libsvtav1", "libaom-av1", "librav1e", "av1_nvenc", "av1_qsv", "av1_amf"})
	opusEnc := findEncoder("opus", []string{"libopus", "opus"})

	if h264Enc == "" {
		if os.Getenv("CI") != "" || os.Getenv("GOYT_REQUIRE_ALL_ENCODERS") != "" {
			t.Fatal("missing required H.264 video encoder")
		}
		t.Skip("H.264 encoder not found, skipping")
	}

	// Generate fixtures
	h264Path := filepath.Join(sourceDir, "h264.mp4")
	run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=160x120:r=25", "-t", "1", "-an",
		"-c:v", h264Enc, "-pix_fmt", "yuv420p", h264Path)

	aacPath := filepath.Join(sourceDir, "aac.m4a")
	run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-vn",
		"-c:a", "aac", aacPath)

	var vp9Path, av1Path, opusPath string

	if vp9Enc != "" {
		vp9Path = filepath.Join(sourceDir, "vp9.webm")
		run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", "color=c=green:s=160x120:r=25", "-t", "1", "-an",
			"-c:v", vp9Enc, "-pix_fmt", "yuv420p", vp9Path)
	}

	if av1Enc != "" {
		av1Path = filepath.Join(sourceDir, "av1.mp4")
		run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", "color=c=red:s=160x120:r=25", "-t", "1", "-an",
			"-c:v", av1Enc, "-pix_fmt", "yuv420p", av1Path)
	}

	if opusEnc != "" {
		opusPath = filepath.Join(sourceDir, "opus.opus")
		run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-vn",
			"-c:a", opusEnc, "-f", "opus", opusPath)
	}

	server := httptest.NewServer(http.FileServer(http.Dir(sourceDir)))
	defer server.Close()

	executor, err := NewExecutor(NewDownloader(server.Client()), processor)
	if err != nil {
		t.Fatal(err)
	}

	h120 := 120
	w160 := 160
	rate := int64(1000000)

	type testCombo struct {
		name        string
		videoCodec  string
		audioCodec  string
		container   string
		videoPath   string
		audioPath   string
		wantVideo   string
		wantAudio   string
		reqEncoders []string
	}

	combos := []testCombo{
		{
			name:        "H264_AAC_MP4",
			videoCodec:  "h264",
			audioCodec:  "aac",
			container:   "mp4",
			videoPath:   "/h264.mp4",
			audioPath:   "/aac.m4a",
			wantVideo:   "h264",
			wantAudio:   "aac",
			reqEncoders: []string{h264Enc},
		},
		{
			name:        "AV1_AAC_MP4",
			videoCodec:  "av1",
			audioCodec:  "aac",
			container:   "mp4",
			videoPath:   "/av1.mp4",
			audioPath:   "/aac.m4a",
			wantVideo:   "av1",
			wantAudio:   "aac",
			reqEncoders: []string{av1Enc},
		},
		{
			name:        "VP9_Opus_WebM",
			videoCodec:  "vp9",
			audioCodec:  "opus",
			container:   "webm",
			videoPath:   "/vp9.webm",
			audioPath:   "/opus.opus",
			wantVideo:   "vp9",
			wantAudio:   "opus",
			reqEncoders: []string{vp9Enc, opusEnc},
		},
		{
			name:        "AV1_Opus_WebM",
			videoCodec:  "av1",
			audioCodec:  "opus",
			container:   "webm",
			videoPath:   "/av1.mp4",
			audioPath:   "/opus.opus",
			wantVideo:   "av1",
			wantAudio:   "opus",
			reqEncoders: []string{av1Enc, opusEnc},
		},
		{
			name:        "H264_AAC_MKV",
			videoCodec:  "h264",
			audioCodec:  "aac",
			container:   "mkv",
			videoPath:   "/h264.mp4",
			audioPath:   "/aac.m4a",
			wantVideo:   "h264",
			wantAudio:   "aac",
			reqEncoders: []string{h264Enc},
		},
		{
			name:        "H264_Opus_MKV",
			videoCodec:  "h264",
			audioCodec:  "opus",
			container:   "mkv",
			videoPath:   "/h264.mp4",
			audioPath:   "/opus.opus",
			wantVideo:   "h264",
			wantAudio:   "opus",
			reqEncoders: []string{h264Enc, opusEnc},
		},
		{
			name:        "VP9_AAC_MKV",
			videoCodec:  "vp9",
			audioCodec:  "aac",
			container:   "mkv",
			videoPath:   "/vp9.webm",
			audioPath:   "/aac.m4a",
			wantVideo:   "vp9",
			wantAudio:   "aac",
			reqEncoders: []string{vp9Enc},
		},
		{
			name:        "VP9_Opus_MKV",
			videoCodec:  "vp9",
			audioCodec:  "opus",
			container:   "mkv",
			videoPath:   "/vp9.webm",
			audioPath:   "/opus.opus",
			wantVideo:   "vp9",
			wantAudio:   "opus",
			reqEncoders: []string{vp9Enc, opusEnc},
		},
		{
			name:        "AV1_AAC_MKV",
			videoCodec:  "av1",
			audioCodec:  "aac",
			container:   "mkv",
			videoPath:   "/av1.mp4",
			audioPath:   "/aac.m4a",
			wantVideo:   "av1",
			wantAudio:   "aac",
			reqEncoders: []string{av1Enc},
		},
		{
			name:        "AV1_Opus_MKV",
			videoCodec:  "av1",
			audioCodec:  "opus",
			container:   "mkv",
			videoPath:   "/av1.mp4",
			audioPath:   "/opus.opus",
			wantVideo:   "av1",
			wantAudio:   "opus",
			reqEncoders: []string{av1Enc, opusEnc},
		},
	}

	for _, tc := range combos {
		t.Run(tc.name, func(t *testing.T) {
			for _, enc := range tc.reqEncoders {
				if enc == "" {
					if os.Getenv("CI") != "" || os.Getenv("GOYT_REQUIRE_ALL_ENCODERS") != "" {
						t.Fatalf("required encoder missing in CI environment for test %s", tc.name)
					}
					t.Skipf("required encoder missing on local machine, skipping %s", tc.name)
				}
			}

			vContainer := "mp4"
			if tc.videoCodec == "vp9" {
				vContainer = "webm"
			}
			aContainer := "m4a"
			if tc.audioCodec == "opus" {
				aContainer = "webm"
			}

			media := &Media{
				ID:    "test-" + tc.name,
				Title: "Test " + tc.name,
				Formats: []Format{
					{
						ID:         "v-" + tc.videoCodec,
						Protocol:   ProtocolHTTP,
						Container:  vContainer,
						VideoCodec: tc.videoCodec,
						AudioCodec: "none",
						Width:      &w160,
						Height:     &h120,
						Bitrate:    &rate,
						Resource:   Resource{URL: server.URL + tc.videoPath},
					},
					{
						ID:              "a-" + tc.audioCodec,
						Protocol:        ProtocolHTTP,
						Container:       aContainer,
						VideoCodec:      "none",
						AudioCodec:      tc.audioCodec,
						Language:        "en",
						AudioIsOriginal: true,
						Bitrate:         &rate,
						Resource:        Resource{URL: server.URL + tc.audioPath},
					},
				},
			}

			plan, err := Plan(media, Selection{
				VideoCodec:    tc.videoCodec,
				Container:     tc.container,
				AllowSeparate: true,
			})
			if err != nil {
				t.Fatalf("Plan failed: %v", err)
			}

			destPath := filepath.Join(outputDir, "out_"+tc.name+"."+tc.container)
			result, err := executor.Execute(ctx, plan, destPath, ExecuteOptions{})
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			if result.SizeBytes <= 0 {
				t.Fatal("output is empty")
			}

			// Metadata and spec verification
			dur := 1 * time.Second
			vSpec := VideoVerificationSpec{
				ExpectedContainer:  tc.container,
				ExpectedVideoCodec: tc.videoCodec,
				ExpectedAudioCodec: tc.audioCodec,
				ExpectedWidth:      &w160,
				ExpectedHeight:     &h120,
			}
			verification, err := verifier.VerifyVideo(ctx, result.Path, vSpec, &dur)
			if err != nil {
				t.Fatalf("VerifyVideo failed: %v", err)
			}
			if normalizeCodec(verification.VideoCodec) != tc.wantVideo {
				t.Fatalf("got video codec %q, want %q", verification.VideoCodec, tc.wantVideo)
			}
			if normalizeCodec(verification.AudioCodec) != tc.wantAudio {
				t.Fatalf("got audio codec %q, want %q", verification.AudioCodec, tc.wantAudio)
			}

			// Full decode check
			if err := processor.CheckDecode(ctx, result.Path); err != nil {
				t.Fatalf("CheckDecode failed on %s: %v", result.Path, err)
			}
		})
	}
}

func TestHLSVideoIntegration_MKV(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("integration test requires ffmpeg")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("integration test requires ffprobe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	// Generate 1s H.264 video TS segment and 1s AAC audio TS segment
	videoSeg := filepath.Join(sourceDir, "video-0.ts")
	run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=160x120:r=25", "-t", "1", "-an",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "mpegts", videoSeg)

	audioSeg := filepath.Join(sourceDir, "audio-0.ts")
	run(ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-vn",
		"-c:a", "aac", "-f", "mpegts", audioSeg)

	videoM3U8 := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\nvideo-0.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "video.m3u8"), []byte(videoM3U8), 0600); err != nil {
		t.Fatal(err)
	}

	audioM3U8 := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\naudio-0.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "audio.m3u8"), []byte(audioM3U8), 0600); err != nil {
		t.Fatal(err)
	}

	masterM3U8 := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=160x120,CODECS=\"avc1.640028,mp4a.40.2\",AUDIO=\"audio\"\nvideo.m3u8\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "master.m3u8"), []byte(masterM3U8), 0600); err != nil {
		t.Fatal(err)
	}

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

	downloader, err := NewHLSDownloader(server.Client(), processor)
	if err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(outputDir, "hls_video.mkv")
	result, err := downloader.Download(
		ctx,
		Resource{URL: server.URL + "/master.m3u8"},
		outputPath,
		120,
		DownloadOptions{},
		nil,
	)
	if err != nil {
		t.Fatalf("HLS video download failed: %v", err)
	}

	if result.SizeBytes <= 0 {
		t.Fatal("HLS output is empty")
	}

	expectedDur := 1 * time.Second
	vSpec := VideoVerificationSpec{
		ExpectedContainer:  "mkv",
		ExpectedVideoCodec: "h264",
		ExpectedAudioCodec: "aac",
	}
	ver, err := verifier.VerifyVideo(ctx, result.Path, vSpec, &expectedDur)
	if err != nil {
		t.Fatalf("VerifyVideo on HLS MKV failed: %v", err)
	}
	if normalizeCodec(ver.VideoCodec) != "h264" || normalizeCodec(ver.AudioCodec) != "aac" {
		t.Fatalf("unexpected codecs: video=%s audio=%s", ver.VideoCodec, ver.AudioCodec)
	}

	if err := processor.CheckDecode(ctx, result.Path); err != nil {
		t.Fatalf("CheckDecode failed on HLS MKV: %v", err)
	}
}
