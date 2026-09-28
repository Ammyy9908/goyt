package potprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

func TestValidateEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		wantError bool
		errMsg    string
	}{
		{
			name:      "Valid localhost HTTP",
			endpoint:  "http://127.0.0.1:4416",
			wantError: false,
		},
		{
			name:      "Valid localhost named HTTP",
			endpoint:  "http://localhost:4416",
			wantError: false,
		},
		{
			name:      "Valid IPv6 loopback HTTP",
			endpoint:  "http://[::1]:4416",
			wantError: false,
		},
		{
			name:      "Valid remote HTTPS",
			endpoint:  "https://pot.example.com:4416",
			wantError: false,
		},
		{
			name:      "Valid remote HTTPS with path prefix",
			endpoint:  "https://pot.example.com/api/pot",
			wantError: false,
		},
		{
			name:      "Empty endpoint",
			endpoint:  "   ",
			wantError: true,
			errMsg:    "endpoint URL is empty",
		},
		{
			name:      "Invalid scheme ftp",
			endpoint:  "ftp://127.0.0.1:4416",
			wantError: true,
			errMsg:    "must be http or https",
		},
		{
			name:      "Userinfo prohibited",
			endpoint:  "http://user:pass@127.0.0.1:4416",
			wantError: true,
			errMsg:    "credentials is not permitted",
		},
		{
			name:      "Unencrypted HTTP remote host prohibited",
			endpoint:  "http://remote.example.com:4416",
			wantError: true,
			errMsg:    "must be localhost/loopback",
		},
		{
			name:      "Missing hostname",
			endpoint:  "http:///path",
			wantError: true,
			errMsg:    "missing hostname",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ValidateEndpoint(tc.endpoint)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for endpoint %q, got nil (url: %v)", tc.endpoint, u)
				}
				if !strings.Contains(err.Error(), tc.errMsg) {
					t.Fatalf("expected error containing %q, got: %v", tc.errMsg, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for endpoint %q: %v", tc.endpoint, err)
				}
				if u == nil {
					t.Fatalf("expected non-nil url for endpoint %q", tc.endpoint)
				}
			}
		})
	}
}

func TestHTTPProvider_SuccessfulTokenAcquisition(t *testing.T) {
	sentinelToken := "verified.http.provider.po.token.12345"
	expectedVideoID := "dQw4w9WgXcQ"
	expectedVisitor := "visitor-data-abc"

	var capturedRawBody map[string]any
	var requestsCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestsCount, 1)
		if r.Method != http.MethodPost || r.URL.Path != "/get_pot" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			http.Error(w, "invalid content-type", http.StatusBadRequest)
			return
		}

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedRawBody)

		exp := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"poToken":        sentinelToken,
			"contentBinding": expectedVideoID,
			"expiresAt":      exp,
		})
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := youtube.POTokenRequest{
		Client:      youtube.ClientWeb,
		Context:     youtube.POTokenContextGVS,
		VideoID:     expectedVideoID,
		VisitorData: expectedVisitor,
	}

	res, err := provider.GetPOToken(context.Background(), req)
	if err != nil {
		t.Fatalf("GetPOToken failed: %v", err)
	}

	if res.Token != sentinelToken {
		t.Fatalf("expected token %q, got %q", sentinelToken, res.Token)
	}
	if res.Client != youtube.ClientWeb {
		t.Fatalf("expected client %q, got %q", youtube.ClientWeb, res.Client)
	}
	if res.Context != youtube.POTokenContextGVS {
		t.Fatalf("expected context %q, got %q", youtube.POTokenContextGVS, res.Context)
	}
	if res.VideoID != expectedVideoID {
		t.Fatalf("expected videoID %q, got %q", expectedVideoID, res.VideoID)
	}
	if res.VisitorData != expectedVisitor {
		t.Fatalf("expected visitorData %q, got %q", expectedVisitor, res.VisitorData)
	}
	if res.ExpiresAt == nil || res.ExpiresAt.IsZero() {
		t.Fatal("expected non-nil expiration time")
	}

	// Verify request payload fields conform strictly to Brainicism 2.0.0
	if binding, ok := capturedRawBody["content_binding"].(string); !ok || binding != expectedVideoID {
		t.Errorf("captured content_binding mismatch: got %v, want %q", capturedRawBody["content_binding"], expectedVideoID)
	}
	if _, hasVisitor := capturedRawBody["visitor_data"]; hasVisitor {
		t.Error("captured request MUST NOT contain deprecated visitor_data key")
	}
	if _, hasClient := capturedRawBody["client"]; hasClient {
		t.Error("captured request should not contain unsupported client key")
	}
	if _, hasContext := capturedRawBody["context"]; hasContext {
		t.Error("captured request should not contain unsupported context key")
	}
}

func TestHTTPProvider_UpstreamContract_RejectsVisitorDataField(t *testing.T) {
	// Upstream Brainicism server/src/main.ts lines 122-142:
	// if ('visitor_data' in body || 'data_sync_id' in body || 'disable_innertube' in body) {
	//   reply.code(400).send({ error: 'visitor_data is deprecated, use content_binding instead' });
	// }
	sentinelToken := "bgutil-2.0.0-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rawBody map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rawBody)

		if _, hasVisitor := rawBody["visitor_data"]; hasVisitor {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "visitor_data is deprecated, use content_binding instead",
			})
			return
		}
		if _, hasSync := rawBody["data_sync_id"]; hasSync {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "visitor_data is deprecated, use content_binding instead",
			})
			return
		}
		if _, hasDisable := rawBody["disable_innertube"]; hasDisable {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "visitor_data is deprecated, use content_binding instead",
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"poToken":        sentinelToken,
			"contentBinding": rawBody["content_binding"],
			"expiresAt":      time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339Nano),
		})
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// 1. GVS Request should succeed because HTTPProvider does not send visitor_data
	gvsReq := youtube.POTokenRequest{
		Client:      youtube.ClientWeb,
		Context:     youtube.POTokenContextGVS,
		VideoID:     "dQw4w9WgXcQ",
		VisitorData: "vis-gvs-123",
	}
	resGVS, err := provider.GetPOToken(context.Background(), gvsReq)
	if err != nil {
		t.Fatalf("GVS GetPOToken unexpectedly failed against strict upstream mock: %v", err)
	}
	if resGVS.Token != sentinelToken {
		t.Fatalf("expected token %q, got %q", sentinelToken, resGVS.Token)
	}

	// 2. Player Request should succeed by placing visitorData into content_binding
	playerReq := youtube.POTokenRequest{
		Client:      youtube.ClientWeb,
		Context:     youtube.POTokenContextPlayer,
		VisitorData: "vis-player-456",
	}
	resPlayer, err := provider.GetPOToken(context.Background(), playerReq)
	if err != nil {
		t.Fatalf("Player GetPOToken unexpectedly failed against strict upstream mock: %v", err)
	}
	if resPlayer.Token != sentinelToken {
		t.Fatalf("expected token %q, got %q", sentinelToken, resPlayer.Token)
	}
}

func TestHTTPProvider_AlternativeResponseFields(t *testing.T) {
	sentinelToken := "snake-case-po-token-9988"
	expectedVideoID := "dQw4w9WgXcQ"
	expTime := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"po_token":        sentinelToken,
			"content_binding": expectedVideoID,
			"expires_at":      expTime.Format(time.RFC3339),
		})
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := youtube.POTokenRequest{
		Client:      youtube.ClientWeb,
		Context:     youtube.POTokenContextGVS,
		VideoID:     expectedVideoID,
		VisitorData: "vis-snake",
	}

	res, err := provider.GetPOToken(context.Background(), req)
	if err != nil {
		t.Fatalf("GetPOToken failed: %v", err)
	}

	if res.Token != sentinelToken {
		t.Fatalf("expected token %q, got %q", sentinelToken, res.Token)
	}
	if res.ExpiresAt == nil || !res.ExpiresAt.Equal(expTime) {
		t.Fatalf("expected expiresAt %v, got: %v", expTime, res.ExpiresAt)
	}
}

func TestHTTPProvider_DoesNotInventExpirationWhenOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"poToken": "token-without-expiry",
		})
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: "dQw4w9WgXcQ",
	}

	res, err := provider.GetPOToken(context.Background(), req)
	if err != nil {
		t.Fatalf("GetPOToken failed: %v", err)
	}

	if res.ExpiresAt != nil {
		t.Fatalf("provider must NOT invent expiration when omitted by server; got %v", res.ExpiresAt)
	}
}

func TestHTTPProvider_GVSMissingVideoID_ZeroProviderRequests(t *testing.T) {
	var requestCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"poToken": "unexpected-token",
		})
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// 1. Missing VideoID with non-empty VisitorData (must NOT fallback to visitor data)
	req := youtube.POTokenRequest{
		Client:      youtube.ClientWeb,
		Context:     youtube.POTokenContextGVS,
		VideoID:     "",
		VisitorData: "fallback-visitor-not-allowed",
	}

	_, err = provider.GetPOToken(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when GVS VideoID is missing, got nil")
	}
	if !strings.Contains(err.Error(), "video ID is required for GVS PO-token request") {
		t.Fatalf("expected video ID requirement error, got: %v", err)
	}
	if reqs := atomic.LoadInt64(&requestCount); reqs != 0 {
		t.Fatalf("expected 0 provider requests when VideoID is missing, got %d", reqs)
	}

	// 2. Whitespace-only VideoID
	reqWhitespace := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: "   ",
	}
	_, err = provider.GetPOToken(context.Background(), reqWhitespace)
	if err == nil {
		t.Fatal("expected error when GVS VideoID is whitespace, got nil")
	}
	if reqs := atomic.LoadInt64(&requestCount); reqs != 0 {
		t.Fatalf("expected 0 provider requests when VideoID is whitespace, got %d", reqs)
	}
}

func TestHTTPProvider_RedirectRejection(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"po_token": "redirected-token"})
	}))
	defer redirectTarget.Close()

	redirectingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/get_pot", http.StatusFound)
	}))
	defer redirectingServer.Close()

	provider, err := NewHTTPProvider(redirectingServer.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: "dQw4w9WgXcQ",
	}

	_, err = provider.GetPOToken(context.Background(), req)
	if err == nil {
		t.Fatal("expected error on provider redirect, got nil")
	}

	if !errors.Is(err, youtube.ErrPOTokenProviderFailed) {
		t.Fatalf("expected ErrPOTokenProviderFailed, got: %v", err)
	}
}

func TestHTTPProvider_OversizedResponseRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Send response exceeding MaxResponseBytes
		fmt.Fprintf(w, `{"po_token":"%s"}`, strings.Repeat("A", MaxResponseBytes+100))
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: "dQw4w9WgXcQ",
	}

	_, err = provider.GetPOToken(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for oversized response, got nil")
	}
	if !errors.Is(err, youtube.ErrPOTokenProviderFailed) {
		t.Fatalf("expected ErrPOTokenProviderFailed for oversized response, got: %v", err)
	}
}

func TestHTTPProvider_ServerErrorAndNotFound(t *testing.T) {
	// 1. 404 Not Found -> ErrPOTokenUnavailable
	server404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server404.Close()

	p404, _ := NewHTTPProvider(server404.URL)
	req := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: "dQw4w9WgXcQ",
	}
	_, err404 := p404.GetPOToken(context.Background(), req)
	if !errors.Is(err404, youtube.ErrPOTokenUnavailable) {
		t.Fatalf("expected ErrPOTokenUnavailable on 404, got: %v", err404)
	}

	// 2. 500 Server Error -> ErrPOTokenProviderFailed
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal generator failure", http.StatusInternalServerError)
	}))
	defer server500.Close()

	p500, _ := NewHTTPProvider(server500.URL)
	_, err500 := p500.GetPOToken(context.Background(), req)
	if !errors.Is(err500, youtube.ErrPOTokenProviderFailed) {
		t.Fatalf("expected ErrPOTokenProviderFailed on 500, got: %v", err500)
	}
}

func TestHTTPProvider_TimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"po_token": "delayed-token"})
	}))
	defer server.Close()

	// 1. Timeout
	pTimeout, _ := NewHTTPProvider(server.URL, WithTimeout(50*time.Millisecond))
	req := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: "dQw4w9WgXcQ",
	}
	_, errTimeout := pTimeout.GetPOToken(context.Background(), req)
	if !errors.Is(errTimeout, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", errTimeout)
	}

	// 2. Cancellation
	pCancel, _ := NewHTTPProvider(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, errCancel := pCancel.GetPOToken(ctx, req)
	if !errors.Is(errCancel, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", errCancel)
	}
}

func TestHTTPProvider_ScopeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"poToken":        "mismatched-scope-token",
			"contentBinding": "different-video-id",
		})
	}))
	defer server.Close()

	provider, _ := NewHTTPProvider(server.URL)
	req := youtube.POTokenRequest{
		Client:      youtube.ClientWeb,
		Context:     youtube.POTokenContextGVS,
		VideoID:     "dQw4w9WgXcQ",
		VisitorData: "required-client-visitor",
	}

	_, err := provider.GetPOToken(context.Background(), req)
	if !errors.Is(err, youtube.ErrPOTokenScopeMismatch) {
		t.Fatalf("expected ErrPOTokenScopeMismatch when provider returns mismatched contentBinding, got: %v", err)
	}
}

func TestHTTPProvider_PingHealthCheck(t *testing.T) {
	var pingCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			atomic.AddInt64(&pingCount, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if atomic.LoadInt64(&pingCount) != 1 {
		t.Fatalf("expected 1 ping request, got %d", atomic.LoadInt64(&pingCount))
	}
}
