//go:build integration

package goyt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobIntegration(t *testing.T) {
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

	runFFmpeg := func(args ...string) {
		t.Helper()
		cmdArgs := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
		output, err := exec.CommandContext(ctx, ffmpegPath, cmdArgs...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg failed: %v\n%s", err, output)
		}
	}

	sourceDir := t.TempDir()
	videoSourceFile := filepath.Join(sourceDir, "video.mp4")
	audioSourceFile := filepath.Join(sourceDir, "audio.m4a")
	muxedSourceFile := filepath.Join(sourceDir, "muxed.mp4")

	// Generate 1s test media files
	runFFmpeg("-f", "lavfi", "-i", "color=c=blue:s=160x120:r=25", "-t", "1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", videoSourceFile)
	runFFmpeg("-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-vn", "-c:a", "aac", audioSourceFile)
	runFFmpeg("-f", "lavfi", "-i", "color=c=blue:s=160x120:r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", muxedSourceFile)

	videoData, err := os.ReadFile(videoSourceFile)
	if err != nil {
		t.Fatal(err)
	}
	audioData, err := os.ReadFile(audioSourceFile)
	if err != nil {
		t.Fatal(err)
	}
	muxedData, err := os.ReadFile(muxedSourceFile)
	if err != nil {
		t.Fatal(err)
	}

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(ffprobePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("video merge with partial resume and input reuse", func(t *testing.T) {
		var videoRequests, audioRequests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/video.mp4":
				videoRequests.Add(1)
				w.Header().Set("ETag", `"etag-video"`)
				http.ServeContent(w, r, "video.mp4", time.Now(), strings.NewReader(string(videoData)))
			case "/audio.m4a":
				count := audioRequests.Add(1)
				w.Header().Set("ETag", `"etag-audio"`)
				if count == 1 {
					// First attempt: send only first 50 bytes and abort to test partial resume
					w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audioData)))
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(audioData[:50])
					return
				}
				// Subsequent attempts: serve full content supporting Range/If-Range
				http.ServeContent(w, r, "audio.m4a", time.Now(), strings.NewReader(string(audioData)))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-video")
		destFile := filepath.Join(t.TempDir(), "final-video.mp4")

		h120 := 120
		w160 := 160
		mockMedia := &Media{
			ID:        "test-video-id",
			SourceURL: "https://www.youtube.com/watch?v=testvideoid",
			Title:     "Integration Test Video",
			Formats: []Format{
				{
					ID:         "137",
					Protocol:   ProtocolHTTP,
					Container:  "mp4",
					VideoCodec: "avc1.640028",
					AudioCodec: "none",
					Width:      &w160,
					Height:     &h120,
					Resource:   Resource{URL: server.URL + "/video.mp4"},
				},
				{
					ID:              "140",
					Protocol:        ProtocolHTTP,
					Container:       "m4a",
					VideoCodec:      "none",
					AudioCodec:      "mp4a.40.2",
					AudioTrackID:    "en-orig",
					Language:        "en",
					AudioIsOriginal: true,
					AudioIsDefault:  true,
					Resource:        Resource{URL: server.URL + "/audio.m4a"},
				},
			},
		}

		plan, err := Plan(mockMedia, Selection{MaxHeight: 120, Container: "mp4", AllowSeparate: true})
		if err != nil {
			t.Fatalf("failed to create plan: %v", err)
		}

		manifest, err := CreateVideoJob(
			mockMedia.SourceURL,
			mockMedia.ID,
			mockMedia.Title,
			nil,
			destFile,
			plan,
			Selection{MaxHeight: 120, Container: "mp4", AllowSeparate: true},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		extractorFn := func(ctx context.Context, u *url.URL) (*Media, error) {
			return mockMedia, nil
		}

		opts := JobRunnerOptions{
			Extractor:  extractorFn,
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 0,
			},
			URLRefreshes: 1,
		}

		// Attempt 1: Video succeeds, audio fails on incomplete stream
		err = ExecuteJob(ctx, jobDir, opts)
		if err == nil {
			t.Fatal("expected error on attempt 1 due to interrupted audio download")
		}

		// Verify video stream completed in manifest
		m1, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatal(err)
		}
		if !m1.Streams[0].Completed {
			t.Fatal("expected stream 0 (video) to be completed")
		}
		if m1.Streams[1].Completed {
			t.Fatal("expected stream 1 (audio) to NOT be completed yet")
		}

		initialVideoRequests := videoRequests.Load()

		// Attempt 2: Resumes audio with Range request, merges streams, commits destination!
		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error on resume: %v", err)
		}

		// Verify video was reused (no new network requests for video!)
		if videoRequests.Load() != initialVideoRequests {
			t.Fatalf("expected video stream to be reused without network request, but video requests increased from %d to %d", initialVideoRequests, videoRequests.Load())
		}

		// Verify output exists and is verified MP4
		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}

		if _, err := verifier.VerifyMP4(ctx, destFile, nil); err != nil {
			t.Fatalf("output failed verification: %v", err)
		}

		// Attempt 3: Idempotent resume of completed job (makes 0 network requests)
		videoReqsBefore := videoRequests.Load()
		audioReqsBefore := audioRequests.Load()
		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error resuming completed job: %v", err)
		}
		if videoRequests.Load() != videoReqsBefore || audioRequests.Load() != audioReqsBefore {
			t.Fatalf("resuming completed job made unexpected network requests: video %d->%d, audio %d->%d", videoReqsBefore, videoRequests.Load(), audioReqsBefore, audioRequests.Load())
		}

		// Attempt 4: If destination output is removed, resume reports error
		_ = os.Remove(destFile)
		err = ExecuteJob(ctx, jobDir, opts)
		if err == nil {
			t.Fatal("expected error when completed destination file is missing, got nil")
		}
		if !errors.Is(err, ErrCompletedOutputMismatch) {
			t.Fatalf("expected ErrCompletedOutputMismatch, got: %v", err)
		}
	})

	t.Run("audio-only job with decode check and crash recovery", func(t *testing.T) {
		hasLame, _ := processor.HasEncoder(ctx, "libmp3lame")
		if !hasLame {
			t.Skip("libmp3lame encoder not available for MP3 test")
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "audio.m4a", time.Now(), strings.NewReader(string(audioData)))
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-audio-only")
		destFile := filepath.Join(t.TempDir(), "final-audio.mp3")

		mockMedia := &Media{
			ID:        "audio-demo-id",
			SourceURL: "https://www.youtube.com/watch?v=audiodemo",
			Title:     "Audio Demo",
			Formats: []Format{
				{
					ID:              "140",
					Protocol:        ProtocolHTTP,
					Container:       "m4a",
					VideoCodec:      "none",
					AudioCodec:      "mp4a.40.2",
					AudioTrackID:    "en-orig",
					Language:        "en",
					AudioIsOriginal: true,
					AudioIsDefault:  true,
					Resource:        Resource{URL: server.URL + "/audio.m4a"},
				},
			},
		}

		plan, err := PlanAudio(mockMedia, AudioSelection{AudioFormat: "mp3"})
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateAudioJob(
			mockMedia.SourceURL,
			mockMedia.ID,
			mockMedia.Title,
			nil,
			destFile,
			plan,
			AudioSelection{AudioFormat: "mp3"},
			true, // DecodeCheck = true
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		opts := JobRunnerOptions{
			Extractor: func(ctx context.Context, u *url.URL) (*Media, error) {
				return mockMedia, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options:    DownloadOptions{Resume: true},
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("failed to execute audio job: %v", err)
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination MP3 missing or empty: %v", err)
		}

		if _, err := verifier.VerifyAudio(ctx, destFile, plan.OutputSpec, nil); err != nil {
			t.Fatalf("audio verification failed: %v", err)
		}
	})

	t.Run("tampered completed input detected and redownloaded", func(t *testing.T) {
		var serverRequests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverRequests.Add(1)
			http.ServeContent(w, r, "video.mp4", time.Now(), strings.NewReader(string(muxedData)))
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-tamper")
		destFile := filepath.Join(t.TempDir(), "tamper-out.mp4")

		h360 := 360
		w640 := 640
		mockMedia := &Media{
			ID:        "tamper-id",
			SourceURL: "https://www.youtube.com/watch?v=tamperid",
			Title:     "Tamper Test",
			Formats: []Format{
				{
					ID:         "18",
					Protocol:   ProtocolHTTP,
					Container:  "mp4",
					VideoCodec: "avc1.42001E",
					AudioCodec: "mp4a.40.2",
					Height:     &h360,
					Width:      &w640,
					Resource:   Resource{URL: server.URL + "/video.mp4"},
				},
			},
		}

		plan, err := Plan(mockMedia, Selection{MaxHeight: 360, Container: "mp4"})
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateVideoJob(
			mockMedia.SourceURL,
			mockMedia.ID,
			mockMedia.Title,
			nil,
			destFile,
			plan,
			Selection{MaxHeight: 360, Container: "mp4"},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		// Pre-populate input with corrupt content and marked completed with original hash
		inputPath := filepath.Join(jobDir, "inputs", "stream-0.media")
		if err := os.MkdirAll(filepath.Dir(inputPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inputPath, []byte("corrupted data that does not match sha256"), 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Streams[0].Completed = true
		manifest.Streams[0].SizeBytes = int64(len(muxedData))
		manifest.Streams[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		opts := JobRunnerOptions{
			Extractor: func(ctx context.Context, u *url.URL) (*Media, error) {
				return mockMedia, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options:    DownloadOptions{Resume: true},
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error recovering from tampered stream: %v", err)
		}

		if serverRequests.Load() != 1 {
			t.Fatalf("expected 1 server request to re-download tampered stream, got %d", serverRequests.Load())
		}
	})

	t.Run("missing pinned audio identity fails on resume", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "video.mp4", time.Now(), strings.NewReader(string(videoData)))
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-missing-audio")
		destFile := filepath.Join(t.TempDir(), "missing-audio.mp4")

		mockMediaOriginal := &Media{
			ID:        "audio-pinned-id",
			SourceURL: "https://www.youtube.com/watch?v=audiopinned",
			Title:     "Audio Pinned Test",
			Formats: []Format{
				{
					ID:              "140",
					Protocol:        ProtocolHTTP,
					Container:       "m4a",
					VideoCodec:      "none",
					AudioCodec:      "mp4a.40.2",
					AudioTrackID:    "en-orig",
					Language:        "en",
					AudioIsOriginal: true,
					AudioIsDefault:  true,
					Resource:        Resource{URL: server.URL + "/audio.m4a"},
				},
			},
		}

		plan, err := PlanAudio(mockMediaOriginal, AudioSelection{AudioFormat: "best"})
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateAudioJob(
			mockMediaOriginal.SourceURL,
			mockMediaOriginal.ID,
			mockMediaOriginal.Title,
			nil,
			destFile,
			plan,
			AudioSelection{AudioFormat: "best"},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		// On resume, candidate list only has spanish dub, missing the pinned english original
		mockMediaRefreshed := &Media{
			ID:        "audio-pinned-id",
			SourceURL: "https://www.youtube.com/watch?v=audiopinned",
			Title:     "Audio Pinned Test",
			Formats: []Format{
				{
					ID:              "140",
					Protocol:        ProtocolHTTP,
					Container:       "m4a",
					VideoCodec:      "none",
					AudioCodec:      "mp4a.40.2",
					AudioTrackID:    "es-dub",
					Language:        "es",
					AudioIsOriginal: false,
					AudioIsDefault:  false,
					Resource:        Resource{URL: server.URL + "/audio_es.m4a"},
				},
			},
		}

		opts := JobRunnerOptions{
			Extractor: func(ctx context.Context, u *url.URL) (*Media, error) {
				return mockMediaRefreshed, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err == nil {
			t.Fatal("expected error when pinned audio track is missing on refresh, got nil")
		}
		if !errors.Is(err, ErrSelectionUnavailable) {
			t.Fatalf("expected ErrSelectionUnavailable, got: %v", err)
		}
	})

	t.Run("crash window recovery after rename to destination", func(t *testing.T) {
		jobDir := filepath.Join(t.TempDir(), "job-crash-recovery")
		destFile := filepath.Join(t.TempDir(), "committed-video.mp4")

		// Write final valid file to destination
		if err := os.WriteFile(destFile, videoData, 0644); err != nil {
			t.Fatal(err)
		}
		finalHash, finalSize, err := ComputeFileSHA256(destFile)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		// Manifest is stuck in ready_to_commit stage right before crash
		manifest := &JobManifest{
			SchemaVersion:   1,
			JobID:           "crash-job-123",
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
			SourceURL:       "https://www.youtube.com/watch?v=crashvideo",
			VideoID:         "crashvideo",
			DestinationPath: destFile,
			Transport:       "http",
			Mode:            "video",
			Stage:           JobStageReadyToCommit,
			PreCommit: &JobPreCommit{
				StagedRelativePath: "staged_output.mp4",
				FinalSizeBytes:     finalSize,
				FinalSHA256:        finalHash,
			},
			Streams: []JobStreamState{
				{
					Index:        0,
					Format:       FormatIdentity{ID: "18"},
					RelativePath: "inputs/stream-0.media",
					Completed:    true,
				},
			},
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		opts := JobRunnerOptions{
			Downloader: NewDownloader(nil),
			Processor:  processor,
			Verifier:   verifier,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error recovering crashed commit: %v", err)
		}

		mLoaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatal(err)
		}
		if mLoaded.Stage != JobStageCompleted {
			t.Fatalf("expected stage to transition to completed, got: %s", mLoaded.Stage)
		}
	})

	t.Run("retry and restart diagnostics on mid-transfer stream failure without strong ETag", func(t *testing.T) {
		var audioRequests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/video.mp4":
				w.Header().Set("ETag", `"etag-video"`)
				http.ServeContent(w, r, "video.mp4", time.Now(), strings.NewReader(string(videoData)))
			case "/audio.m4a":
				count := audioRequests.Add(1)
				// Do NOT send ETag on audio stream (simulating YouTube/server without strong ETag)
				if count == 1 {
					w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audioData)))
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(audioData[:50])
					return
				}
				// Attempt 1: serve full audio without ETag
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audioData)))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(audioData)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-retry-diagnostics")
		destFile := filepath.Join(t.TempDir(), "retry-diag-out.mp4")

		h120 := 120
		w160 := 160
		mockMedia := &Media{
			ID:        "diag-test-id",
			SourceURL: "https://www.youtube.com/watch?v=diagtestid",
			Title:     "Diagnostics Test",
			Formats: []Format{
				{
					ID:         "137",
					Protocol:   ProtocolHTTP,
					Container:  "mp4",
					VideoCodec: "avc1.640028",
					AudioCodec: "none",
					Width:      &w160,
					Height:     &h120,
					Resource:   Resource{URL: server.URL + "/video.mp4"},
				},
				{
					ID:              "140",
					Protocol:        ProtocolHTTP,
					Container:       "m4a",
					VideoCodec:      "none",
					AudioCodec:      "mp4a.40.2",
					AudioTrackID:    "en-orig",
					Language:        "en",
					AudioIsOriginal: true,
					AudioIsDefault:  true,
					Resource:        Resource{URL: server.URL + "/audio.m4a"},
				},
			},
		}

		plan, err := Plan(mockMedia, Selection{MaxHeight: 120, Container: "mp4", AllowSeparate: true})
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateVideoJob(
			mockMedia.SourceURL,
			mockMedia.ID,
			mockMedia.Title,
			nil,
			destFile,
			plan,
			Selection{MaxHeight: 120, Container: "mp4", AllowSeparate: true},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		var stderrBuf strings.Builder
		opts := JobRunnerOptions{
			Extractor: func(ctx context.Context, u *url.URL) (*Media, error) {
				return mockMedia, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
				RetryDelay: time.Millisecond,
			},
			Stderr: &stderrBuf,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing job: %v", err)
		}

		stderrOutput := stderrBuf.String()
		if !strings.Contains(stderrOutput, "Stream 2 (format 140): recovery attempt 1/2") {
			t.Errorf("expected stderr to contain stream 2 retry diagnostic, got:\n%s", stderrOutput)
		}
		if !strings.Contains(stderrOutput, "Restarting from byte 0") {
			t.Errorf("expected stderr to indicate restart from byte 0, got:\n%s", stderrOutput)
		}
		if !strings.Contains(stderrOutput, "discarded") {
			t.Errorf("expected stderr to report discarded partial data, got:\n%s", stderrOutput)
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}
	})
}

func TestHLSJobIntegration(t *testing.T) {
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

	runFFmpeg := func(args ...string) {
		t.Helper()
		cmdArgs := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
		output, err := exec.CommandContext(ctx, ffmpegPath, cmdArgs...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg failed: %v\n%s", err, output)
		}
	}

	sourceDir := t.TempDir()
	tsVideo0 := filepath.Join(sourceDir, "video0.ts")
	tsVideo1 := filepath.Join(sourceDir, "video1.ts")
	tsMuxed0 := filepath.Join(sourceDir, "muxed0.ts")
	tsMuxed1 := filepath.Join(sourceDir, "muxed1.ts")
	rawAAC0 := filepath.Join(sourceDir, "raw0.aac")
	rawAAC1 := filepath.Join(sourceDir, "raw1.aac")

	// Generate 1s test media chunks
	runFFmpeg("-f", "lavfi", "-i", "color=c=blue:s=160x120:r=25", "-t", "1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "mpegts", tsVideo0)
	runFFmpeg("-f", "lavfi", "-i", "color=c=red:s=160x120:r=25", "-t", "1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "mpegts", tsVideo1)

	runFFmpeg("-f", "lavfi", "-i", "color=c=blue:s=160x120:r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-f", "mpegts", tsMuxed0)
	runFFmpeg("-f", "lavfi", "-i", "color=c=red:s=160x120:r=25", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-f", "mpegts", tsMuxed1)

	runFFmpeg("-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-vn", "-c:a", "aac", "-f", "adts", rawAAC0)
	runFFmpeg("-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000", "-t", "1", "-vn", "-c:a", "aac", "-f", "adts", rawAAC1)

	// Prefix AAC with ID3 timestamp
	id3Payload := []byte("com.apple.streaming.transportStreamTimestamp\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	id3Header := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, byte(len(id3Payload) >> 7), byte(len(id3Payload) & 0x7f)}
	tag := append(id3Header, id3Payload...)

	rawAACData0, err := os.ReadFile(rawAAC0)
	if err != nil {
		t.Fatal(err)
	}
	aac0 := append(tag, rawAACData0...)

	rawAACData1, err := os.ReadFile(rawAAC1)
	if err != nil {
		t.Fatal(err)
	}
	aac1 := append(tag, rawAACData1...)

	videoData0, _ := os.ReadFile(tsVideo0)
	videoData1, _ := os.ReadFile(tsVideo1)
	muxedData0, _ := os.ReadFile(tsMuxed0)
	muxedData1, _ := os.ReadFile(tsMuxed1)

	processor, err := NewFFmpeg(ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(ffprobePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("combined MPEG-TS HLS job resume with segment reuse", func(t *testing.T) {
		var seg0Requests, seg1Requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/muxed.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/seg0.ts\n#EXTINF:1.0,\n/seg1.ts\n#EXT-X-ENDLIST\n")
			case "/seg0.ts":
				seg0Requests.Add(1)
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData0)
			case "/seg1.ts":
				seg1Requests.Add(1)
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-combined")
		destFile := filepath.Join(t.TempDir(), "output.mp4")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		resolved, err := hlsDownloader.ResolvePlaylist(ctx, Resource{URL: server.URL + "/muxed.m3u8"}, 1080)
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=hlsvideotest",
			"hlsvideotest",
			"HLS Combined Test",
			destFile,
			resolved,
			Selection{MaxHeight: 1080},
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		// Download only segment 0 first and checkpoint it
		track0Dir := filepath.Join(jobDir, "hls", "gen-1", "track-0")
		_ = os.MkdirAll(track0Dir, 0700)
		seg0Path := filepath.Join(track0Dir, "segment-00000.ts")
		if err := os.WriteFile(seg0Path, muxedData0, 0600); err != nil {
			t.Fatal(err)
		}
		hash0, size0, _ := ComputeFileSHA256(seg0Path)
		manifest.HLS.Tracks[0].Segments[0].Completed = true
		manifest.HLS.Tracks[0].Segments[0].RelativePath = filepath.Join("hls", "gen-1", "track-0", "segment-00000.ts")
		manifest.HLS.Tracks[0].Segments[0].SizeBytes = size0
		manifest.HLS.Tracks[0].Segments[0].SHA256 = hash0
		manifest.Stage = JobStageDownloading
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		// Now execute the job to resume
		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				return Resource{URL: server.URL + "/muxed.m3u8"}, &Media{ID: "hlsvideotest", Title: "HLS Combined Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
			},
			URLRefreshes: 1,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing HLS job: %v", err)
		}

		// Verify segment 0 was NOT requested again (since it was already completed)
		if seg0Requests.Load() != 0 {
			t.Fatalf("expected 0 requests for completed segment 0, got %d", seg0Requests.Load())
		}
		// Segment 1 was downloaded
		if seg1Requests.Load() != 1 {
			t.Fatalf("expected 1 request for segment 1, got %d", seg1Requests.Load())
		}

		// Verify destination exists and is valid MP4
		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}

		// Verify job is marked completed and intermediates cleaned up
		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Stage != JobStageCompleted {
			t.Fatalf("expected JobStageCompleted, got %s", loaded.Stage)
		}
		if _, err := os.Stat(filepath.Join(jobDir, "hls")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("expected hls intermediate directory to be cleaned up")
		}

		// Test idempotent resume of completed job without network requests
		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error resuming completed job: %v", err)
		}
		if seg1Requests.Load() != 1 {
			t.Fatalf("expected still 1 request for segment 1 after completed resume, got %d", seg1Requests.Load())
		}
	})

	t.Run("separate video plus packed AAC audio HLS job resume", func(t *testing.T) {
		var v0Reqs, v1Reqs, a0Reqs, a1Reqs atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/master.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,URI=\"/audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,CODECS=\"avc1.640028,mp4a.40.2\",RESOLUTION=160x120,AUDIO=\"audio\"\n/video.m3u8\n")
			case "/video.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/v0.ts\n#EXTINF:1.0,\n/v1.ts\n#EXT-X-ENDLIST\n")
			case "/audio.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/a0.aac\n#EXTINF:1.0,\n/a1.aac\n#EXT-X-ENDLIST\n")
			case "/v0.ts":
				v0Reqs.Add(1)
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(videoData0)
			case "/v1.ts":
				v1Reqs.Add(1)
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(videoData1)
			case "/a0.aac":
				a0Reqs.Add(1)
				w.Header().Set("Content-Type", "audio/aac")
				_, _ = w.Write(aac0)
			case "/a1.aac":
				a1Reqs.Add(1)
				w.Header().Set("Content-Type", "audio/aac")
				_, _ = w.Write(aac1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-separate")
		destFile := filepath.Join(t.TempDir(), "output.mp4")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		resolved, err := hlsDownloader.ResolvePlaylistLanguage(ctx, Resource{URL: server.URL + "/master.m3u8"}, 1080, "en")
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=hlsvideoseparate",
			"hlsvideoseparate",
			"HLS Separate Test",
			destFile,
			resolved,
			Selection{MaxHeight: 1080, AudioLanguage: "en"},
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				return Resource{URL: server.URL + "/master.m3u8"}, &Media{ID: "hlsvideoseparate", Title: "HLS Separate Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
			},
			URLRefreshes: 1,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing separate HLS job: %v", err)
		}

		if v0Reqs.Load() != 1 || v1Reqs.Load() != 1 || a0Reqs.Load() != 1 || a1Reqs.Load() != 1 {
			t.Fatalf("unexpected request counts: v0=%d, v1=%d, a0=%d, a1=%d", v0Reqs.Load(), v1Reqs.Load(), a0Reqs.Load(), a1Reqs.Load())
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}
	})

	t.Run("audio-only HLS job makes zero video requests", func(t *testing.T) {
		var videoReqs, audioReqs atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/master.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,URI=\"/audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,CODECS=\"avc1.640028,mp4a.40.2\",RESOLUTION=160x120,AUDIO=\"audio\"\n/video.m3u8\n")
			case "/video.m3u8", "/v0.ts", "/v1.ts":
				videoReqs.Add(1)
				http.NotFound(w, r)
			case "/audio.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/a0.aac\n#EXTINF:1.0,\n/a1.aac\n#EXT-X-ENDLIST\n")
			case "/a0.aac":
				audioReqs.Add(1)
				w.Header().Set("Content-Type", "audio/aac")
				_, _ = w.Write(aac0)
			case "/a1.aac":
				audioReqs.Add(1)
				w.Header().Set("Content-Type", "audio/aac")
				_, _ = w.Write(aac1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-audio-only")
		destFile := filepath.Join(t.TempDir(), "song.mp3")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		track, selectedAudio, isOrig, warn, err := hlsDownloader.ResolveAudioLanguage(ctx, Resource{URL: server.URL + "/master.m3u8"}, "en")
		if err != nil {
			t.Fatal(err)
		}

		spec := AudioOutputSpec{
			RequestedFormat: "mp3",
			ResolvedCodec:   "mp3",
			Container:       "mp3",
			Extension:       ".mp3",
			Encoder:         "libmp3lame",
			Quality:         2,
		}

		manifest, err := CreateHLSAudioJob(
			"https://www.youtube.com/watch?v=hlsaudiotest",
			"hlsaudiotest",
			"HLS Audio Test",
			destFile,
			track,
			selectedAudio,
			isOrig,
			warn,
			spec,
			AudioSelection{AudioLanguage: "en", AudioFormat: "mp3"},
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				return Resource{URL: server.URL + "/master.m3u8"}, &Media{ID: "hlsaudiotest", Title: "HLS Audio Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
			},
			URLRefreshes: 1,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing audio-only HLS job: %v", err)
		}

		if videoReqs.Load() != 0 {
			t.Fatalf("expected 0 video requests in audio-only mode, got %d", videoReqs.Load())
		}
		if audioReqs.Load() != 2 {
			t.Fatalf("expected 2 audio segment requests, got %d", audioReqs.Load())
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination audio file missing or empty: %v", err)
		}
	})

	t.Run("changed signed URLs force isolated presentation restart in gen-2", func(t *testing.T) {
		var token atomic.Value
		token.Store("tok1")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			curTok := token.Load().(string)
			switch r.URL.Path {
			case "/muxed.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/seg0.ts?token=%s\n#EXTINF:1.0,\n/seg1.ts?token=%s\n#EXT-X-ENDLIST\n", curTok, curTok)
			case "/seg0.ts":
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData0)
			case "/seg1.ts":
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-changed-tokens")
		destFile := filepath.Join(t.TempDir(), "output.mp4")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		resolved, err := hlsDownloader.ResolvePlaylist(ctx, Resource{URL: server.URL + "/muxed.m3u8"}, 1080)
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=hlsrestarttest",
			"hlsrestarttest",
			"HLS Restart Test",
			destFile,
			resolved,
			Selection{MaxHeight: 1080},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		// Save segment 0 as completed in gen-1
		track0Dir := filepath.Join(jobDir, "hls", "gen-1", "track-0")
		_ = os.MkdirAll(track0Dir, 0700)
		seg0Path := filepath.Join(track0Dir, "segment-00000.ts")
		_ = os.WriteFile(seg0Path, muxedData0, 0600)
		hash0, size0, _ := ComputeFileSHA256(seg0Path)

		manifest.HLS.Tracks[0].Segments[0].Completed = true
		manifest.HLS.Tracks[0].Segments[0].RelativePath = filepath.Join("hls", "gen-1", "track-0", "segment-00000.ts")
		manifest.HLS.Tracks[0].Segments[0].SizeBytes = size0
		manifest.HLS.Tracks[0].Segments[0].SHA256 = hash0
		manifest.Stage = JobStageDownloading
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		// Now change the token on the server so refreshed URLs have different tokens
		token.Store("tok2")

		var stderrBuf strings.Builder
		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				return Resource{URL: server.URL + "/muxed.m3u8"}, &Media{ID: "hlsrestarttest", Title: "HLS Restart Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
			},
			URLRefreshes: 1,
			Stderr:       &stderrBuf,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing job: %v", err)
		}

		stderrStr := stderrBuf.String()
		if !strings.Contains(stderrStr, "HLS presentation restart (generation 2)") {
			t.Errorf("expected stderr to report generation 2 restart, got:\n%s", stderrStr)
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}
	})

	t.Run("tampered segment detected and redownloaded", func(t *testing.T) {
		var seg0Requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/muxed.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/seg0.ts\n#EXTINF:1.0,\n/seg1.ts\n#EXT-X-ENDLIST\n")
			case "/seg0.ts":
				seg0Requests.Add(1)
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData0)
			case "/seg1.ts":
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-tampered")
		destFile := filepath.Join(t.TempDir(), "output.mp4")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		resolved, err := hlsDownloader.ResolvePlaylist(ctx, Resource{URL: server.URL + "/muxed.m3u8"}, 1080)
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=hlstamperedtest",
			"hlstamperedtest",
			"HLS Tampered Test",
			destFile,
			resolved,
			Selection{MaxHeight: 1080},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		// Save segment 0 with valid metadata in manifest, but write corrupted data on disk
		track0Dir := filepath.Join(jobDir, "hls", "gen-1", "track-0")
		_ = os.MkdirAll(track0Dir, 0700)
		seg0Path := filepath.Join(track0Dir, "segment-00000.ts")
		_ = os.WriteFile(seg0Path, []byte("corrupted bytes here"), 0600)

		manifest.HLS.Tracks[0].Segments[0].Completed = true
		manifest.HLS.Tracks[0].Segments[0].RelativePath = filepath.Join("hls", "gen-1", "track-0", "segment-00000.ts")
		manifest.HLS.Tracks[0].Segments[0].SizeBytes = int64(len(muxedData0))
		manifest.HLS.Tracks[0].Segments[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		manifest.Stage = JobStageDownloading
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				return Resource{URL: server.URL + "/muxed.m3u8"}, &Media{ID: "hlstamperedtest", Title: "HLS Tampered Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
			},
			URLRefreshes: 1,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing job: %v", err)
		}

		// Verify segment 0 was re-requested from server
		if seg0Requests.Load() != 1 {
			t.Fatalf("expected 1 request for tampered segment 0, got %d", seg0Requests.Load())
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}
	})

	t.Run("URL refresh during segment failure uses shared budget", func(t *testing.T) {
		var seg1Attempts atomic.Int32
		var refreshCount atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/muxed.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/seg0.ts?ref=%d\n#EXTINF:1.0,\n/seg1.ts?ref=%d\n#EXT-X-ENDLIST\n", refreshCount.Load(), refreshCount.Load())
			case "/seg0.ts":
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData0)
			case "/seg1.ts":
				att := seg1Attempts.Add(1)
				if att == 1 {
					// Fail with 403 Forbidden to trigger URL refresh
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-refresh-budget")
		destFile := filepath.Join(t.TempDir(), "output.mp4")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		resolved, err := hlsDownloader.ResolvePlaylist(ctx, Resource{URL: server.URL + "/muxed.m3u8"}, 1080)
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=hlsrefreshtest",
			"hlsrefreshtest",
			"HLS Refresh Test",
			destFile,
			resolved,
			Selection{MaxHeight: 1080},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		var stderrBuf strings.Builder
		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				refreshCount.Add(1)
				return Resource{URL: server.URL + "/muxed.m3u8"}, &Media{ID: "hlsrefreshtest", Title: "HLS Refresh Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
			Options: DownloadOptions{
				Resume:     true,
				MaxRetries: 2,
			},
			URLRefreshes: 1,
			Stderr:       &stderrBuf,
		}

		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error executing job with refresh: %v", err)
		}

		stderrStr := stderrBuf.String()
		if !strings.Contains(stderrStr, "Refreshing playback URLs after media access failure") {
			t.Errorf("expected stderr to report URL refresh, got:\n%s", stderrStr)
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}
	})

	t.Run("cancellation preserves recoverable state and allows resume", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/muxed.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:1.0,\n/seg0.ts\n#EXTINF:1.0,\n/seg1.ts\n#EXT-X-ENDLIST\n")
			case "/seg0.ts":
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData0)
			case "/seg1.ts":
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write(muxedData1)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		jobDir := filepath.Join(t.TempDir(), "job-hls-cancel")
		destFile := filepath.Join(t.TempDir(), "output.mp4")

		hlsDownloader, _ := NewHLSDownloader(server.Client(), processor)
		resolved, err := hlsDownloader.ResolvePlaylist(ctx, Resource{URL: server.URL + "/muxed.m3u8"}, 1080)
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=hlscanceltest",
			"hlscanceltest",
			"HLS Cancel Test",
			destFile,
			resolved,
			Selection{MaxHeight: 1080},
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatal(err)
		}

		cancelCtx, cancelFunc := context.WithCancel(ctx)
		cancelFunc() // cancel immediately

		opts := JobRunnerOptions{
			HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
				return Resource{URL: server.URL + "/muxed.m3u8"}, &Media{ID: "hlscanceltest", Title: "HLS Cancel Test"}, nil
			},
			Downloader: NewDownloader(server.Client()),
			Processor:  processor,
			Verifier:   verifier,
		}

		err = ExecuteJob(cancelCtx, jobDir, opts)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}

		// State must remain intact and valid
		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest after cancel: %v", err)
		}
		if loaded.JobID != manifest.JobID {
			t.Fatalf("unexpected job ID: %s", loaded.JobID)
		}

		// Now resume with active context
		err = ExecuteJob(ctx, jobDir, opts)
		if err != nil {
			t.Fatalf("unexpected error resuming canceled job: %v", err)
		}

		info, err := os.Stat(destFile)
		if err != nil || info.Size() == 0 {
			t.Fatalf("destination output file missing or empty: %v", err)
		}
	})
}
