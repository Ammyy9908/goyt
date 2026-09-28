package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ammyy9908/goyt"
)

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

// mockPOTokenProvider implements POTokenProvider for testing.
type mockPOTokenProvider struct {
	mu           sync.Mutex
	calls        []POTokenRequest
	tokenFunc    func(ctx context.Context, req POTokenRequest) (POTokenResult, error)
	callCountMap map[string]int
}

func newMockPOTokenProvider(fn func(ctx context.Context, req POTokenRequest) (POTokenResult, error)) *mockPOTokenProvider {
	return &mockPOTokenProvider{
		tokenFunc:    fn,
		callCountMap: make(map[string]int),
	}
}

func (m *mockPOTokenProvider) GetPOToken(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
	if err := ctx.Err(); err != nil {
		return POTokenResult{}, err
	}
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.callCountMap[req.ScopeKey()]++
	fn := m.tokenFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return POTokenResult{
		Token:       "mock-po-token-value",
		Client:      req.Client,
		Context:     req.Context,
		VideoID:     req.VideoID,
		VisitorData: req.VisitorData,
	}, nil
}

func (m *mockPOTokenProvider) Calls() []POTokenRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]POTokenRequest, len(m.calls))
	copy(res, m.calls)
	return res
}

func (m *mockPOTokenProvider) TotalCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func mockPlayerJSON(videoID string, itags ...int) string {
	var formats []string
	for _, itag := range itags {
		formats = append(formats, fmt.Sprintf(`{
			"itag": %d,
			"mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"",
			"width": 640,
			"height": 360,
			"url": "https://rr1---sn-test.googlevideo.com/videoplayback?itag=%d&id=%s"
		}`, itag, itag, videoID))
	}
	return fmt.Sprintf(`{
		"videoDetails": {
			"videoId": %q,
			"title": "Test Video",
			"lengthSeconds": "120"
		},
		"playabilityStatus": {
			"status": "OK"
		},
		"streamingData": {
			"formats": [%s]
		}
	}`, videoID, strings.Join(formats, ","))
}

func mockWatchHTMLWithVisitor(videoID, visitorData string, itags ...int) string {
	playerJSON := mockPlayerJSON(videoID, itags...)
	return fmt.Sprintf(`<!DOCTYPE html><html><head>
		<script>var ytInitialPlayerResponse = %s;</script>
		<script>ytcfg.set({"VISITOR_DATA":%q});</script>
	</head><body></body></html>`, playerJSON, visitorData)
}

func TestPOToken_ValidationAndScope(t *testing.T) {
	req := POTokenRequest{
		Client:      ClientWeb,
		Context:     POTokenContextGVS,
		VideoID:     "dQw4w9WgXcQ",
		VisitorData: "visitor-token-123",
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected valid request, got: %v", err)
	}

	// 1. Valid result
	now := time.Now()
	exp := now.Add(1 * time.Hour)
	validRes := POTokenResult{
		Token:       "sample.po.token.12345",
		Client:      ClientWeb,
		Context:     POTokenContextGVS,
		VideoID:     "dQw4w9WgXcQ",
		VisitorData: "visitor-token-123",
		ExpiresAt:   &exp,
	}
	if err := validRes.ValidateFor(req, now); err != nil {
		t.Fatalf("expected valid result, got: %v", err)
	}

	// 2. Expired result
	pastExp := now.Add(-5 * time.Minute)
	expiredRes := validRes
	expiredRes.ExpiresAt = &pastExp
	if err := expiredRes.ValidateFor(req, now); !errors.Is(err, ErrPOTokenExpired) {
		t.Fatalf("expected ErrPOTokenExpired, got: %v", err)
	}

	// 3. Client mismatch
	mismatchClient := validRes
	mismatchClient.Client = ClientVisionOS
	if err := mismatchClient.ValidateFor(req, now); !errors.Is(err, ErrPOTokenScopeMismatch) {
		t.Fatalf("expected ErrPOTokenScopeMismatch for client, got: %v", err)
	}

	// 4. Context mismatch
	mismatchContext := validRes
	mismatchContext.Context = POTokenContextPlayer
	if err := mismatchContext.ValidateFor(req, now); !errors.Is(err, ErrPOTokenScopeMismatch) {
		t.Fatalf("expected ErrPOTokenScopeMismatch for context, got: %v", err)
	}

	// 5. VideoID mismatch
	mismatchVideo := validRes
	mismatchVideo.VideoID = "different11x"
	if err := mismatchVideo.ValidateFor(req, now); !errors.Is(err, ErrPOTokenScopeMismatch) {
		t.Fatalf("expected ErrPOTokenScopeMismatch for video ID, got: %v", err)
	}

	// 6. VisitorData mismatch
	mismatchVisitor := validRes
	mismatchVisitor.VisitorData = "other-visitor"
	if err := mismatchVisitor.ValidateFor(req, now); !errors.Is(err, ErrPOTokenScopeMismatch) {
		t.Fatalf("expected ErrPOTokenScopeMismatch for visitor data, got: %v", err)
	}

	// 7. Empty or invalid token
	emptyToken := validRes
	emptyToken.Token = "   "
	if err := emptyToken.ValidateFor(req, now); !errors.Is(err, ErrPOTokenInvalid) {
		t.Fatalf("expected ErrPOTokenInvalid for empty token, got: %v", err)
	}

	// 8. Control characters in token
	badToken := validRes
	badToken.Token = "token\nwith\rnewlines"
	if err := badToken.ValidateFor(req, now); !errors.Is(err, ErrPOTokenInvalid) {
		t.Fatalf("expected ErrPOTokenInvalid for control chars, got: %v", err)
	}

	// 9. Oversized token
	hugeToken := validRes
	hugeToken.Token = strings.Repeat("A", MaxPOTokenLength+1)
	if err := hugeToken.ValidateFor(req, now); !errors.Is(err, ErrPOTokenInvalid) {
		t.Fatalf("expected ErrPOTokenInvalid for oversized token, got: %v", err)
	}
}

func TestPOToken_NoProviderPreservesExistingBehavior(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	extractor := New(client)
	if extractor.POTokenProvider() != nil {
		t.Fatal("expected nil POTokenProvider by default")
	}

	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	media, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected extraction error without provider: %v", err)
	}
	if len(media.Formats) != 1 {
		t.Fatalf("expected 1 format, got %d", len(media.Formats))
	}
	// Verify no pot param is appended
	if strings.Contains(media.Formats[0].Resource.URL, "pot=") {
		t.Fatalf("format URL should not contain pot param when no provider is configured: %s", media.Formats[0].Resource.URL)
	}
}

func TestPOToken_ProviderCalledForWebGVSAndPlacedCorrectly(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	visitor := "test-visitor-token-abc"
	watchHTML := mockWatchHTMLWithVisitor(videoID, visitor, 18, 22)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	expectedToken := "verified-gvs-po-token-9988"
	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		if req.Context == POTokenContextGVS {
			return POTokenResult{
				Token:       expectedToken,
				Client:      req.Client,
				Context:     req.Context,
				VideoID:     req.VideoID,
				VisitorData: req.VisitorData,
			}, nil
		}
		return POTokenResult{}, ErrPOTokenUnavailable
	})

	extractor := New(client, WithPOTokenProvider(provider))
	if extractor.POTokenProvider() == nil {
		t.Fatal("expected configured POTokenProvider")
	}

	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	media, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected extraction error: %v", err)
	}

	if len(media.Formats) != 2 {
		t.Fatalf("expected 2 formats, got %d", len(media.Formats))
	}

	// Verify provider was called once for GVS (same-scope extraction reuse)
	calls := provider.Calls()
	gvsCalls := 0
	for _, c := range calls {
		if c.Context == POTokenContextGVS {
			gvsCalls++
			if c.Client != ClientWeb {
				t.Errorf("expected client web, got %s", c.Client)
			}
			if c.VideoID != videoID {
				t.Errorf("expected video ID %s, got %s", videoID, c.VideoID)
			}
			if c.VisitorData != visitor {
				t.Errorf("expected visitor data %s, got %s", visitor, c.VisitorData)
			}
		}
	}
	if gvsCalls != 1 {
		t.Fatalf("expected exactly 1 GVS provider call for extraction scope, got %d", gvsCalls)
	}

	// Verify pot param on all googlevideo.com URLs
	for _, f := range media.Formats {
		parsed, err := url.Parse(f.Resource.URL)
		if err != nil {
			t.Fatalf("failed to parse format URL: %v", err)
		}
		potVal := parsed.Query().Get("pot")
		if potVal != expectedToken {
			t.Errorf("format %s pot param mismatch: got %q, want %q (URL: %s)", f.ID, potVal, expectedToken, f.Resource.URL)
		}
	}
}

func TestPOToken_VisionOSDoesNotCallProvider(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	playerJSON := mockPlayerJSON(videoID, 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`<html><script>ytcfg.set({"VISITOR_DATA":"vis-1"});</script></html>`))),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/youtubei/v1/player") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(playerJSON)),
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

	provider := newMockPOTokenProvider(nil)
	extractor := New(client, WithPOTokenProvider(provider))

	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	media, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL, ClientVisionOS)
	if err != nil {
		t.Fatalf("unexpected visionos extraction error: %v", err)
	}
	if len(media.Formats) != 1 {
		t.Fatalf("expected 1 format, got %d", len(media.Formats))
	}

	// Provider should not be called for visionos
	if provider.TotalCalls() != 0 {
		t.Fatalf("expected 0 provider calls for visionos, got %d", provider.TotalCalls())
	}
}

func TestPOToken_DefaultInspectionDoesNotCallProvider(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	provider := newMockPOTokenProvider(nil)
	extractor := New(client, WithPOTokenProvider(provider))

	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	report, err := extractor.Inspect(context.Background(), testURL)
	if err != nil {
		t.Fatalf("unexpected inspect error: %v", err)
	}
	if report == nil || len(report.Formats) != 1 {
		t.Fatalf("expected 1 format in report, got %+v", report)
	}

	if provider.TotalCalls() != 0 {
		t.Fatalf("expected 0 provider calls during default inspection, got %d", provider.TotalCalls())
	}
}

func TestPOToken_ExpiredResultFailsExtractionWithTypedError(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	pastExp := time.Now().Add(-10 * time.Minute)
	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       "expired-token-value",
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
			ExpiresAt:   &pastExp,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	_, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL, ClientWeb)
	if err == nil {
		t.Fatal("expected error for expired PO-token, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got: %T (%v)", err, err)
	}
	if extErr.Code != ErrCodePOTokenExpired {
		t.Fatalf("expected error code %s, got: %s", ErrCodePOTokenExpired, extErr.Code)
	}
	// Check privacy: token value must not be in error message
	if strings.Contains(extErr.Error(), "expired-token-value") {
		t.Fatalf("error message leaked token secret: %s", extErr.Error())
	}
}

func TestPOToken_ScopeMismatchFailsExtractionWithTypedError(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	// Return result for a different video ID
	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       "mismatched-scope-token",
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     "differentVid",
			VisitorData: req.VisitorData,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	_, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL, ClientWeb)
	if err == nil {
		t.Fatal("expected error for scope mismatch, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got: %T (%v)", err, err)
	}
	if extErr.Code != ErrCodePOTokenScopeMismatch {
		t.Fatalf("expected error code %s, got: %s", ErrCodePOTokenScopeMismatch, extErr.Code)
	}
}

func TestPOToken_ProviderFailureYieldsTypedError(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{}, errors.New("underlying generator crash: connection refused")
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	_, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL, ClientWeb)
	if err == nil {
		t.Fatal("expected error for provider failure, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got: %T (%v)", err, err)
	}
	if extErr.Code != ErrCodePOTokenProviderFailed {
		t.Fatalf("expected error code %s, got: %s", ErrCodePOTokenProviderFailed, extErr.Code)
	}
}

func TestPOToken_ScopeChangeReacquiresToken(t *testing.T) {
	videoID1 := "dQw4w9WgXcQ"
	videoID2 := "oHg5SJYRHA0"
	watchHTML1 := mockWatchHTMLWithVisitor(videoID1, "visitor-1", 18)
	watchHTML2 := mockWatchHTMLWithVisitor(videoID2, "visitor-2", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				body := watchHTML1
				if strings.Contains(req.URL.RawQuery, videoID2) {
					body = watchHTML2
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(body)),
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

	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       "token-for-" + req.VideoID,
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))

	// 1. Extract Video 1
	testURL1, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID1)
	m1, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL1, ClientWeb)
	if err != nil {
		t.Fatalf("extraction 1 failed: %v", err)
	}
	if !strings.Contains(m1.Formats[0].Resource.URL, "pot=token-for-"+videoID1) {
		t.Fatalf("unexpected URL for video 1: %s", m1.Formats[0].Resource.URL)
	}

	// 2. Extract Video 2 (should reacquire with Video 2 scope)
	testURL2, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID2)
	m2, err := extractor.ExtractDownloadableWithClient(context.Background(), testURL2, ClientWeb)
	if err != nil {
		t.Fatalf("extraction 2 failed: %v", err)
	}
	if !strings.Contains(m2.Formats[0].Resource.URL, "pot=token-for-"+videoID2) {
		t.Fatalf("unexpected URL for video 2: %s", m2.Formats[0].Resource.URL)
	}

	// Verify provider was called for each distinct video scope
	calls := provider.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 distinct provider calls, got %d", len(calls))
	}
	if calls[0].VideoID != videoID1 || calls[1].VideoID != videoID2 {
		t.Fatalf("calls scope mismatch: %+v", calls)
	}
}

func TestPOToken_ContextCancellation(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		select {
		case <-ctx.Done():
			return POTokenResult{}, ctx.Err()
		case <-time.After(1 * time.Second):
			return POTokenResult{Token: "late-token"}, nil
		}
	})

	extractor := New(client, WithPOTokenProvider(provider))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)
	_, err := extractor.ExtractDownloadableWithClient(ctx, testURL, ClientWeb)
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
}

func TestPOToken_ConcurrentExtractionsThreadSafety(t *testing.T) {
	var requestCount int64
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt64(&requestCount, 1)
			q := req.URL.Query()
			vid := q.Get("v")
			if vid == "" {
				vid = "dQw4w9WgXcQ"
			}
			watchHTML := mockWatchHTMLWithVisitor(vid, "vis-"+vid, 18)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
				Request:    req,
			}, nil
		}),
	}

	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       "token-" + req.VideoID,
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))

	const routines = 15
	var wg sync.WaitGroup
	errChan := make(chan error, routines)

	for i := 0; i < routines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			vid := fmt.Sprintf("testVideo%02d", idx)
			u, _ := url.Parse("https://www.youtube.com/watch?v=" + vid)
			res, err := extractor.ExtractDownloadableResult(context.Background(), u, ClientWeb)
			if err != nil {
				errChan <- fmt.Errorf("routine %d failed: %w", idx, err)
				return
			}
			if len(res.Media.Formats) != 1 {
				errChan <- fmt.Errorf("routine %d expected 1 format, got %d", idx, len(res.Media.Formats))
				return
			}
			expectedPot := "pot=token-" + vid
			if !strings.Contains(res.Media.Formats[0].Resource.URL, expectedPot) {
				errChan <- fmt.Errorf("routine %d URL missing expected pot param %q: %s", idx, expectedPot, res.Media.Formats[0].Resource.URL)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Fatal(err)
	}
}

func TestPOToken_NonGooglevideoHostDoesNotReceivePotParam(t *testing.T) {
	rawURL := "https://example.com/stream.mp4"
	potVal := "sensitive-token-1234"
	applied := applyGVSPOToken(rawURL, potVal)
	if strings.Contains(applied, "pot=") {
		t.Fatalf("non-googlevideo host URL must not receive pot parameter: %s", applied)
	}

	// HTTP URLs should not receive pot param
	httpURL := "http://rr1---sn-test.googlevideo.com/videoplayback?itag=18"
	appliedHTTP := applyGVSPOToken(httpURL, potVal)
	if strings.Contains(appliedHTTP, "pot=") {
		t.Fatalf("insecure HTTP googlevideo URL must not receive pot parameter: %s", appliedHTTP)
	}

	// Lookalike host domains should not receive pot param
	lookalikeURL := "https://evilgooglevideo.com/videoplayback?itag=18"
	appliedLookalike := applyGVSPOToken(lookalikeURL, potVal)
	if strings.Contains(appliedLookalike, "pot=") {
		t.Fatalf("lookalike host URL must not receive pot parameter: %s", appliedLookalike)
	}

	// Subdomain suffix attack should not receive pot param
	suffixAttackURL := "https://googlevideo.com.attacker.com/videoplayback?itag=18"
	appliedSuffix := applyGVSPOToken(suffixAttackURL, potVal)
	if strings.Contains(appliedSuffix, "pot=") {
		t.Fatalf("suffix attack URL must not receive pot parameter: %s", appliedSuffix)
	}

	// URL with userinfo should not receive pot param
	userinfoURL := "https://user:pass@rr1---sn-test.googlevideo.com/videoplayback?itag=18"
	appliedUserinfo := applyGVSPOToken(userinfoURL, potVal)
	if strings.Contains(appliedUserinfo, "pot=") {
		t.Fatalf("URL with userinfo must not receive pot parameter: %s", appliedUserinfo)
	}

	// Valid HTTPS googlevideo.com host
	gvURL := "https://rr1---sn-test.googlevideo.com/videoplayback?itag=18&expire=12345"
	appliedGV := applyGVSPOToken(gvURL, potVal)
	if !strings.Contains(appliedGV, "pot="+potVal) {
		t.Fatalf("googlevideo URL must receive pot parameter: %s", appliedGV)
	}
	if !strings.Contains(appliedGV, "expire=12345") {
		t.Fatalf("googlevideo URL must preserve existing query parameters: %s", appliedGV)
	}
}

func TestPOToken_RequestPlayerWithPOTokenPlacement(t *testing.T) {
	var capturedBody []byte
	var capturedHeader http.Header

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/player") {
				var buf bytes.Buffer
				_, _ = io.Copy(&buf, req.Body)
				capturedBody = buf.Bytes()
				capturedHeader = req.Header.Clone()
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(mockPlayerJSON("dQw4w9WgXcQ", 18))),
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

	extractor := New(client)
	poTokenVal := "verified-player-po-token-7766"
	profile := webProfile()

	// 1. With PO-Token supplied
	_, err := extractor.requestPlayerWithPOToken(context.Background(), "dQw4w9WgXcQ", profile, poTokenVal, "vis-123")
	if err != nil {
		t.Fatalf("requestPlayerWithPOToken failed: %v", err)
	}

	// Decode into a generic map to check JSON hierarchy
	var rootMap map[string]any
	if err := json.Unmarshal(capturedBody, &rootMap); err != nil {
		t.Fatalf("failed to unmarshal captured player request: %v\nBody was:\n%s", err, string(capturedBody))
	}

	// Assert: serviceIntegrityDimensions.poToken exists at the root
	sidRaw, hasSIDRoot := rootMap["serviceIntegrityDimensions"]
	if !hasSIDRoot {
		t.Fatalf("expected serviceIntegrityDimensions at JSON root\nBody was:\n%s", string(capturedBody))
	}
	sidMap, ok := sidRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected serviceIntegrityDimensions to be a JSON object, got %T", sidRaw)
	}
	if sidMap["poToken"] != poTokenVal {
		t.Fatalf("expected root serviceIntegrityDimensions.poToken = %q, got %q", poTokenVal, sidMap["poToken"])
	}

	// Assert: serviceIntegrityDimensions does NOT appear under context
	ctxRaw, hasCtx := rootMap["context"]
	if !hasCtx {
		t.Fatalf("expected context at JSON root")
	}
	ctxMap, ok := ctxRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected context to be a JSON object, got %T", ctxRaw)
	}
	if _, hasSIDInCtx := ctxMap["serviceIntegrityDimensions"]; hasSIDInCtx {
		t.Fatalf("serviceIntegrityDimensions MUST NOT appear under context object\nBody was:\n%s", string(capturedBody))
	}

	// Assert: Visitor header remains consistent
	if capturedHeader.Get("X-Goog-Visitor-Id") != "vis-123" {
		t.Fatalf("expected X-Goog-Visitor-Id = %q, got %q", "vis-123", capturedHeader.Get("X-Goog-Visitor-Id"))
	}

	// 2. Without PO-Token supplied (poToken == "")
	var capturedBodyNoToken []byte
	clientNoToken := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/player") {
				var buf bytes.Buffer
				_, _ = io.Copy(&buf, req.Body)
				capturedBodyNoToken = buf.Bytes()
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(mockPlayerJSON("dQw4w9WgXcQ", 18))),
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

	extractorNoToken := New(clientNoToken)
	_, err = extractorNoToken.requestPlayerWithPOToken(context.Background(), "dQw4w9WgXcQ", profile, "", "vis-123")
	if err != nil {
		t.Fatalf("requestPlayerWithPOToken without token failed: %v", err)
	}

	var rootMapNoToken map[string]any
	if err := json.Unmarshal(capturedBodyNoToken, &rootMapNoToken); err != nil {
		t.Fatalf("failed to unmarshal captured player request: %v\nBody was:\n%s", err, string(capturedBodyNoToken))
	}

	// Assert: serviceIntegrityDimensions is completely omitted when no token is supplied
	if _, hasSID := rootMapNoToken["serviceIntegrityDimensions"]; hasSID {
		t.Fatalf("serviceIntegrityDimensions should be omitted when no token is supplied\nBody was:\n%s", string(capturedBodyNoToken))
	}
}

func TestPOToken_Privacy_ManifestsAndDiagnosticsOmitTokens(t *testing.T) {
	sentinelToken := "SUPER_SECRET_POT_TOKEN_XYZ_9999"
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       sentinelToken,
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)

	res, err := extractor.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// 1. Diagnostics must not contain token secret
	for k, d := range res.Diagnostics {
		diagStr := d.String()
		if strings.Contains(diagStr, sentinelToken) {
			t.Fatalf("diagnostic %s contains token secret: %s", k, diagStr)
		}
	}

	// 2. Extractor AllFormatDiagnostics must not contain token secret
	for k, d := range extractor.AllFormatDiagnostics() {
		diagStr := d.String()
		if strings.Contains(diagStr, sentinelToken) {
			t.Fatalf("extractor diagnostic %s contains token secret: %s", k, diagStr)
		}
	}

	// 3. Error strings must not contain token secret
	failProvider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{}, fmt.Errorf("generator exploded with token=%s", sentinelToken)
	})
	extractorFail := New(client, WithPOTokenProvider(failProvider))
	_, failErr := extractorFail.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if failErr == nil {
		t.Fatal("expected failure error, got nil")
	}
	if strings.Contains(failErr.Error(), sentinelToken) {
		t.Fatalf("public error message leaked token secret: %s", failErr.Error())
	}
}

func TestPOToken_UnknownExpiryTokensNotReusedAcrossExtractions(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	callCount := 0
	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		callCount++
		return POTokenResult{
			Token:       fmt.Sprintf("unknown-expiry-token-%d", callCount),
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
			ExpiresAt:   nil, // Unknown expiry must not be cached across extractions
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)

	// Extraction 1
	m1, err := extractor.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("extraction 1 failed: %v", err)
	}
	if !strings.Contains(m1.Media.Formats[0].Resource.URL, "pot=unknown-expiry-token-1") {
		t.Fatalf("expected token 1 in format URL: %s", m1.Media.Formats[0].Resource.URL)
	}
	if callCount != 1 {
		t.Fatalf("expected provider call count 1, got %d", callCount)
	}

	// Extraction 2 (same extractor, same video)
	m2, err := extractor.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("extraction 2 failed: %v", err)
	}
	if !strings.Contains(m2.Media.Formats[0].Resource.URL, "pot=unknown-expiry-token-2") {
		t.Fatalf("expected token 2 in format URL (not cached across extractions): %s", m2.Media.Formats[0].Resource.URL)
	}
	if callCount != 2 {
		t.Fatalf("expected provider call count 2 after second extraction, got %d", callCount)
	}
}

func TestPOToken_GVSResourceValidateDestinationScope(t *testing.T) {
	// 1. Valid HTTPS Google Video targets
	validURLs := []string{
		"https://googlevideo.com/videoplayback",
		"https://rr1---sn-test.googlevideo.com/videoplayback",
		"https://r1---sn-vgqsrn7e.googlevideo.com/videoplayback?itag=18",
	}
	for _, raw := range validURLs {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("failed to parse url %q: %v", raw, err)
		}
		if err := validateGVSDestination(u); err != nil {
			t.Errorf("expected valid destination for %q, got: %v", raw, err)
		}
	}

	// 2. Prohibited targets: HTTP, userinfo, custom ports, youtube.com subdomains, youtu.be, untrusted hosts
	invalidURLs := []struct {
		url    string
		reason string
	}{
		{"http://rr1---sn-test.googlevideo.com/videoplayback", "insecure HTTP scheme"},
		{"https://user:pass@rr1---sn-test.googlevideo.com/videoplayback", "userinfo present"},
		{"https://rr1---sn-test.googlevideo.com:8443/videoplayback", "custom port present"},
		{"https://youtube.com/videoplayback", "youtube.com is outside direct GVS media scope"},
		{"https://www.youtube.com/videoplayback", "www.youtube.com is outside direct GVS media scope"},
		{"https://m.youtube.com/videoplayback", "m.youtube.com is outside direct GVS media scope"},
		{"https://youtu.be/videoplayback", "youtu.be is outside direct GVS media scope"},
		{"https://untrusted.example/videoplayback", "untrusted third-party domain"},
		{"https://evilgooglevideo.com/videoplayback", "lookalike host name"},
		{"https://googlevideo.com.attacker.com/videoplayback", "subdomain suffix domain"},
	}

	for _, tc := range invalidURLs {
		u, err := url.Parse(tc.url)
		if err != nil {
			t.Fatalf("failed to parse url %q: %v", tc.url, err)
		}
		if err := validateGVSDestination(u); err == nil {
			t.Errorf("expected rejection for %q (%s), but got nil", tc.url, tc.reason)
		}
	}
}

func TestPOToken_ExtractorAttachesValidateDestinationToGVSFormats(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	// 1. With PO-Token provider configured -> ValidateDestination must be attached
	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       "gvs-token-attached-test",
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)

	res, err := extractor.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	if len(res.Media.Formats) == 0 {
		t.Fatal("expected at least 1 format")
	}

	for _, f := range res.Media.Formats {
		if f.Resource.ValidateDestination == nil {
			t.Errorf("format %s missing ValidateDestination policy for GVS token-bearing resource", f.ID)
		} else {
			// Check policy works
			target, _ := url.Parse("https://youtube.com/test")
			if err := f.Resource.ValidateDestination(target); err == nil {
				t.Errorf("format %s ValidateDestination policy should reject youtube.com", f.ID)
			}
		}
	}

	// 2. Without PO-Token provider configured -> ValidateDestination is nil
	extractorNoProv := New(client)
	resNoProv, err := extractorNoProv.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("extraction without provider failed: %v", err)
	}
	for _, f := range resNoProv.Media.Formats {
		if f.Resource.ValidateDestination != nil {
			t.Errorf("format %s should have nil ValidateDestination when no token is attached", f.ID)
		}
	}
}

func TestPOToken_DownloaderRedirectSafety_GVSResourceCannotRedirectToYouTubeOrYoutuBe(t *testing.T) {
	sentinelToken := "SUPER_SECRET_GVS_SENTINEL_TOKEN_4455"
	var initialPermittedRequests int64
	var targetYouTubeRequests int64
	var targetYoutuBeRequests int64

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Hostname() {
			case "rr1---sn-test.googlevideo.com":
				atomic.AddInt64(&initialPermittedRequests, 1)
				// Redirect to youtube.com
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://www.youtube.com/videoplayback?pot=" + sentinelToken},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			case "rr2---sn-test.googlevideo.com":
				atomic.AddInt64(&initialPermittedRequests, 1)
				// Redirect to youtu.be
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://youtu.be/videoplayback?pot=" + sentinelToken},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			case "www.youtube.com", "youtube.com":
				atomic.AddInt64(&targetYouTubeRequests, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("youtube payload")),
					Request:    req,
				}, nil
			case "youtu.be":
				atomic.AddInt64(&targetYoutuBeRequests, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("youtu.be payload")),
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

	downloader := goyt.NewDownloader(client)

	// Test 1: Redirect to youtube.com
	dest1 := filepath.Join(t.TempDir(), "yt.mp4")
	_, err1 := downloader.Download(
		context.Background(),
		goyt.Resource{
			URL:                 "https://rr1---sn-test.googlevideo.com/videoplayback?itag=18&pot=" + sentinelToken,
			ValidateDestination: validateGVSDestination,
		},
		dest1,
		goyt.DownloadOptions{},
	)
	if err1 == nil {
		t.Fatal("expected error redirecting GVS resource to youtube.com, got nil")
	}
	if ytReqs := atomic.LoadInt64(&targetYouTubeRequests); ytReqs != 0 {
		t.Fatalf("youtube.com received %d requests, want 0", ytReqs)
	}

	// Test 2: Redirect to youtu.be
	dest2 := filepath.Join(t.TempDir(), "ytbe.mp4")
	_, err2 := downloader.Download(
		context.Background(),
		goyt.Resource{
			URL:                 "https://rr2---sn-test.googlevideo.com/videoplayback?itag=18&pot=" + sentinelToken,
			ValidateDestination: validateGVSDestination,
		},
		dest2,
		goyt.DownloadOptions{},
	)
	if err2 == nil {
		t.Fatal("expected error redirecting GVS resource to youtu.be, got nil")
	}
	if ytbeReqs := atomic.LoadInt64(&targetYoutuBeRequests); ytbeReqs != 0 {
		t.Fatalf("youtu.be received %d requests, want 0", ytbeReqs)
	}
}

func TestPOToken_DownloaderRedirectSafety_DroppingPotOnIntermediateHopDoesNotRemovePolicy(t *testing.T) {
	sentinelToken := "SUPER_SECRET_DROP_POT_TOKEN_9911"
	var hop0Requests int64
	var hop1Requests int64
	var hop2UntrustedRequests int64

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Hostname() {
			case "rr1---sn-test.googlevideo.com":
				atomic.AddInt64(&hop0Requests, 1)
				// Hop 0 redirects to permitted Hop 1 but DROPS the ?pot= parameter
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://rr2---sn-test.googlevideo.com/videoplayback?itag=18"},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			case "rr2---sn-test.googlevideo.com":
				atomic.AddInt64(&hop1Requests, 1)
				// Hop 1 (without pot param) redirects to untrusted host
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://untrusted.example/stream.mp4"},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: req,
				}, nil
			case "untrusted.example":
				atomic.AddInt64(&hop2UntrustedRequests, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("untrusted data")),
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

	downloader := goyt.NewDownloader(client)
	dest := filepath.Join(t.TempDir(), "drop_pot.mp4")

	_, err := downloader.Download(
		context.Background(),
		goyt.Resource{
			URL:                 "https://rr1---sn-test.googlevideo.com/videoplayback?itag=18&pot=" + sentinelToken,
			ValidateDestination: validateGVSDestination,
		},
		dest,
		goyt.DownloadOptions{},
	)
	if err == nil {
		t.Fatal("expected error on multi-hop redirect to untrusted host, got nil")
	}

	if h0 := atomic.LoadInt64(&hop0Requests); h0 != 1 {
		t.Fatalf("hop 0 expected 1 request, got %d", h0)
	}
	if h1 := atomic.LoadInt64(&hop1Requests); h1 != 1 {
		t.Fatalf("hop 1 expected 1 request, got %d", h1)
	}
	if h2 := atomic.LoadInt64(&hop2UntrustedRequests); h2 != 0 {
		t.Fatalf("untrusted target received %d requests, want 0", h2)
	}
}

func TestPOToken_ResourcePolicyPreservedAcrossRefreshAndExecution(t *testing.T) {
	videoID := "dQw4w9WgXcQ"
	watchHTML := mockWatchHTMLWithVisitor(videoID, "vis-123", 18)

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
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

	provider := newMockPOTokenProvider(func(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
		return POTokenResult{
			Token:       "refresh-test-token",
			Client:      req.Client,
			Context:     req.Context,
			VideoID:     req.VideoID,
			VisitorData: req.VisitorData,
		}, nil
	})

	extractor := New(client, WithPOTokenProvider(provider))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=" + videoID)

	res, err := extractor.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("initial extraction failed: %v", err)
	}

	origFormat := res.Media.Formats[0]
	if origFormat.Resource.ValidateDestination == nil {
		t.Fatal("expected original format to have ValidateDestination policy")
	}

	// 1. MatchRefreshedFormat preserves ValidateDestination
	matched, err := goyt.MatchRefreshedFormat(origFormat, res.Media.Formats)
	if err != nil {
		t.Fatalf("MatchRefreshedFormat failed: %v", err)
	}
	if matched.Resource.ValidateDestination == nil {
		t.Fatal("expected matched refreshed format to preserve ValidateDestination policy")
	}

	// Verify policy is functional
	untrusted, _ := url.Parse("https://untrusted.example/stream.mp4")
	if err := matched.Resource.ValidateDestination(untrusted); err == nil {
		t.Fatal("expected policy on matched format to reject untrusted domain")
	}
	trusted, _ := url.Parse("https://rr1---sn-test.googlevideo.com/videoplayback")
	if err := matched.Resource.ValidateDestination(trusted); err != nil {
		t.Fatalf("expected policy on matched format to accept googlevideo.com, got: %v", err)
	}
}
