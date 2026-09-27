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
