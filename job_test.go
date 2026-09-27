package goyt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobManifestValidation(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "output.mp4")

	t.Run("valid video job manifest", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job1")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateVideoJob(
			"https://www.youtube.com/watch?v=abcdefghijk",
			"abcdefghijk",
			"Test Video",
			nil,
			destPath,
			&DownloadPlan{
				MediaID:         "abcdefghijk",
				Title:           "Test Video",
				OutputContainer: "mp4",
				Streams: []Format{
					{
						ID:        "137",
						Protocol:  "http",
						Container: "mp4",
					},
					{
						ID:        "140",
						Protocol:  "http",
						Container: "m4a",
					},
				},
				NeedsMerge: true,
			},
			Selection{MaxHeight: 1080, Container: "mp4", AllowSeparate: true},
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error creating job: %v", err)
		}

		if err := manifest.Save(jobDir); err != nil {
			t.Fatalf("failed to save manifest: %v", err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}

		if loaded.JobID != manifest.JobID {
			t.Fatalf("got job ID %q, want %q", loaded.JobID, manifest.JobID)
		}
		if len(loaded.Streams) != 2 {
			t.Fatalf("got %d streams, want 2", len(loaded.Streams))
		}
		if loaded.Mode != "video" {
			t.Fatalf("got mode %q, want video", loaded.Mode)
		}
	})

	t.Run("valid audio-only job manifest", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-audio")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		manifest, err := CreateAudioJob(
			"https://www.youtube.com/watch?v=abcdefghijk",
			"abcdefghijk",
			"Test Audio",
			nil,
			filepath.Join(tempDir, "audio.mp3"),
			&AudioPlan{
				Stream: Format{
					ID:        "140",
					Protocol:  "http",
					Container: "m4a",
				},
				OutputSpec: AudioOutputSpec{
					RequestedFormat: "mp3",
					ResolvedCodec:   "mp3",
					Container:       "mp3",
					Extension:       ".mp3",
					Encoder:         "libmp3lame",
					Quality:         2,
				},
			},
			AudioSelection{AudioFormat: "mp3"},
			true,
		)
		if err != nil {
			t.Fatalf("unexpected error creating audio job: %v", err)
		}

		if err := manifest.Save(jobDir); err != nil {
			t.Fatalf("failed to save manifest: %v", err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}

		if loaded.Mode != "audio-only" {
			t.Fatalf("got mode %q, want audio-only", loaded.Mode)
		}
		if loaded.AudioOutputSpec == nil || loaded.AudioOutputSpec.Encoder != "libmp3lame" {
			t.Fatalf("unexpected audio output spec: %+v", loaded.AudioOutputSpec)
		}
		if !loaded.DecodeCheck {
			t.Fatal("expected DecodeCheck to be true")
		}
	})

	t.Run("valid HLS video job manifest (2 tracks)", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-hls-video-2tracks")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		resolved := &ResolvedHLS{
			Video: &HLSTrack{
				Playlist: &HLSPlaylist{
					Segments: []HLSSegment{
						{URL: "https://example.test/v-seg0.ts", Duration: 2 * time.Second},
						{URL: "https://example.test/v-seg1.ts", Duration: 2 * time.Second},
					},
					Duration: 4 * time.Second,
				},
			},
			Audio: &HLSTrack{
				Playlist: &HLSPlaylist{
					Segments: []HLSSegment{
						{URL: "https://example.test/a-seg0.aac", Duration: 2 * time.Second},
						{URL: "https://example.test/a-seg1.aac", Duration: 2 * time.Second},
					},
					Duration: 4 * time.Second,
				},
			},
			SelectedVariant: &HLSVariant{
				Width:     1920,
				Height:    1080,
				Bandwidth: 5000000,
				Codecs:    "avc1.640028,mp4a.40.2",
			},
			SelectedAudio: &HLSAudioRendition{
				GroupID:  "audio",
				Name:     "English",
				Language: "en",
			},
		}

		manifest, err := CreateHLSVideoJob(
			"https://www.youtube.com/watch?v=abcdefghijk",
			"abcdefghijk",
			"HLS Video",
			destPath,
			resolved,
			Selection{MaxHeight: 1080},
			true,
		)
		if err != nil {
			t.Fatalf("unexpected error creating HLS video job: %v", err)
		}

		if err := manifest.Save(jobDir); err != nil {
			t.Fatalf("failed to save manifest: %v", err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}

		if loaded.Transport != "hls" || loaded.Mode != "video" {
			t.Fatalf("unexpected transport/mode: %s / %s", loaded.Transport, loaded.Mode)
		}
		if loaded.HLS == nil || len(loaded.HLS.Tracks) != 2 {
			t.Fatalf("expected 2 HLS tracks, got: %+v", loaded.HLS)
		}
		if loaded.HLS.Tracks[0].Role != "video" || loaded.HLS.Tracks[1].Role != "audio" {
			t.Fatalf("unexpected track roles: %s, %s", loaded.HLS.Tracks[0].Role, loaded.HLS.Tracks[1].Role)
		}
		if len(loaded.HLS.Tracks[0].Segments) != 2 {
			t.Fatalf("expected 2 video segments, got %d", len(loaded.HLS.Tracks[0].Segments))
		}
	})

	t.Run("valid HLS audio-only job manifest", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-hls-audio")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		track := &HLSTrack{
			Playlist: &HLSPlaylist{
				Segments: []HLSSegment{
					{URL: "https://example.test/a-seg0.aac", Duration: 3 * time.Second},
				},
				Duration: 3 * time.Second,
			},
		}

		manifest, err := CreateHLSAudioJob(
			"https://www.youtube.com/watch?v=abcdefghijk",
			"abcdefghijk",
			"HLS Audio",
			filepath.Join(tempDir, "song.mp3"),
			track,
			&HLSAudioRendition{GroupID: "audio", Name: "English", Language: "en"},
			true,
			"",
			AudioOutputSpec{
				RequestedFormat: "mp3",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Encoder:         "libmp3lame",
				Quality:         2,
			},
			AudioSelection{AudioFormat: "mp3"},
			true,
		)
		if err != nil {
			t.Fatalf("unexpected error creating HLS audio job: %v", err)
		}

		if err := manifest.Save(jobDir); err != nil {
			t.Fatalf("failed to save manifest: %v", err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}

		if loaded.Transport != "hls" || loaded.Mode != "audio-only" {
			t.Fatalf("unexpected transport/mode: %s / %s", loaded.Transport, loaded.Mode)
		}
		if loaded.HLS == nil || len(loaded.HLS.Tracks) != 1 || loaded.HLS.Tracks[0].Role != "audio" {
			t.Fatalf("expected 1 audio track, got: %+v", loaded.HLS)
		}
	})

	t.Run("HLS audio-only rejects video track", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-hls-audio-reject-video")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		data := `{
			"schema_version": 1,
			"job_id": "test-hls-invalid",
			"source_url": "https://www.youtube.com/watch?v=abcdefghijk",
			"video_id": "abcdefghijk",
			"destination_path": "` + filepath.Join(tempDir, "audio.mp3") + `",
			"transport": "hls",
			"mode": "audio-only",
			"stage": "planned",
			"hls": {
				"generation": 1,
				"tracks": [
					{
						"index": 0,
						"role": "video",
						"generation": 1,
						"target_duration": 2,
						"duration_seconds": 2.0,
						"segments": [
							{"index": 0, "duration_seconds": 2.0, "url_fingerprint": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "relative_path": "hls/gen-1/track-0/segment-00000.ts"}
						]
					}
				]
			}
		}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}

		_, err := LoadJobManifest(jobDir)
		if err == nil {
			t.Fatal("expected error for audio-only mode with video track, got nil")
		}
		if !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("expected ErrInvalidManifest, got: %v", err)
		}
	})

	t.Run("HLS rejects path traversal in segment path", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-hls-traversal")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		data := `{
			"schema_version": 1,
			"job_id": "test-hls-traversal",
			"source_url": "https://www.youtube.com/watch?v=abcdefghijk",
			"video_id": "abcdefghijk",
			"destination_path": "` + destPath + `",
			"transport": "hls",
			"mode": "video",
			"stage": "planned",
			"hls": {
				"generation": 1,
				"tracks": [
					{
						"index": 0,
						"role": "video",
						"generation": 1,
						"target_duration": 2,
						"duration_seconds": 2.0,
						"segments": [
							{"index": 0, "duration_seconds": 2.0, "url_fingerprint": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "relative_path": "../../etc/passwd"}
						]
					}
				]
			}
		}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}

		_, err := LoadJobManifest(jobDir)
		if err == nil {
			t.Fatal("expected error for segment path traversal, got nil")
		}
		if !errors.Is(err, ErrInvalidJobPath) {
			t.Fatalf("expected ErrInvalidJobPath, got: %v", err)
		}
	})

	t.Run("unsupported manifest version", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-unsupported-version")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		data := `{
			"schema_version": 99,
			"job_id": "test-job-99",
			"source_url": "https://www.youtube.com/watch?v=abcdefghijk",
			"video_id": "abcdefghijk",
			"destination_path": "` + destPath + `",
			"transport": "http",
			"mode": "video",
			"stage": "planned",
			"streams": [{"index": 0, "format": {"id": "18"}, "relative_path": "inputs/stream-0.media"}]
		}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}

		_, err := LoadJobManifest(jobDir)
		if err == nil {
			t.Fatal("expected error for unsupported manifest version, got nil")
		}
		if !errors.Is(err, ErrUnsupportedManifestVersion) {
			t.Fatalf("expected ErrUnsupportedManifestVersion, got: %v", err)
		}
	})

	t.Run("corrupted manifest JSON", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-corrupted")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte("{not valid json..."), 0600); err != nil {
			t.Fatal(err)
		}

		_, err := LoadJobManifest(jobDir)
		if err == nil {
			t.Fatal("expected error for corrupted manifest, got nil")
		}
		if !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("expected ErrInvalidManifest, got: %v", err)
		}
	})

	t.Run("missing manifest file", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-missing")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		_, err := LoadJobManifest(jobDir)
		if err == nil {
			t.Fatal("expected error for missing manifest, got nil")
		}
		if !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("expected ErrJobNotFound, got: %v", err)
		}
	})
}

func TestJobManifestClient(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "output.mp4")

	t.Run("default client is visionos", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-client-default")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		manifest, err := CreateVideoJob(
			"https://www.youtube.com/watch?v=abcdefghijk",
			"abcdefghijk",
			"Test Video",
			nil,
			destPath,
			&DownloadPlan{
				MediaID:         "abcdefghijk",
				Title:           "Test Video",
				OutputContainer: "mp4",
				Streams: []Format{
					{ID: "18", Protocol: "http", Container: "mp4"},
				},
			},
			Selection{MaxHeight: 360, Container: "mp4"},
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error creating job: %v", err)
		}
		if manifest.Client != "visionos" {
			t.Fatalf("expected default client visionos, got %s", manifest.Client)
		}
		if err := manifest.Save(jobDir); err != nil {
			t.Fatalf("failed to save manifest: %v", err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}
		if loaded.Client != "visionos" {
			t.Fatalf("expected loaded client visionos, got %s", loaded.Client)
		}
	})

	t.Run("legacy manifest without client field defaults to visionos", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-legacy")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		data := `{
			"schema_version": 1,
			"job_id": "legacy-job-id",
			"source_url": "https://www.youtube.com/watch?v=abcdefghijk",
			"video_id": "abcdefghijk",
			"destination_path": "` + destPath + `",
			"transport": "http",
			"mode": "video",
			"stage": "planned",
			"streams": [
				{"index": 0, "format": {"id": "18", "protocol": "http", "container": "mp4"}, "relative_path": "inputs/stream-0.media"}
			]
		}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("unexpected error loading legacy manifest: %v", err)
		}
		if loaded.Client != "visionos" {
			t.Fatalf("expected legacy manifest client to default to visionos, got %q", loaded.Client)
		}
	})

	t.Run("manifest with explicit web client preserves client", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-web")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		manifest, err := CreateVideoJob(
			"https://www.youtube.com/watch?v=abcdefghijk",
			"abcdefghijk",
			"Test Video",
			nil,
			destPath,
			&DownloadPlan{
				MediaID:         "abcdefghijk",
				Title:           "Test Video",
				OutputContainer: "mp4",
				Streams: []Format{
					{ID: "18", Protocol: "http", Container: "mp4"},
				},
			},
			Selection{MaxHeight: 360, Container: "mp4"},
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		manifest.Client = "web"
		if err := manifest.Save(jobDir); err != nil {
			t.Fatalf("failed to save manifest: %v", err)
		}

		loaded, err := LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}
		if loaded.Client != "web" {
			t.Fatalf("expected web client, got %s", loaded.Client)
		}
	})

	t.Run("manifest with whitespace client fails validation", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-whitespace-client")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		data := `{
			"schema_version": 1,
			"job_id": "blank-client-job",
			"source_url": "https://www.youtube.com/watch?v=abcdefghijk",
			"video_id": "abcdefghijk",
			"destination_path": "` + destPath + `",
			"transport": "http",
			"client": "   ",
			"mode": "video",
			"stage": "planned",
			"streams": [
				{"index": 0, "format": {"id": "18", "protocol": "http", "container": "mp4"}, "relative_path": "inputs/stream-0.media"}
			]
		}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}

		_, err := LoadJobManifest(jobDir)
		if err == nil {
			t.Fatalf("expected error for whitespace client, got nil")
		}
		if !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("expected ErrInvalidManifest for whitespace client, got %v", err)
		}
	})

	t.Run("ExecuteJob with ClientValidator rejects unsupported client", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "job-unsupported-client-exec")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		data := `{
			"schema_version": 1,
			"job_id": "bad-client-job",
			"source_url": "https://www.youtube.com/watch?v=abcdefghijk",
			"video_id": "abcdefghijk",
			"destination_path": "` + destPath + `",
			"transport": "http",
			"client": "unknown_client",
			"mode": "video",
			"stage": "planned",
			"streams": [
				{"index": 0, "format": {"id": "18", "protocol": "http", "container": "mp4"}, "relative_path": "inputs/stream-0.media"}
			]
		}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}

		err := ExecuteJob(context.Background(), jobDir, JobRunnerOptions{
			ClientValidator: func(c string) error {
				if c != "visionos" && c != "web" {
					return fmt.Errorf("unsupported client %q", c)
				}
				return nil
			},
		})
		if err == nil {
			t.Fatalf("expected error from ExecuteJob with unsupported client, got nil")
		}
		if !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("expected ErrInvalidManifest from ExecuteJob, got %v", err)
		}
	})
}

func TestJobPathTraversalAndSymlinkRejection(t *testing.T) {
	jobDir := t.TempDir()

	t.Run("rejects relative path with parent traversal", func(t *testing.T) {
		_, err := ValidateJobSubpath(jobDir, "../outside.txt")
		if err == nil {
			t.Fatal("expected error for parent directory traversal, got nil")
		}
		if !errors.Is(err, ErrInvalidJobPath) {
			t.Fatalf("expected ErrInvalidJobPath, got: %v", err)
		}
	})

	t.Run("rejects absolute path", func(t *testing.T) {
		_, err := ValidateJobSubpath(jobDir, "/etc/passwd")
		if err == nil {
			t.Fatal("expected error for absolute path, got nil")
		}
		if !errors.Is(err, ErrInvalidJobPath) {
			t.Fatalf("expected ErrInvalidJobPath, got: %v", err)
		}
	})

	t.Run("rejects symlinked file inside job directory", func(t *testing.T) {
		targetFile := filepath.Join(t.TempDir(), "target.txt")
		if err := os.WriteFile(targetFile, []byte("secret"), 0600); err != nil {
			t.Fatal(err)
		}

		symlinkPath := filepath.Join(jobDir, "symlink_file.txt")
		if err := os.Symlink(targetFile, symlinkPath); err != nil {
			t.Skip("symlinks not supported on this environment:", err)
		}

		_, err := ValidateJobSubpath(jobDir, "symlink_file.txt")
		if err == nil {
			t.Fatal("expected error for symlinked file, got nil")
		}
		if !errors.Is(err, ErrInvalidJobPath) {
			t.Fatalf("expected ErrInvalidJobPath, got: %v", err)
		}
	})

	t.Run("accepts clean subpath", func(t *testing.T) {
		target, err := ValidateJobSubpath(jobDir, "inputs/stream-0.media")
		if err != nil {
			t.Fatalf("unexpected error for clean subpath: %v", err)
		}
		expected := filepath.Join(jobDir, "inputs", "stream-0.media")
		if target != expected {
			t.Fatalf("got %q, want %q", target, expected)
		}
	})
}

func TestJobExclusiveLock(t *testing.T) {
	jobDir := t.TempDir()

	lock1, err := AcquireJobLock(jobDir)
	if err != nil {
		t.Fatalf("unexpected error acquiring lock 1: %v", err)
	}

	// Second acquire on the same job must fail with ErrJobLocked
	_, err = AcquireJobLock(jobDir)
	if err == nil {
		t.Fatal("expected error acquiring locked job, got nil")
	}
	if !errors.Is(err, ErrJobLocked) {
		t.Fatalf("expected ErrJobLocked, got: %v", err)
	}

	// Close lock1 and verify lock2 can now be acquired
	if err := lock1.Close(); err != nil {
		t.Fatalf("error closing lock 1: %v", err)
	}

	lock2, err := AcquireJobLock(jobDir)
	if err != nil {
		t.Fatalf("unexpected error acquiring lock 2 after unlock: %v", err)
	}
	_ = lock2.Close()
}

// TestJobLockSubprocess tests that lock is held across separate processes and automatically
// released when the child process terminates or is killed.
func TestJobLockSubprocess(t *testing.T) {
	if os.Getenv("GOYT_TEST_LOCK_HELPER") == "1" {
		jobDir := os.Getenv("GOYT_TEST_LOCK_JOBDIR")
		lock, err := AcquireJobLock(jobDir)
		if err != nil {
			os.Exit(2)
		}
		defer lock.Close()
		// Signal ready to parent by writing file
		_ = os.WriteFile(filepath.Join(jobDir, "ready.txt"), []byte("ready"), 0600)
		time.Sleep(10 * time.Second)
		return
	}

	jobDir := t.TempDir()

	// Start helper subprocess that holds lock
	cmd := exec.Command(os.Args[0], "-test.run=^TestJobLockSubprocess$")
	cmd.Env = append(os.Environ(),
		"GOYT_TEST_LOCK_HELPER=1",
		"GOYT_TEST_LOCK_JOBDIR="+jobDir,
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start lock helper subprocess: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	// Wait for helper to acquire lock and become ready
	readyFile := filepath.Join(jobDir, "ready.txt")
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Attempt to acquire lock while child is running
	_, err := AcquireJobLock(jobDir)
	if err == nil {
		t.Fatal("expected lock collision with running child process, got nil")
	}
	if !errors.Is(err, ErrJobLocked) {
		t.Fatalf("expected ErrJobLocked from child process, got: %v", err)
	}

	// Kill child process abruptly (SIGKILL / Kill)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// Lock must be released by OS immediately upon child death
	lock, err := AcquireJobLock(jobDir)
	if err != nil {
		t.Fatalf("expected lock acquisition after child death, got error: %v", err)
	}
	_ = lock.Close()
}

func TestHTTPInitialMediaNoDuplicateExtraction(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "job-http-no-dup")
	destPath := filepath.Join(t.TempDir(), "output.mp4")

	media := &Media{
		ID:    "httpnodup",
		Title: "HTTP No Dup",
		Formats: []Format{
			{
				ID:        "18",
				Protocol:  "http",
				Container: "mp4",
				Resource: Resource{
					URL: "https://example.test/stream.mp4",
				},
			},
		},
	}

	manifest, err := CreateVideoJob(
		"https://www.youtube.com/watch?v=httpnodup",
		"httpnodup",
		"HTTP No Dup",
		nil,
		destPath,
		&DownloadPlan{
			MediaID:         "httpnodup",
			Title:           "HTTP No Dup",
			OutputContainer: "mp4",
			Streams: []Format{
				media.Formats[0],
			},
			NeedsMerge: false,
			NeedsRemux: false,
		},
		Selection{Container: "mp4"},
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

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to test pre-download extraction phase

	var extractCount atomic.Int32
	opts := JobRunnerOptions{
		Extractor: func(ctx context.Context, u *url.URL) (*Media, error) {
			extractCount.Add(1)
			return media, nil
		},
		InitialMedia: media,
	}

	err = ExecuteJob(cancelCtx, jobDir, opts)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	if count := extractCount.Load(); count != 0 {
		t.Fatalf("expected 0 Extractor calls when InitialMedia is provided, got: %d", count)
	}
}

func TestHLSInitialResolvedTracksNoDuplicateExtraction(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "job-hls-unit-no-dup")
	destFile := filepath.Join(t.TempDir(), "output.mp4")

	baseURL, _ := url.Parse("https://example.test/playlist.m3u8")
	resolved := &ResolvedHLS{
		Video: &HLSTrack{
			BaseURL: baseURL,
			Playlist: &HLSPlaylist{
				Segments: []HLSSegment{
					{URL: "https://example.test/seg0.ts", Duration: 1 * time.Second},
				},
				Duration: 1 * time.Second,
			},
		},
	}

	manifest, err := CreateHLSVideoJob(
		"https://www.youtube.com/watch?v=hlsunitnodup",
		"hlsunitnodup",
		"HLS Unit No Dup",
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

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to test pre-download extraction phase

	var extractCount atomic.Int32
	opts := JobRunnerOptions{
		HLSExtractor: func(ctx context.Context, u *url.URL) (Resource, *Media, error) {
			extractCount.Add(1)
			return Resource{URL: "https://example.test/playlist.m3u8"}, &Media{ID: "hlsunitnodup", Title: "HLS Unit No Dup"}, nil
		},
		InitialResolvedHLSTracks: []*HLSTrack{resolved.Video},
	}

	err = ExecuteJob(cancelCtx, jobDir, opts)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	if count := extractCount.Load(); count != 0 {
		t.Fatalf("expected 0 HLSExtractor calls when InitialResolvedHLSTracks is provided, got: %d", count)
	}

	loaded, err := LoadJobManifest(jobDir)
	if err != nil {
		t.Fatalf("failed to load manifest: %v", err)
	}
	if loaded.HLS.Generation != 1 {
		t.Fatalf("expected generation 1, got: %d", loaded.HLS.Generation)
	}
}
