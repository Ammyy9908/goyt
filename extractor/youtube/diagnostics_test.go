package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockDiagPOTokenProvider implements POTokenProvider for diagnostics testing.
type mockDiagPOTokenProvider struct {
	token     string
	expiresAt *time.Time
	err       error
}

func (m *mockDiagPOTokenProvider) GetPOToken(ctx context.Context, req POTokenRequest) (POTokenResult, error) {
	if m.err != nil {
		return POTokenResult{}, m.err
	}
	return POTokenResult{
		Token:       m.token,
		Client:      req.Client,
		Context:     req.Context,
		VideoID:     req.VideoID,
		VisitorData: req.VisitorData,
		ExpiresAt:   m.expiresAt,
	}, nil
}

func TestDiagnostics_Success_VisionOS(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "player") {
				respJSON := `{
					"playabilityStatus": {"status": "OK"},
					"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "Test Video", "lengthSeconds": "212"},
					"streamingData": {
						"hlsManifestUrl": "https://manifest.googlevideo.com/api/manifest/hls_variant/expire/1800000000",
						"formats": [
							{"itag": 18, "mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 640, "height": 360, "bitrate": 500000}
						],
						"adaptiveFormats": [
							{"itag": 137, "mimeType": "video/mp4; codecs=\"avc1.640028\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 1920, "height": 1080, "bitrate": 3000000},
							{"itag": 140, "mimeType": "audio/mp4; codecs=\"mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "bitrate": 128000}
						]
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(respJSON)),
				}, nil
			}
			// Watch page for visitor data
			watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "CgtUZXN0VmlzaXRvcg%3D%3D"});</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	ext := New(client)
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	res, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientVisionOS)
	if err != nil {
		t.Fatalf("ExtractDownloadableResult unexpected error: %v", err)
	}
	if res.ExtractionReport == nil {
		t.Fatalf("expected non-nil ExtractionReport")
	}

	diag := res.ExtractionReport
	if diag.Client != ClientVisionOS {
		t.Errorf("expected client visionos, got %s", diag.Client)
	}
	if diag.VideoID != "dQw4w9WgXcQ" {
		t.Errorf("expected video ID dQw4w9WgXcQ, got %s", diag.VideoID)
	}
	if diag.FinalStatus != "success" {
		t.Errorf("expected final status success, got %s", diag.FinalStatus)
	}
	if diag.PlaybackStatus != "OK" {
		t.Errorf("expected playback status OK, got %s", diag.PlaybackStatus)
	}
	if !diag.SessionInputs.VisitorDataPresent {
		t.Errorf("expected VisitorDataPresent to be true")
	}
	if diag.SessionInputs.POTokenProviderConfigured {
		t.Errorf("expected POTokenProviderConfigured to be false")
	}

	// Verify FormatInventory
	if diag.FormatInventory.TotalDiscovered != 3 {
		t.Errorf("expected 3 total discovered formats, got %d", diag.FormatInventory.TotalDiscovered)
	}
	if diag.FormatInventory.DirectDownloadableCount != 3 {
		t.Errorf("expected 3 direct downloadable formats, got %d", diag.FormatInventory.DirectDownloadableCount)
	}
	if !diag.FormatInventory.HLSManifestPresent {
		t.Errorf("expected HLSManifestPresent to be true")
	}
	if diag.ResolutionComparison.HighestAvailableHeight != 1080 {
		t.Errorf("expected highest height 1080, got %d", diag.ResolutionComparison.HighestAvailableHeight)
	}

	// Check stage execution order and properties
	if len(diag.Stages) < 2 {
		t.Fatalf("expected at least 2 stages recorded, got %d", len(diag.Stages))
	}
	for i, s := range diag.Stages {
		if s.ExecutionOrder != i+1 {
			t.Errorf("stage %d has order %d, want %d", i, s.ExecutionOrder, i+1)
		}
		if !s.Success {
			t.Errorf("stage %s was not marked successful", s.Name)
		}
	}

	textReport := diag.FormatTextReport()
	if !strings.Contains(textReport, "Final Outcome:    SUCCESS") {
		t.Errorf("text report missing success line: %s", textReport)
	}
	if !strings.Contains(textReport, "Highest available:    1080p") {
		t.Errorf("text report missing resolution comparison: %s", textReport)
	}
}

func TestDiagnostics_Success_Web_WithPOToken(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "CgtUZXN0VmlzaXRvcjI%3D"});</script>
<script>var ytInitialPlayerResponse = {
	"playabilityStatus": {"status": "OK"},
	"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "Web Test", "lengthSeconds": "180"},
	"streamingData": {
		"formats": [
			{"itag": 18, "mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 640, "height": 360, "bitrate": 500000}
		]
	}
};</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	expTime := time.Now().Add(6 * time.Hour)
	poProv := &mockDiagPOTokenProvider{
		token:     "TEST_SECRET_GVS_PO_TOKEN_VALUE",
		expiresAt: &expTime,
	}

	ext := New(client, WithPOTokenProvider(poProv))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	res, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("ExtractDownloadableResult unexpected error: %v", err)
	}

	diag := res.ExtractionReport
	if diag == nil {
		t.Fatalf("expected non-nil ExtractionReport")
	}

	if !diag.SessionInputs.POTokenProviderConfigured {
		t.Errorf("expected POTokenProviderConfigured to be true")
	}
	if !diag.SessionInputs.POTokenAcquired {
		t.Errorf("expected POTokenAcquired to be true")
	}
	if diag.SessionInputs.POTokenContext != "gvs" {
		t.Errorf("expected POTokenContext 'gvs', got %s", diag.SessionInputs.POTokenContext)
	}
	if diag.SessionInputs.POTokenExpiresInSeconds == nil || *diag.SessionInputs.POTokenExpiresInSeconds <= 0 {
		t.Errorf("expected positive POTokenExpiresInSeconds, got %v", diag.SessionInputs.POTokenExpiresInSeconds)
	}

	// Verify LatestDiagnostics on Extractor matches
	latest := ext.LatestDiagnostics()
	if latest == nil || latest.FinalStatus != "success" {
		t.Errorf("expected Extractor.LatestDiagnostics() to be populated with success")
	}
}

func TestDiagnostics_Failure_WatchPageError(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("Too Many Requests")),
			}, nil
		}),
	}

	ext := New(client)
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	_, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err == nil {
		t.Fatalf("expected extraction error, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extErr.Diagnostics == nil {
		t.Fatalf("expected non-nil Diagnostics on ExtractionError")
	}

	diag := extErr.Diagnostics
	if diag.FinalStatus != "failed" {
		t.Errorf("expected FinalStatus 'failed', got %s", diag.FinalStatus)
	}
	if diag.FailureStage != string(StageWatchPageFetch) {
		t.Errorf("expected FailureStage '%s', got %s", StageWatchPageFetch, diag.FailureStage)
	}
	if diag.FailureCode != ErrCodeExtractionRequestFailed {
		t.Errorf("expected FailureCode '%s', got %s", ErrCodeExtractionRequestFailed, diag.FailureCode)
	}

	textReport := diag.FormatTextReport()
	if !strings.Contains(textReport, "FAILED at stage 'watch_page_fetch'") {
		t.Errorf("text report missing failure details: %s", textReport)
	}
}

func TestDiagnostics_Failure_BotCheckRequired(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "player") {
				botJSON := `{
					"playabilityStatus": {
						"status": "LOGIN_REQUIRED",
						"reason": "Sign in to confirm you’re not a bot. This helps protect our community."
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(botJSON)),
				}, nil
			}
			watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "CgtUZXN0VmlzaXRvcg%3D%3D"});</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	ext := New(client)
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	_, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientVisionOS)
	if err == nil {
		t.Fatalf("expected error on bot check, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeBotCheckRequired {
		t.Errorf("expected ErrCodeBotCheckRequired, got %s", extErr.Code)
	}
	if extErr.Diagnostics == nil {
		t.Fatalf("expected Diagnostics attached to ExtractionError")
	}

	diag := extErr.Diagnostics
	if diag.FinalStatus != "failed" {
		t.Errorf("expected final status failed, got %s", diag.FinalStatus)
	}
	if diag.FailureStage != string(StagePlayerRequest) {
		t.Errorf("expected FailureStage player_request, got %s", diag.FailureStage)
	}
	if diag.FailureCode != ErrCodeBotCheckRequired {
		t.Errorf("expected FailureCode bot_check_required, got %s", diag.FailureCode)
	}
	if diag.PlaybackStatus != "LOGIN_REQUIRED" {
		t.Errorf("expected PlaybackStatus LOGIN_REQUIRED, got %s", diag.PlaybackStatus)
	}

	// Verify HTTP success is distinguished from playback rejection
	var playerStage *StageDiagnostic
	for i := range diag.Stages {
		if diag.Stages[i].Name == StagePlayerRequest {
			playerStage = &diag.Stages[i]
			break
		}
	}
	if playerStage == nil {
		t.Fatalf("expected StagePlayerRequest to be recorded in Stages")
	}
	if playerStage.HTTPStatus != http.StatusOK {
		t.Errorf("expected playerStage.HTTPStatus == 200, got %d", playerStage.HTTPStatus)
	}
	if playerStage.Success {
		t.Errorf("expected playerStage.Success == false for rejected playability")
	}

	textReport := diag.FormatTextReport()
	if !strings.Contains(textReport, "bot_check_required") {
		t.Errorf("text report missing bot_check_required: %s", textReport)
	}
	if !strings.Contains(textReport, "player_request") || !strings.Contains(textReport, "REJECTED") {
		t.Errorf("text report should show REJECTED for player_request stage, got:\n%s", textReport)
	}
	if strings.Contains(textReport, "player_request  OK") {
		t.Errorf("text report must NOT display rejected player response as stage 'OK':\n%s", textReport)
	}
}

func TestDiagnostics_Failure_POTokenProviderError(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			watchHTML := `<html><script>var ytInitialPlayerResponse = {
				"playabilityStatus": {"status": "OK"},
				"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "POT Fail"},
				"streamingData": {
					"formats": [{"itag": 18, "mimeType": "video/mp4", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000"}]
				}
			};</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	poProv := &mockDiagPOTokenProvider{
		err: errors.New("backend connection refused"),
	}

	ext := New(client, WithPOTokenProvider(poProv))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	_, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err == nil {
		t.Fatalf("expected PO token provider failure, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodePOTokenProviderFailed {
		t.Errorf("expected ErrCodePOTokenProviderFailed, got %s", extErr.Code)
	}
	if extErr.Diagnostics == nil {
		t.Fatalf("expected Diagnostics attached to ExtractionError")
	}

	diag := extErr.Diagnostics
	if diag.FailureStage != string(StagePOTokenAcquire) {
		t.Errorf("expected failure stage po_token_acquire, got %s", diag.FailureStage)
	}
	if diag.SessionInputs.POTokenAcquired {
		t.Errorf("expected POTokenAcquired to be false")
	}
}

func TestDiagnostics_Failure_NoSupportedFormats(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "player") {
				sabrJSON := `{
					"playabilityStatus": {"status": "OK"},
					"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "SABR Only"},
					"streamingData": {
						"serverAbrStreamingUrl": "https://sabr.googlevideo.com/sabr"
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(sabrJSON)),
				}, nil
			}
			watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "CgtUZXN0VmlzaXRvcg%3D%3D"});</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	ext := New(client)
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	_, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientVisionOS)
	if err == nil {
		t.Fatalf("expected error on SABR only, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeNoSupportedFormats {
		t.Errorf("expected ErrCodeNoSupportedFormats, got %s", extErr.Code)
	}
	if extErr.Diagnostics == nil {
		t.Fatalf("expected Diagnostics attached to ExtractionError")
	}

	diag := extErr.Diagnostics
	if !diag.FormatInventory.SABREndpointPresent || !diag.FormatInventory.SABRRejected {
		t.Errorf("expected SABR to be detected and rejected")
	}
	if diag.FailureStage != string(StageFormatFiltering) {
		t.Errorf("expected FailureStage format_filtering, got %s", diag.FailureStage)
	}
}

func TestDiagnostics_StrictRedaction(t *testing.T) {
	secretToken := "SUPER_SECRET_PO_TOKEN_XYZ_99999"
	secretVisitor := "SUPER_SECRET_VISITOR_DATA_ABC_11111"
	secretAuth := "Bearer secret_jwt_token_auth_header"
	secretCookie := "PREF=f1=50000000&VISITOR_INFO1_LIVE=secret_cookie_val"
	signedURL := "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000"

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "` + secretVisitor + `"});</script>
<script>var ytInitialPlayerResponse = {
	"playabilityStatus": {"status": "OK", "reason": "` + secretAuth + `"},
	"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "Safe Redaction Test"},
	"streamingData": {
		"formats": [
			{"itag": 18, "mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"", "url": "` + signedURL + `"}
		]
	}
};</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	poProv := &mockDiagPOTokenProvider{
		token: secretToken,
	}

	ext := New(client, WithPOTokenProvider(poProv))
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	res, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("ExtractDownloadableResult unexpected error: %v", err)
	}

	diag := res.ExtractionReport
	if diag == nil {
		t.Fatalf("expected non-nil ExtractionReport")
	}

	// 1. Check JSON serialization of ExtractionDiagnostics
	jsonData, err := json.MarshalIndent(diag, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(jsonData)

	forbidden := []string{
		secretToken,
		secretVisitor,
		secretAuth,
		secretCookie,
		"SECRET_SIGNATURE_CIPHER_VAL",
		"SECRET_N_VAL",
		"videoplayback",
		"googlevideo.com",
		"Authorization",
		"Cookie",
	}

	for _, secret := range forbidden {
		if strings.Contains(jsonStr, secret) {
			t.Fatalf("JSON extraction diagnostics leaked sensitive secret %q:\n%s", secret, jsonStr)
		}
	}

	// 2. Check FormatTextReport() text output
	textReport := diag.FormatTextReport()
	for _, secret := range forbidden {
		if strings.Contains(textReport, secret) {
			t.Fatalf("FormatTextReport() leaked sensitive secret %q:\n%s", secret, textReport)
		}
	}
}

func TestDiagnostics_SessionConsistency_VisitorDataAndHeaders(t *testing.T) {
	expectedVisitor := "CgtFeHBlY3RlZFZpc2l0b3I%3D"
	var receivedVisitorHeader string
	var receivedClientHeader string
	var receivedVersionHeader string

	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "player") {
				receivedVisitorHeader = req.Header.Get("X-Goog-Visitor-Id")
				receivedClientHeader = req.Header.Get("X-YouTube-Client-Name")
				receivedVersionHeader = req.Header.Get("X-YouTube-Client-Version")

				respJSON := `{
					"playabilityStatus": {"status": "OK"},
					"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "Consistency Video"},
					"streamingData": {
						"formats": [{"itag": 18, "mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000"}]
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(respJSON)),
				}, nil
			}
			watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "` + expectedVisitor + `"});</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	ext := New(client)
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	res, err := ext.ExtractDownloadableResult(context.Background(), testURL, ClientVisionOS)
	if err != nil {
		t.Fatalf("ExtractDownloadableResult unexpected error: %v", err)
	}

	if receivedVisitorHeader != expectedVisitor {
		t.Errorf("expected X-Goog-Visitor-Id %q, got %q", expectedVisitor, receivedVisitorHeader)
	}
	if receivedClientHeader != "101" {
		t.Errorf("expected X-YouTube-Client-Name '101', got %q", receivedClientHeader)
	}
	if receivedVersionHeader != "1.02" {
		t.Errorf("expected X-YouTube-Client-Version '1.02', got %q", receivedVersionHeader)
	}
	if !res.ExtractionReport.SessionInputs.VisitorDataPresent {
		t.Errorf("expected VisitorDataPresent to be true")
	}
}

func TestDiagnostics_InspectIntegration(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			watchHTML := `<html><script>var ytInitialPlayerResponse = {
				"playabilityStatus": {"status": "OK"},
				"videoDetails": {"videoId": "dQw4w9WgXcQ", "title": "Inspect Test"},
				"streamingData": {
					"formats": [{"itag": 18, "mimeType": "video/mp4", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000"}]
				}
			};</script></html>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
			}, nil
		}),
	}

	ext := New(client)
	testURL, _ := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	report, err := ext.InspectWithClient(context.Background(), testURL, ClientWeb)
	if err != nil {
		t.Fatalf("InspectWithClient unexpected error: %v", err)
	}
	if report.ExtractionReport == nil {
		t.Fatalf("expected non-nil ExtractionReport on Report")
	}
	if report.ExtractionReport.Client != ClientWeb {
		t.Errorf("expected client web, got %s", report.ExtractionReport.Client)
	}

	clientResult := BuildClientInspectResult(ClientWeb, report, nil)
	if clientResult.Diagnostics == nil {
		t.Fatalf("expected non-nil Diagnostics on ClientInspectResult")
	}
	if clientResult.Diagnostics.FinalStatus != "success" {
		t.Errorf("expected Diagnostics.FinalStatus 'success', got %s", clientResult.Diagnostics.FinalStatus)
	}
}

func TestDiagnostics_ConcurrentExtractionsIsolation(t *testing.T) {
	client := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/watch") {
				v := req.URL.Query().Get("v")
				if strings.HasPrefix(v, "vbot") {
					watchHTML := `<html><script>ytcfg.set({"VISITOR_DATA": "CgtUZXN0VmlzaXRvcg%3D%3D"});</script>
<script>var ytInitialPlayerResponse = {
	"playabilityStatus": {"status": "LOGIN_REQUIRED", "reason": "Sign in to confirm you’re not a bot"}
};</script></html>`
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
						Body:       io.NopCloser(strings.NewReader(watchHTML)),
					}, nil
				}
				watchHTML := fmt.Sprintf(`<html><script>ytcfg.set({"VISITOR_DATA": "CgtUZXN0VmlzaXRvcg%%3D%%3D"});</script>
<script>var ytInitialPlayerResponse = {
	"playabilityStatus": {"status": "OK"},
	"videoDetails": {"videoId": "%s", "title": "Concurrent Test %s", "lengthSeconds": "120"},
	"streamingData": {
		"formats": [
			{"itag": 18, "mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 640, "height": 360, "bitrate": 500000}
		],
		"adaptiveFormats": [
			{"itag": 137, "mimeType": "video/mp4; codecs=\"avc1.640028\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 1920, "height": 1080, "bitrate": 3000000},
			{"itag": 140, "mimeType": "audio/mp4; codecs=\"mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "bitrate": 128000}
		]
	}
};</script></html>`, v, v)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(watchHTML)),
				}, nil
			}
			if strings.Contains(req.URL.Path, "/youtubei/v1/player") {
				bodyBytes, _ := io.ReadAll(req.Body)
				var body struct {
					VideoID string `json:"videoId"`
				}
				_ = json.Unmarshal(bodyBytes, &body)
				vid := body.VideoID
				if strings.HasPrefix(vid, "vbot") {
					respJSON := `{"playabilityStatus": {"status": "LOGIN_REQUIRED", "reason": "Sign in to confirm you’re not a bot"}}`
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(respJSON)),
					}, nil
				}
				respJSON := fmt.Sprintf(`{
					"playabilityStatus": {"status": "OK"},
					"videoDetails": {"videoId": "%s", "title": "Concurrent %s", "lengthSeconds": "120"},
					"streamingData": {
						"formats": [
							{"itag": 18, "mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 640, "height": 360, "bitrate": 500000}
						],
						"adaptiveFormats": [
							{"itag": 137, "mimeType": "video/mp4; codecs=\"avc1.640028\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "width": 1920, "height": 1080, "bitrate": 3000000},
							{"itag": 140, "mimeType": "audio/mp4; codecs=\"mp4a.40.2\"", "url": "https://rr1---sn-test.googlevideo.com/videoplayback?expire=1800000000", "bitrate": 128000}
						]
					}
				}`, vid, vid)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(respJSON)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("404")),
			}, nil
		}),
	}

	ext := New(client)
	const concurrency = 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			isBot := idx%3 == 0
			var vid string
			if isBot {
				vid = fmt.Sprintf("vbot%07d", idx)
			} else {
				vid = fmt.Sprintf("vokk%07d", idx)
			}
			targetURL, _ := url.Parse("https://www.youtube.com/watch?v=" + vid)
			clientName := ClientVisionOS
			if idx%2 == 0 {
				clientName = ClientWeb
			}

			res, err := ext.ExtractDownloadableResult(context.Background(), targetURL, clientName)
			if isBot {
				if err == nil {
					t.Errorf("worker %d (%s) expected error for bot check, got nil", idx, vid)
					return
				}
				var extErr *ExtractionError
				if !errors.As(err, &extErr) || extErr.Diagnostics == nil {
					t.Errorf("worker %d expected ExtractionError with Diagnostics, got %v", idx, err)
					return
				}
				if extErr.Diagnostics.VideoID != vid {
					t.Errorf("worker %d received wrong VideoID in diagnostics: expected %s, got %s", idx, vid, extErr.Diagnostics.VideoID)
				}
				if extErr.Diagnostics.Client != clientName {
					t.Errorf("worker %d received wrong Client in diagnostics: expected %s, got %s", idx, clientName, extErr.Diagnostics.Client)
				}
				if extErr.Diagnostics.FinalStatus != "failed" {
					t.Errorf("worker %d expected FinalStatus 'failed', got %s", idx, extErr.Diagnostics.FinalStatus)
				}
			} else {
				if err != nil {
					t.Errorf("worker %d (%s) unexpected error: %v", idx, vid, err)
					return
				}
				if res.ExtractionReport == nil {
					t.Errorf("worker %d expected non-nil ExtractionReport", idx)
					return
				}
				if res.ExtractionReport.VideoID != vid {
					t.Errorf("worker %d received wrong VideoID in diagnostics: expected %s, got %s", idx, vid, res.ExtractionReport.VideoID)
				}
				if res.ExtractionReport.Client != clientName {
					t.Errorf("worker %d received wrong Client in diagnostics: expected %s, got %s", idx, clientName, res.ExtractionReport.Client)
				}
				if res.ExtractionReport.FinalStatus != "success" {
					t.Errorf("worker %d expected FinalStatus 'success', got %s", idx, res.ExtractionReport.FinalStatus)
				}
			}
		}(i)
	}

	wg.Wait()
}
