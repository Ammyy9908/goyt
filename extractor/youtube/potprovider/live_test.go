package potprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

func TestLiveProvider_IntegrationSmoke(t *testing.T) {
	endpoint := "http://127.0.0.1:4416"

	// 1. Probe health check
	provider, err := NewHTTPProvider(endpoint, WithTimeout(15*time.Second))
	if err != nil {
		t.Fatalf("failed to create HTTPProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := provider.Ping(ctx); err != nil {
		t.Skipf("live provider not running at %s: skipping live smoke test (%v)", endpoint, err)
		return
	}

	t.Log("1. Live provider health check succeeded at", endpoint)

	// 2. Real upstream deprecated-field rejection assertion
	deprecatedReqPayload := map[string]any{
		"visitor_data": "deprecated_session_data",
	}
	depBytes, _ := json.Marshal(deprecatedReqPayload)
	depResp, err := http.Post(endpoint+"/get_pot", "application/json", bytes.NewReader(depBytes))
	if err != nil {
		t.Fatalf("failed to send deprecated payload test: %v", err)
	}
	defer depResp.Body.Close()

	if depResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected real provider to reject deprecated visitor_data with HTTP 400, got: %d", depResp.StatusCode)
	}
	var depErrBody map[string]any
	_ = json.NewDecoder(depResp.Body).Decode(&depErrBody)
	if errStr, _ := depErrBody["error"].(string); !strings.Contains(errStr, "visitor_data is deprecated") {
		t.Fatalf("expected error message containing 'visitor_data is deprecated', got: %v", errStr)
	}
	t.Log("2. Real provider correctly rejected deprecated visitor_data with HTTP 400 Bad Request")

	// 3. Real token acquisition & adapter parsing for GVS context
	testVideoID := "dQw4w9WgXcQ"
	req := youtube.POTokenRequest{
		Client:  youtube.ClientWeb,
		Context: youtube.POTokenContextGVS,
		VideoID: testVideoID,
	}

	tokenCtx, tokenCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer tokenCancel()

	result, err := provider.GetPOToken(tokenCtx, req)
	if err != nil {
		t.Fatalf("real provider GetPOToken failed: %v", err)
	}

	// 4. Returned binding and expiry validation (WITHOUT printing token value)
	if len(result.Token) == 0 {
		t.Fatal("expected non-empty token from real provider")
	}
	if result.Client != youtube.ClientWeb {
		t.Fatalf("expected client %q, got %q", youtube.ClientWeb, result.Client)
	}
	if result.Context != youtube.POTokenContextGVS {
		t.Fatalf("expected context %q, got %q", youtube.POTokenContextGVS, result.Context)
	}
	if result.VideoID != testVideoID {
		t.Fatalf("expected videoID %q, got %q", testVideoID, result.VideoID)
	}
	if result.ExpiresAt == nil || result.ExpiresAt.IsZero() {
		t.Fatal("expected non-nil expiresAt timestamp from real provider")
	}
	if !result.ExpiresAt.After(time.Now()) {
		t.Fatalf("expected expiresAt in the future, got: %v", result.ExpiresAt)
	}

	t.Logf("3. Real token generation succeeded (token length: %d chars, expires in %v)", len(result.Token), time.Until(*result.ExpiresAt).Round(time.Second))
	t.Log("4. Returned binding and expiry validated successfully against real provider")
}
