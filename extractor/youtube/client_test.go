package youtube

import (
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
