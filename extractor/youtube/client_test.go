package youtube

import (
	"strings"
	"testing"
)

func TestValidateClient(t *testing.T) {
	tests := []struct {
		input       string
		expected    string
		expectError bool
	}{
		{"visionos", ClientVisionOS, false},
		{"VISIONOS", ClientVisionOS, false},
		{"VisionOS", ClientVisionOS, false},
		{"web", ClientWeb, false},
		{"WEB", ClientWeb, false},
		{"all", "", true},
		{"android", "", true},
		{"ios", "", true},
		{"", ClientVisionOS, false},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ValidateClient(tt.input)
			if tt.expectError {
				if err == nil {
					t.Fatalf("ValidateClient(%q) expected error, got %q", tt.input, got)
				}
			} else {
				if err != nil {
					t.Fatalf("ValidateClient(%q) unexpected error: %v", tt.input, err)
				}
				if got != tt.expected {
					t.Fatalf("ValidateClient(%q) = %q, want %q", tt.input, got, tt.expected)
				}
			}
		})
	}
}

func TestValidateInspectClient(t *testing.T) {
	tests := []struct {
		input       string
		expected    string
		expectError bool
	}{
		{"visionos", ClientVisionOS, false},
		{"web", ClientWeb, false},
		{"all", "all", false},
		{"ALL", "all", false},
		{"android", "", true},
		{"", "all", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ValidateInspectClient(tt.input)
			if tt.expectError {
				if err == nil {
					t.Fatalf("ValidateInspectClient(%q) expected error, got %q", tt.input, got)
				}
			} else {
				if err != nil {
					t.Fatalf("ValidateInspectClient(%q) unexpected error: %v", tt.input, err)
				}
				if got != tt.expected {
					t.Fatalf("ValidateInspectClient(%q) = %q, want %q", tt.input, got, tt.expected)
				}
			}
		})
	}
}

func TestGetClientProfile(t *testing.T) {
	vos, ok := GetClientProfile(ClientVisionOS)
	if !ok {
		t.Fatalf("GetClientProfile(visionos) expected ok=true")
	}
	if vos.Name != ClientVisionOS {
		t.Fatalf("expected name %s, got %s", ClientVisionOS, vos.Name)
	}
	if !vos.Capabilities.MetadataInspection || !vos.Capabilities.DirectExtraction || !vos.Capabilities.HLSExtraction {
		t.Fatalf("visionos capabilities mismatch: %+v", vos.Capabilities)
	}
	if vos.Capabilities.SignatureDecipher || vos.Capabilities.NChallengeSolve || vos.Capabilities.POTokenProvider {
		t.Fatalf("expected challenge solvers to be false for visionos: %+v", vos.Capabilities)
	}

	web, ok := GetClientProfile(ClientWeb)
	if !ok {
		t.Fatalf("GetClientProfile(web) expected ok=true")
	}
	if web.Name != ClientWeb {
		t.Fatalf("expected name %s, got %s", ClientWeb, web.Name)
	}
	if !web.Capabilities.MetadataInspection || !web.Capabilities.DirectExtraction || !web.Capabilities.HLSExtraction {
		t.Fatalf("web capabilities mismatch: %+v", web.Capabilities)
	}

	_, ok = GetClientProfile("unknown")
	if ok {
		t.Fatal("GetClientProfile(unknown) expected ok=false, got true")
	}
}

func TestListSupportedClients(t *testing.T) {
	clients := ListSupportedClients()
	if len(clients) != 2 {
		t.Fatalf("expected 2 supported clients, got %d", len(clients))
	}
	if clients[0] != ClientVisionOS || clients[1] != ClientWeb {
		t.Fatalf("unexpected clients list: %v", clients)
	}
}

func TestInspectionLimitations_WebAndVisionOS(t *testing.T) {
	player := &playerResponse{
		VideoDetails: struct {
			VideoID       string `json:"videoId"`
			Title         string `json:"title"`
			LengthSeconds string `json:"lengthSeconds"`
			IsLiveContent bool   `json:"isLiveContent"`
		}{
			VideoID:       "abcdefghijk",
			Title:         "Test Video",
			LengthSeconds: "120",
		},
	}
	player.PlayabilityStatus.Status = "OK"

	staleLimitation := "No JavaScript challenge solver or PO-token provider is implemented."
	expectedStatements := []string{
		"Inspection reports detected JavaScript challenges without executing solving.",
		"Downloads can optionally solve supported JavaScript challenges using -js-runtime.",
		"No PO-token provider is implemented.",
	}

	// 1. Test Web report
	webReport := buildReport("abcdefghijk", player)
	for _, lim := range webReport.Limitations {
		if lim == staleLimitation {
			t.Errorf("web report contains stale limitation: %q", lim)
		}
	}
	for _, expected := range expectedStatements {
		found := false
		for _, lim := range webReport.Limitations {
			if lim == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("web report missing expected limitation statement: %q", expected)
		}
	}

	// 2. Test VisionOS report
	vosReport := buildReport("abcdefghijk", player)
	if len(vosReport.Limitations) > 0 {
		vosReport.Limitations[0] = "VISIONOS player API response; discovered URLs are not download-verified."
	}
	vosReport.Limitations = append(
		vosReport.Limitations,
		"Visitor identifier propagated from a fresh watch page; no account cookies or PO-token provider.",
	)
	for _, lim := range vosReport.Limitations {
		if lim == staleLimitation {
			t.Errorf("visionos report contains stale limitation: %q", lim)
		}
	}
	for _, expected := range expectedStatements {
		found := false
		for _, lim := range vosReport.Limitations {
			if lim == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("visionos report missing expected limitation statement: %q", expected)
		}
	}

	// 3. Test JSON DTO serialization
	webResult := BuildClientInspectResult(ClientWeb, webReport, nil)
	vosResult := BuildClientInspectResult(ClientVisionOS, vosReport, nil)
	jsonResp := BuildInspectResponse([]ClientInspectResult{webResult, vosResult})

	if jsonResp.SchemaVersion != 1 {
		t.Fatalf("expected schema_version 1, got %d", jsonResp.SchemaVersion)
	}
	for _, res := range jsonResp.Results {
		for _, lim := range res.Limitations {
			if lim == staleLimitation {
				t.Errorf("json result for %s contains stale limitation: %q", res.Client, lim)
			}
		}
		for _, expected := range expectedStatements {
			found := false
			for _, lim := range res.Limitations {
				if lim == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("json result for %s missing expected limitation: %q", res.Client, expected)
			}
		}
	}
}

func TestInspectionLimitations_RestrictedPlayback(t *testing.T) {
	player := &playerResponse{
		VideoDetails: struct {
			VideoID       string `json:"videoId"`
			Title         string `json:"title"`
			LengthSeconds string `json:"lengthSeconds"`
			IsLiveContent bool   `json:"isLiveContent"`
		}{
			VideoID: "abcdefghijk",
			Title:   "Restricted Video",
		},
	}
	player.PlayabilityStatus.Status = "LOGIN_REQUIRED"
	player.PlayabilityStatus.Reason = "Sign in to confirm your age"

	report := buildReport("abcdefghijk", player)
	if report.PlaybackStatus != "LOGIN_REQUIRED" || report.PlaybackReason != "Sign in to confirm your age" {
		t.Fatalf("playback status or reason lost: status=%q, reason=%q", report.PlaybackStatus, report.PlaybackReason)
	}

	// Verify limitations are properly populated on restricted reports
	expectedStatements := []string{
		"Inspection reports detected JavaScript challenges without executing solving.",
		"Downloads can optionally solve supported JavaScript challenges using -js-runtime.",
		"No PO-token provider is implemented.",
	}
	for _, expected := range expectedStatements {
		found := false
		for _, lim := range report.Limitations {
			if lim == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("restricted report missing expected limitation statement: %q", expected)
		}
	}

	// Verify JSON Schema 1 preserves restricted status
	res := BuildClientInspectResult(ClientWeb, report, nil)
	if res.Status != "ok" || res.Playback == nil || res.Playback.Status != "LOGIN_REQUIRED" {
		t.Fatalf("unexpected JSON DTO for restricted playback: %+v", res)
	}
}

func TestBotCheckDiagnostic_Accuracy(t *testing.T) {
	botReason := "Sign in to confirm you’re not a bot"
	if !IsBotCheckReason(botReason) {
		t.Fatalf("expected IsBotCheckReason(%q) = true", botReason)
	}

	err := classifyPlayabilityError(ClientWeb, "LOGIN_REQUIRED", botReason)
	if err == nil || err.Code != ErrCodeBotCheckRequired {
		t.Fatalf("expected ErrCodeBotCheckRequired, got: %+v", err)
	}

	msg := err.Error()
	// Must state that the request was rejected before usable media was returned
	if !strings.Contains(msg, "bot-check response") || !strings.Contains(msg, "no usable media was returned") {
		t.Errorf("expected descriptive bot-check diagnostic, got: %s", msg)
	}
	// Must NOT claim enabling Deno fixes it
	if strings.Contains(strings.ToLower(msg), "deno") {
		t.Errorf("bot-check diagnostic should not mention Deno: %s", msg)
	}
	// Must NOT claim permanent IP block
	if strings.Contains(strings.ToLower(msg), "permanent") || strings.Contains(strings.ToLower(msg), "blocked forever") {
		t.Errorf("bot-check diagnostic should not claim permanent IP block: %s", msg)
	}
}
