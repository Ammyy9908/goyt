package goyt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func assertDownloadFile(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != want {
		t.Fatalf("got %q, want %q", data, want)
	}
}

func TestDownloadSuccess(t *testing.T) {
	const content = "hello video"

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			fmt.Fprint(w, content)
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	var last Progress

	result, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			OnProgress: func(p Progress) {
				last = p
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertDownloadFile(t, path, content)

	if result.SizeBytes != int64(len(content)) ||
		last.DownloadedBytes != int64(len(content)) {
		t.Fatal("incorrect size or progress")
	}

	if _, err := os.Stat(path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial file should be removed after success")
	}
}

func TestDownloadResume(t *testing.T) {
	const content = "hello world"

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") != "bytes=5-" ||
				r.Header.Get("If-Range") != `"v1"` {
				t.Error("missing resume headers")
			}

			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Range", "bytes 5-10/11")
			w.Header().Set("Content-Length", "6")
			w.WriteHeader(http.StatusPartialContent)
			fmt.Fprint(w, content[5:])
		},
	))
	defer server.Close()

	resource := Resource{URL: server.URL}
	path := filepath.Join(t.TempDir(), "video.mp4")

	if err := os.WriteFile(path+".part", []byte(content[:5]), 0600); err != nil {
		t.Fatal(err)
	}

	if err := saveResume(path+".part.json", resumeState{
		Key:   resourceKey(resource),
		ETag:  `"v1"`,
		Total: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		resource,
		path,
		DownloadOptions{Resume: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertDownloadFile(t, path, content)
}

func TestDownloadRestartsOnFullResponse(t *testing.T) {
	for _, etag := range []string{`"v1"`, `"v2"`} {
		t.Run(etag, func(t *testing.T) {
			const content = "replacement content"

			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					// Simulates ignored Range or a changed resource.
					w.Header().Set("ETag", etag)
					fmt.Fprint(w, content)
				},
			))
			defer server.Close()

			resource := Resource{URL: server.URL}
			path := filepath.Join(t.TempDir(), "video.mp4")

			if err := os.WriteFile(path+".part", []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}

			if err := saveResume(path+".part.json", resumeState{
				Key:   resourceKey(resource),
				ETag:  `"v1"`,
				Total: 100,
			}); err != nil {
				t.Fatal(err)
			}

			_, err := NewDownloader(server.Client()).Download(
				context.Background(),
				resource,
				path,
				DownloadOptions{Resume: true},
			)
			if err != nil {
				t.Fatal(err)
			}

			assertDownloadFile(t, path, content)
		})
	}
}

func TestDownloadRetries(t *testing.T) {
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if requests.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, "ok")
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			MaxRetries: 1,
			RetryDelay: time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if requests.Load() != 2 {
		t.Fatalf("got %d requests, want 2", requests.Load())
	}

	assertDownloadFile(t, path, "ok")
}

func TestDownloadTruncatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "short")
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{},
	)
	if err == nil {
		t.Fatal("expected truncated response error")
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete download must not become the final file")
	}
}

func TestDownloadCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, strings.Repeat("x", 1024))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		},
	))
	defer server.Close()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	defer cancel()

	path := filepath.Join(t.TempDir(), "video.mp4")

	_, err := NewDownloader(server.Client()).Download(
		ctx,
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			OnProgress: func(p Progress) {
				if p.DownloadedBytes > 0 {
					cancel()
				}
			},
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled download must not become the final file")
	}
}
