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

func TestDownloadRejectsUninitializedDownloader(t *testing.T) {
	tests := []struct {
		name       string
		downloader *Downloader
	}{
		{"nil_receiver", nil},
		{"zero_value", &Downloader{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.downloader.Download(
				context.Background(),
				Resource{URL: "https://example.invalid/video"},
				filepath.Join(t.TempDir(), "video.mp4"),
				DownloadOptions{},
			)
			if err == nil {
				t.Fatal("expected constructor error")
			}
		})
	}
}

func TestDownloadExpiredResourceMakesNoRequest(t *testing.T) {
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			fmt.Fprint(w, "unexpected")
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	expired := time.Now().Add(-time.Minute)

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{
			URL:       server.URL,
			ExpiresAt: &expired,
		},
		path,
		DownloadOptions{MaxRetries: 2},
	)

	if !errors.Is(err, ErrResourceExpired) {
		t.Fatalf("got %v, want ErrResourceExpired", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("made %d requests for expired resource", requests.Load())
	}

	assertDownloadFile(t, path, "original")

	if _, err := os.Stat(path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected partial file: %v", err)
	}
}

func TestDownloadInterruptedTransferResumesAndPreservesDestination(
	t *testing.T,
) {
	const content = "hello world"
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `"stable"`)

			switch requests.Add(1) {
			case 1:
				// Advertise the complete body but disconnect after five bytes.
				w.Header().Set("Content-Length", "11")
				fmt.Fprint(w, content[:5])

			case 2:
				if got := r.Header.Get("Range"); got != "bytes=5-" {
					t.Errorf("Range = %q; want bytes=5-", got)
				}
				if got := r.Header.Get("If-Range"); got != `"stable"` {
					t.Errorf("If-Range = %q; want stable ETag", got)
				}

				w.Header().Set("Content-Range", "bytes 5-10/11")
				w.Header().Set("Content-Length", "6")
				w.WriteHeader(http.StatusPartialContent)
				fmt.Fprint(w, content[5:])

			default:
				t.Error("unexpected additional request")
				w.WriteHeader(http.StatusInternalServerError)
			}
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	downloader := NewDownloader(server.Client())
	resource := Resource{URL: server.URL}
	options := DownloadOptions{Resume: true}

	// First call fails but retains the original destination and partial bytes.
	_, err := downloader.Download(
		context.Background(), resource, path, options,
	)
	if err == nil {
		t.Fatal("expected interrupted-transfer error")
	}

	assertDownloadFile(t, path, "original")
	assertDownloadFile(t, path+".part", content[:5])

	// A later call resumes the same resource.
	result, err := downloader.Download(
		context.Background(), resource, path, options,
	)
	if err != nil {
		t.Fatal(err)
	}

	assertDownloadFile(t, path, content)
	if result.SizeBytes != int64(len(content)) {
		t.Fatalf("incorrect completed size: %d", result.SizeBytes)
	}
	if requests.Load() != 2 {
		t.Fatalf("got %d requests; want 2", requests.Load())
	}

	for _, suffix := range []string{".part", ".part.json"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("checkpoint %s remains: %v", suffix, err)
		}
	}
}

func TestDownloadForbiddenDoesNotRetryOrReplaceDestination(t *testing.T) {
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusForbidden)
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			MaxRetries: 3,
			RetryDelay: time.Millisecond,
		},
	)
	if err == nil {
		t.Fatal("expected HTTP 403 error")
	}
	if requests.Load() != 1 {
		t.Fatalf("got %d requests; want 1", requests.Load())
	}

	assertDownloadFile(t, path, "original")
}

func TestDownloadCancellationPreservesExistingDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.Header().Set("ETag", `"stable"`)
			fmt.Fprint(w, "partial")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		},
	))
	defer server.Close()

	// Safety deadline prevents a broken cancellation path hanging the test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewDownloader(server.Client()).Download(
		ctx,
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			Resume: true,
			OnProgress: func(p Progress) {
				if p.DownloadedBytes > 0 {
					cancel()
				}
			},
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v; want context.Canceled", err)
	}

	assertDownloadFile(t, path, "original")

	info, err := os.Stat(path + ".part")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("expected retained partial bytes")
	}
}
