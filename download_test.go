package goyt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
		{
			name: "plain error",
			err:  errors.New("unexpected EOF"),
			want: "unexpected EOF",
		},
		{
			name: "url error with query parameters",
			err: &url.Error{
				Op:  "Get",
				URL: "https://rr1---sn-abc.googlevideo.com/videoplayback?expire=12345&sig=secrettoken",
				Err: errors.New("read: connection reset by peer"),
			},
			want: "Get: read: connection reset by peer",
		},
		{
			name: "embedded URL in error string",
			err:  errors.New("failed fetching https://secret.host.com/path?token=123: timeout"),
			want: "failed fetching : timeout",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeError(tc.err)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDownloadRetryDiagnostics_RestartNoETag(t *testing.T) {
	const content = "1234567890abcdefghij" // 20 bytes
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		if count == 1 {
			// No ETag header sent on attempt 0; write 10 bytes then abort
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(content[:10]))
			return
		}
		// Attempt 1: full response
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	var retryEvents []RetryDiagnostic
	var progressEvents []int64

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			Resume:     true,
			MaxRetries: 1,
			RetryDelay: time.Millisecond,
			OnProgress: func(p Progress) {
				progressEvents = append(progressEvents, p.DownloadedBytes)
			},
			OnRetry: func(diag RetryDiagnostic) {
				retryEvents = append(retryEvents, diag)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertDownloadFile(t, path, content)

	if len(retryEvents) != 1 {
		t.Fatalf("expected 1 retry event, got %d", len(retryEvents))
	}
	diag := retryEvents[0]
	if diag.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", diag.Attempt)
	}
	if diag.Resumed {
		t.Error("expected Resumed to be false because no strong ETag was provided")
	}
	if diag.PriorOffset != 10 {
		t.Errorf("expected PriorOffset 10, got %d", diag.PriorOffset)
	}
	if !strings.Contains(diag.ActionReason, "strong ETag") {
		t.Errorf("unexpected ActionReason: %s", diag.ActionReason)
	}

	// Verify progress reset happened: progress reached 10, then reset to 0 upon restart
	hasReset := false
	for i := 1; i < len(progressEvents); i++ {
		if progressEvents[i] < progressEvents[i-1] {
			hasReset = true
			break
		}
	}
	if !hasReset {
		t.Errorf("expected progress reset event in progress stream: %v", progressEvents)
	}
}

func TestDownloadRetryDiagnostics_RestartFull200Response(t *testing.T) {
	const content = "1234567890abcdefghij" // 20 bytes
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		w.Header().Set("ETag", `"v1"`)
		if count == 1 {
			// Write 10 bytes then abort
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(content[:10]))
			return
		}
		// Attempt 1: server ignores Range and sends full 200 OK
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	var retryEvents []RetryDiagnostic

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			Resume:     true,
			MaxRetries: 1,
			RetryDelay: time.Millisecond,
			OnRetry: func(diag RetryDiagnostic) {
				retryEvents = append(retryEvents, diag)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertDownloadFile(t, path, content)

	if len(retryEvents) != 1 {
		t.Fatalf("expected 1 retry event, got %d", len(retryEvents))
	}
	diag := retryEvents[0]
	if diag.Resumed {
		t.Error("expected Resumed to be false because server responded with 200 OK")
	}
	if diag.PriorOffset != 10 {
		t.Errorf("expected PriorOffset 10, got %d", diag.PriorOffset)
	}
	if !strings.Contains(diag.ActionReason, "200 OK instead of 206") {
		t.Errorf("unexpected ActionReason: %s", diag.ActionReason)
	}
}

func TestDownloadRetryDiagnostics_ResumePartial206(t *testing.T) {
	const content = "1234567890abcdefghij" // 20 bytes
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		w.Header().Set("ETag", `"v1"`)
		if count == 1 {
			// Attempt 0: write 10 bytes then abort
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(content[:10]))
			return
		}
		// Attempt 1: serve 206 Partial Content
		if r.Header.Get("Range") != "bytes=10-" || r.Header.Get("If-Range") != `"v1"` {
			t.Errorf("missing or incorrect resume headers: Range=%q, If-Range=%q", r.Header.Get("Range"), r.Header.Get("If-Range"))
		}
		w.Header().Set("Content-Range", "bytes 10-19/20")
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(content[10:]))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "video.mp4")
	var retryEvents []RetryDiagnostic
	var progressEvents []int64

	_, err := NewDownloader(server.Client()).Download(
		context.Background(),
		Resource{URL: server.URL},
		path,
		DownloadOptions{
			Resume:     true,
			MaxRetries: 1,
			RetryDelay: time.Millisecond,
			OnProgress: func(p Progress) {
				progressEvents = append(progressEvents, p.DownloadedBytes)
			},
			OnRetry: func(diag RetryDiagnostic) {
				retryEvents = append(retryEvents, diag)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertDownloadFile(t, path, content)

	if len(retryEvents) != 1 {
		t.Fatalf("expected 1 retry event, got %d", len(retryEvents))
	}
	diag := retryEvents[0]
	if !diag.Resumed {
		t.Error("expected Resumed to be true for validated 206 response")
	}
	if diag.PriorOffset != 10 {
		t.Errorf("expected PriorOffset 10, got %d", diag.PriorOffset)
	}

	// Verify progress did NOT reset to 0: monotonically non-decreasing
	for i := 1; i < len(progressEvents); i++ {
		if progressEvents[i] < progressEvents[i-1] {
			t.Fatalf("resumed download should not have progress decrease: %v", progressEvents)
		}
	}
}

func TestDownloadRedirectSafety_RejectedTargetsReceiveZeroRequests(t *testing.T) {
	var rejectedTargetRequests int64
	rejectedTargetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&rejectedTargetRequests, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("untrusted payload"))
	}))
	defer rejectedTargetServer.Close()

	// 1. Insecure redirect from HTTPS to HTTP with pot=<sentinel>: target must receive ZERO requests
	sentinelToken := "SUPER_SECRET_REDIRECT_SENTINEL_POT_9911"
	tlsRedirectServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, rejectedTargetServer.URL+"/insecure?pot="+sentinelToken, http.StatusFound)
	}))
	defer tlsRedirectServer.Close()

	destPath := filepath.Join(t.TempDir(), "target.mp4")
	origCallerClient := tlsRedirectServer.Client()
	if origCallerClient.CheckRedirect != nil {
		t.Fatal("expected nil CheckRedirect on freshly created test client")
	}
	downloader := NewDownloader(origCallerClient)

	// Verify caller-owned client is not mutated by NewDownloader
	if origCallerClient.CheckRedirect != nil {
		t.Fatal("NewDownloader mutated caller-owned http.Client.CheckRedirect")
	}

	_, err := downloader.Download(
		context.Background(),
		Resource{URL: tlsRedirectServer.URL + "/start"},
		destPath,
		DownloadOptions{},
	)
	if err == nil {
		t.Fatal("expected insecure redirect error, got nil")
	}
	sanitized := SanitizeError(err)
	if !strings.Contains(sanitized, "insecure redirect") {
		t.Fatalf("expected 'insecure redirect' error, got: %v", sanitized)
	}
	if strings.Contains(sanitized, sentinelToken) {
		t.Fatalf("sanitized download error leaked sentinel token: %v", sanitized)
	}
	if count := atomic.LoadInt64(&rejectedTargetRequests); count != 0 {
		t.Fatalf("rejected insecure redirect target received %d requests, want 0", count)
	}

	// 2. Redirect with userinfo in target URL: target must receive ZERO requests
	userinfoRedirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parsed, _ := url.Parse(rejectedTargetServer.URL)
		parsed.User = url.UserPassword("attacker", "secret")
		http.Redirect(w, r, parsed.String()+"/userinfo?pot="+sentinelToken, http.StatusFound)
	}))
	defer userinfoRedirectServer.Close()

	destPath2 := filepath.Join(t.TempDir(), "target2.mp4")
	downloader2 := NewDownloader(userinfoRedirectServer.Client())

	_, err2 := downloader2.Download(
		context.Background(),
		Resource{URL: userinfoRedirectServer.URL + "/start"},
		destPath2,
		DownloadOptions{},
	)
	if err2 == nil {
		t.Fatal("expected userinfo redirect error, got nil")
	}
	sanitized2 := SanitizeError(err2)
	if !strings.Contains(sanitized2, "userinfo rejected") {
		t.Fatalf("expected 'userinfo rejected' error, got: %v", sanitized2)
	}
	if strings.Contains(sanitized2, sentinelToken) {
		t.Fatalf("sanitized download error leaked sentinel token: %v", sanitized2)
	}
	if count := atomic.LoadInt64(&rejectedTargetRequests); count != 0 {
		t.Fatalf("rejected userinfo redirect target received %d requests, want 0", count)
	}
}

type downloadTestRoundTripper func(req *http.Request) (*http.Response, error)

func (f downloadTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDownloadRedirectSafety_DestinationPolicyEnforcedOnRedirect(t *testing.T) {
	sentinelToken := "SUPER_SECRET_TOKEN_BEARING_REDIRECT_SENTINEL_9988"
	var untrustedTargetRequests int64
	var initialPermittedRequests int64

	client := &http.Client{
		Transport: downloadTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() == "allowed.example" {
				atomic.AddInt64(&initialPermittedRequests, 1)
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://untrusted.example/media?token=" + sentinelToken},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			}
			if req.URL.Hostname() == "untrusted.example" {
				atomic.AddInt64(&untrustedTargetRequests, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("untrusted payload")),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("404")),
				Request:    req,
			}, nil
		}),
	}

	destPath := filepath.Join(t.TempDir(), "target.mp4")
	downloader := NewDownloader(client)

	policy := func(target *url.URL) error {
		if target.Hostname() != "allowed.example" {
			return fmt.Errorf("host %s not permitted", target.Hostname())
		}
		return nil
	}

	_, err := downloader.Download(
		context.Background(),
		Resource{
			URL:                 "https://allowed.example/media?token=" + sentinelToken,
			ValidateDestination: policy,
		},
		destPath,
		DownloadOptions{},
	)
	if err == nil {
		t.Fatal("expected error for redirect to untrusted host, got nil")
	}

	sanitized := SanitizeError(err)
	if !strings.Contains(sanitized, "untrusted redirect target") {
		t.Fatalf("expected untrusted redirect error, got: %v (sanitized: %s)", err, sanitized)
	}
	if strings.Contains(sanitized, sentinelToken) {
		t.Fatalf("sanitized error leaked sentinel token: %s", sanitized)
	}

	// Assert: Initial permitted host received 1 request
	if initial := atomic.LoadInt64(&initialPermittedRequests); initial != 1 {
		t.Fatalf("initial permitted host expected 1 request, got %d", initial)
	}
	// Assert: Untrusted target received ZERO requests
	if untrusted := atomic.LoadInt64(&untrustedTargetRequests); untrusted != 0 {
		t.Fatalf("untrusted target received %d requests, want 0", untrusted)
	}
}

func TestDownloadRedirectSafety_DestinationPolicyPreservedAcrossHopDroppingParams(t *testing.T) {
	sentinelToken := "SUPER_SECRET_MULTI_HOP_SENTINEL_TOKEN_7744"
	var hop0Requests int64
	var hop1Requests int64
	var hop2UntrustedRequests int64

	client := &http.Client{
		Transport: downloadTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Hostname() {
			case "allowed-1.example":
				atomic.AddInt64(&hop0Requests, 1)
				// Hop 0 redirects to allowed Hop 1, dropping query parameters
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://allowed-2.example/media_hop1"},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			case "allowed-2.example":
				atomic.AddInt64(&hop1Requests, 1)
				// Hop 1 (which has no query params) redirects to untrusted Hop 2
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://untrusted.example/media"},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			case "untrusted.example":
				atomic.AddInt64(&hop2UntrustedRequests, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("untrusted payload")),
					Request:    req,
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("404")),
					Request:    req,
				}, nil
			}
		}),
	}

	destPath := filepath.Join(t.TempDir(), "target_multihop.mp4")
	downloader := NewDownloader(client)

	policy := func(target *url.URL) error {
		if target.Hostname() != "allowed-1.example" && target.Hostname() != "allowed-2.example" {
			return fmt.Errorf("host %s not in allowed set", target.Hostname())
		}
		return nil
	}

	_, err := downloader.Download(
		context.Background(),
		Resource{
			URL:                 "https://allowed-1.example/media?pot=" + sentinelToken,
			ValidateDestination: policy,
		},
		destPath,
		DownloadOptions{},
	)
	if err == nil {
		t.Fatal("expected error on multi-hop redirect to untrusted host, got nil")
	}

	sanitized := SanitizeError(err)
	if !strings.Contains(sanitized, "untrusted redirect target") {
		t.Fatalf("expected untrusted redirect error, got: %v (sanitized: %s)", err, sanitized)
	}

	// Assert: Hop 0 and Hop 1 received requests
	if h0 := atomic.LoadInt64(&hop0Requests); h0 != 1 {
		t.Fatalf("hop 0 expected 1 request, got %d", h0)
	}
	if h1 := atomic.LoadInt64(&hop1Requests); h1 != 1 {
		t.Fatalf("hop 1 expected 1 request, got %d", h1)
	}
	// Assert: Untrusted Hop 2 received ZERO requests
	if h2 := atomic.LoadInt64(&hop2UntrustedRequests); h2 != 0 {
		t.Fatalf("untrusted hop 2 received %d requests, want 0", h2)
	}
}

func TestDownload_GenericOrdinaryDownloadWithPotQueryParamNotRestricted(t *testing.T) {
	var initialRequests int64
	var targetRequests int64
	content := "generic non-token media content bytes"

	client := &http.Client{
		Transport: downloadTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() == "example.com" {
				atomic.AddInt64(&initialRequests, 1)
				// Ordinary redirect between generic HTTPS hosts with a ?pot=example query param
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://cdn.example.org/stream.mp4?pot=example"},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			}
			if req.URL.Hostname() == "cdn.example.org" {
				atomic.AddInt64(&targetRequests, 1)
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: int64(len(content)),
					Body:          io.NopCloser(strings.NewReader(content)),
					Request:       req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("404")),
				Request:    req,
			}, nil
		}),
	}

	destPath := filepath.Join(t.TempDir(), "generic_pot.mp4")
	downloader := NewDownloader(client)

	res, err := downloader.Download(
		context.Background(),
		Resource{URL: "https://example.com/stream.mp4?pot=example"}, // No ValidateDestination policy
		destPath,
		DownloadOptions{},
	)
	if err != nil {
		t.Fatalf("generic download with pot parameter failed unexpectedly: %v", err)
	}
	if res.SizeBytes != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), res.SizeBytes)
	}

	if initial := atomic.LoadInt64(&initialRequests); initial != 1 {
		t.Fatalf("initial generic host expected 1 request, got %d", initial)
	}
	if target := atomic.LoadInt64(&targetRequests); target != 1 {
		t.Fatalf("target generic host expected 1 request, got %d", target)
	}
	assertDownloadFile(t, destPath, content)
}

func TestDownload_InitialDestinationPolicyRejection(t *testing.T) {
	var networkRequests int64
	client := &http.Client{
		Transport: downloadTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt64(&networkRequests, 1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("data")),
				Request:    req,
			}, nil
		}),
	}

	downloader := NewDownloader(client)
	destPath := filepath.Join(t.TempDir(), "rejected.mp4")

	policy := func(target *url.URL) error {
		return errors.New("initial destination rejected by policy")
	}

	_, err := downloader.Download(
		context.Background(),
		Resource{
			URL:                 "https://untrusted.example/stream.mp4",
			ValidateDestination: policy,
		},
		destPath,
		DownloadOptions{},
	)
	if err == nil {
		t.Fatal("expected immediate rejection by destination policy, got nil")
	}
	if !strings.Contains(err.Error(), "invalid destination") {
		t.Fatalf("expected invalid destination error, got: %v", err)
	}
	if networkRequests != 0 {
		t.Fatalf("expected zero network requests for initial policy rejection, got %d", networkRequests)
	}
}
