package goyt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStall_ServerStallsBeforeResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("too late"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 50 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected stall error, got nil")
	}
	if !errors.Is(err, ErrDownloadStalled) {
		t.Fatalf("expected ErrDownloadStalled, got: %v", err)
	}
}

func TestStall_HeadersArrive_BodyNeverStarts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(300 * time.Millisecond)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 50 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected stall error, got nil")
	}
	if !errors.Is(err, ErrDownloadStalled) {
		t.Fatalf("expected ErrDownloadStalled, got: %v", err)
	}
}

func TestStall_SomeBytesArrive_ThenTransferStalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("initial chunk of bytes"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(300 * time.Millisecond)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 50 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected stall error, got nil")
	}
	if !errors.Is(err, ErrDownloadStalled) {
		t.Fatalf("expected ErrDownloadStalled, got: %v", err)
	}
}

func TestStall_SlowProgressingTransfer_Succeeds(t *testing.T) {
	// Total transfer takes ~160ms, but each chunk arrives every 20ms.
	// With StallTimeout = 80ms, the transfer must succeed and NOT be flagged as stalled.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "80")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		for i := 0; i < 8; i++ {
			time.Sleep(20 * time.Millisecond)
			_, _ = w.Write([]byte("0123456789"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	res, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 80 * time.Millisecond,
		},
	)

	if err != nil {
		t.Fatalf("slow progressing transfer failed unexpectedly: %v", err)
	}
	if res.SizeBytes != 80 {
		t.Fatalf("size = %d; want 80", res.SizeBytes)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 80 {
		t.Fatalf("file length = %d; want 80", len(data))
	}
}

func TestStall_RetrySuccess(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		if att == 1 {
			// First attempt stalls
			time.Sleep(200 * time.Millisecond)
			return
		}

		w.Header().Set("Content-Length", "11")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	res, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   2,
			RetryDelay:   10 * time.Millisecond,
			StallTimeout: 40 * time.Millisecond,
		},
	)

	if err != nil {
		t.Fatalf("expected retry success, got: %v", err)
	}
	if res.SizeBytes != 11 {
		t.Fatalf("size = %d; want 11", res.SizeBytes)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d; want 2", attempts.Load())
	}
}

func TestStall_StrongETagResume(t *testing.T) {
	var attempts atomic.Int32
	fullContent := "0123456789abcdefghijklmnopqrstuvwxyz" // 36 bytes

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		w.Header().Set("ETag", `"strong-123"`)
		w.Header().Set("Accept-Ranges", "bytes")

		if att == 1 {
			// First attempt: deliver 10 bytes then stall
			w.Header().Set("Content-Length", "36")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fullContent[:10]))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(200 * time.Millisecond)
			return
		}

		// Second attempt: verify Range and If-Range
		rangeHeader := r.Header.Get("Range")
		ifRangeHeader := r.Header.Get("If-Range")

		if rangeHeader != "bytes=10-" || ifRangeHeader != `"strong-123"` {
			http.Error(w, fmt.Sprintf("bad range: %s / %s", rangeHeader, ifRangeHeader), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Range", "bytes 10-35/36")
		w.Header().Set("Content-Length", "26")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(fullContent[10:]))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	res, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			Resume:       true,
			MaxRetries:   2,
			RetryDelay:   10 * time.Millisecond,
			StallTimeout: 40 * time.Millisecond,
		},
	)

	if err != nil {
		t.Fatalf("expected successful resume after stall, got: %v", err)
	}
	if res.SizeBytes != 36 {
		t.Fatalf("size = %d; want 36", res.SizeBytes)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != fullContent {
		t.Fatalf("content = %q; want %q", string(data), fullContent)
	}
}

func TestStall_ChangedRangeRestart(t *testing.T) {
	var attempts atomic.Int32
	content1 := "initial 10 bytes before change"
	content2 := "new complete content from 0 after change!!"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)

		if att == 1 {
			w.Header().Set("ETag", `"etag-v1"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content1)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(content1[:10]))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(200 * time.Millisecond)
			return
		}

		// Second attempt: file changed on server, responds 200 OK with new content
		w.Header().Set("ETag", `"etag-v2"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content2)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(content2))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	res, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			Resume:       true,
			MaxRetries:   2,
			RetryDelay:   10 * time.Millisecond,
			StallTimeout: 40 * time.Millisecond,
		},
	)

	if err != nil {
		t.Fatalf("expected restart on changed file, got: %v", err)
	}
	if res.SizeBytes != int64(len(content2)) {
		t.Fatalf("size = %d; want %d", res.SizeBytes, len(content2))
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content2 {
		t.Fatalf("content = %q; want %q", string(data), content2)
	}
}

func TestStall_RetryExhaustion_PreservesErrDownloadStalled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   2,
			RetryDelay:   5 * time.Millisecond,
			StallTimeout: 30 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDownloadStalled) {
		t.Fatalf("expected errors.Is(err, ErrDownloadStalled), got: %v", err)
	}
}

func TestStall_ParentCancellation_WinsOverStall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel parent context before or immediately during request
	time.AfterFunc(15*time.Millisecond, cancel)

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		ctx,
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 200 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if errors.Is(err, ErrDownloadStalled) {
		t.Fatal("parent cancellation should not be classified as ErrDownloadStalled")
	}
}

func TestStall_ParentDeadline_WinsOverStall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		ctx,
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 200 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
	if errors.Is(err, ErrDownloadStalled) {
		t.Fatal("parent deadline should not be classified as ErrDownloadStalled")
	}
}

func TestStall_DisabledInactivity_RespectsParentCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	_, err := downloader.Download(
		ctx,
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   0,
			StallTimeout: 0, // Disabled stall detection
		},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestStall_InvalidOptions(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(nil)

	// Negative StallTimeout
	_, err := downloader.Download(
		context.Background(),
		Resource{URL: "https://example.com/file"},
		dest,
		DownloadOptions{StallTimeout: -1 * time.Second},
	)
	if err == nil {
		t.Fatal("expected error on negative StallTimeout, got nil")
	}

	// Negative MaxRetries
	_, err = downloader.Download(
		context.Background(),
		Resource{URL: "https://example.com/file"},
		dest,
		DownloadOptions{MaxRetries: -1},
	)
	if err == nil {
		t.Fatal("expected error on negative MaxRetries, got nil")
	}

	// Negative RetryDelay
	_, err = downloader.Download(
		context.Background(),
		Resource{URL: "https://example.com/file"},
		dest,
		DownloadOptions{RetryDelay: -1 * time.Second},
	)
	if err == nil {
		t.Fatal("expected error on negative RetryDelay, got nil")
	}
}

func TestStall_ExistingDestination_SurvivesFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "existing_video.mp4")
	originalContent := "existing valuable video data"
	if err := os.WriteFile(dest, []byte(originalContent), 0600); err != nil {
		t.Fatal(err)
	}

	downloader := NewDownloader(server.Client())
	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   1,
			RetryDelay:   5 * time.Millisecond,
			StallTimeout: 20 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected download failure, got nil")
	}

	// Verify original file was preserved untouched
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != originalContent {
		t.Fatalf("destination corrupted: %q; want %q", string(data), originalContent)
	}
}

func TestStall_ExecutorAndHLS_Validation(t *testing.T) {
	downloader := NewDownloader(nil)
	executor, err := NewExecutor(downloader, nil)
	if err != nil {
		t.Fatal(err)
	}

	plan := &DownloadPlan{
		OutputContainer: "mp4",
		Streams: []Format{
			{ID: "18", Protocol: ProtocolHTTP, Resource: Resource{URL: "https://example.com/v.mp4"}},
		},
	}

	dest := filepath.Join(t.TempDir(), "out.mp4")

	// Negative StallTimeout in Execute
	_, err = executor.Execute(context.Background(), plan, dest, ExecuteOptions{
		Download: DownloadOptions{StallTimeout: -10 * time.Second},
	})
	if err == nil {
		t.Fatal("expected error for negative StallTimeout in Execute, got nil")
	}

	// Negative StallTimeout in ExecuteAudio
	audioPlan := &AudioPlan{
		Stream:     Format{ID: "140", Protocol: ProtocolHTTP, Resource: Resource{URL: "https://example.com/a.m4a"}},
		OutputSpec: AudioOutputSpec{Extension: ".m4a"},
	}
	_, err = executor.ExecuteAudio(context.Background(), audioPlan, filepath.Join(t.TempDir(), "out.m4a"), ExecuteOptions{
		Download: DownloadOptions{StallTimeout: -10 * time.Second},
	})
	if err == nil {
		t.Fatal("expected error for negative StallTimeout in ExecuteAudio, got nil")
	}

	// Negative StallTimeout in HLSDownloader
	hlsDownloader := &HLSDownloader{http: downloader, processor: &FFmpeg{}}
	_, err = hlsDownloader.Download(context.Background(), Resource{URL: "https://example.com/live.m3u8"}, dest, 1080, DownloadOptions{
		StallTimeout: -5 * time.Second,
	}, nil)
	if err == nil {
		t.Fatal("expected error for negative StallTimeout in HLSDownloader.Download, got nil")
	}

	_, err = hlsDownloader.DownloadAudio(context.Background(), Resource{URL: "https://example.com/live.m3u8"}, filepath.Join(t.TempDir(), "out.mp3"), "", AudioOutputSpec{RequestedFormat: "mp3", Extension: ".mp3"}, DownloadOptions{
		StallTimeout: -5 * time.Second,
	}, nil)
	if err == nil {
		t.Fatal("expected error for negative StallTimeout in HLSDownloader.DownloadAudio, got nil")
	}
}

func TestStall_OverallDeadlineIncludesRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()

	dest := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	start := time.Now()
	_, err := downloader.Download(
		ctx,
		Resource{URL: server.URL},
		dest,
		DownloadOptions{
			MaxRetries:   5,
			RetryDelay:   30 * time.Millisecond,
			StallTimeout: 20 * time.Millisecond,
		},
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("overall deadline took too long to terminate: %v", elapsed)
	}
}
