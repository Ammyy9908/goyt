package youtube

import (
	"errors"
	"fmt"
	"strings"
)

// Supported client identifiers.
const (
	ClientVisionOS = "visionos"
	ClientWeb      = "web"
	DefaultClient  = ClientVisionOS
)

// SupportedClients lists the clients available for video/audio downloads.
var SupportedClients = []string{ClientVisionOS, ClientWeb}

// SupportedInspectClients lists the clients available for inspection.
var SupportedInspectClients = []string{ClientWeb, ClientVisionOS, "all"}

// ListSupportedClients returns a copy of supported download client names.
func ListSupportedClients() []string {
	return append([]string(nil), SupportedClients...)
}

// ListSupportedInspectClients returns a copy of supported inspect client names.
func ListSupportedInspectClients() []string {
	return append([]string(nil), SupportedInspectClients...)
}

// ClientCapabilities models what the implementation can attempt for each client,
// separately from what a specific response actually exposes.
type ClientCapabilities struct {
	MetadataInspection bool
	DirectExtraction   bool
	HLSExtraction      bool
	SignatureDecipher  bool
	NChallengeSolve    bool
	POTokenProvider    bool
}

// ClientProfile groups client configuration, headers, and capabilities.
type ClientProfile struct {
	Name         string
	HeaderID     string
	Context      clientContext
	Capabilities ClientCapabilities
}

type clientContext struct {
	ClientName       string `json:"clientName"`
	ClientVersion    string `json:"clientVersion"`
	DeviceMake       string `json:"deviceMake"`
	DeviceModel      string `json:"deviceModel"`
	UserAgent        string `json:"userAgent"`
	OSName           string `json:"osName"`
	OSVersion        string `json:"osVersion"`
	Language         string `json:"hl"`
	TimeZone         string `json:"timeZone"`
	UTCOffsetMinutes int    `json:"utcOffsetMinutes"`
}

// ValidateClient validates and normalizes a client name for download/extraction.
func ValidateClient(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return DefaultClient, nil
	}
	switch name {
	case ClientVisionOS, ClientWeb:
		return name, nil
	default:
		return "", fmt.Errorf("youtube: unsupported client %q; supported clients are visionos, web", name)
	}
}

// ValidateInspectClient validates and normalizes a client name for inspect.
func ValidateInspectClient(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "all", nil
	}
	switch name {
	case ClientWeb, ClientVisionOS, "all":
		return name, nil
	default:
		return "", errors.New("client must be web, visionos, or all")
	}
}

// GetClientProfile returns the profile for the named client.
func GetClientProfile(name string) (ClientProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ClientVisionOS:
		return visionOSProfile(), true
	case ClientWeb:
		return webProfile(), true
	default:
		return ClientProfile{}, false
	}
}

// Profile observed in yt-dlp's YouTube client definitions.
//
// This client does not require a JavaScript player in that configuration.
// That does not guarantee downloadable streams for every request.
func visionOSProfile() ClientProfile {
	return ClientProfile{
		Name:     ClientVisionOS,
		HeaderID: "101",
		Context: clientContext{
			ClientName:    "VISIONOS",
			ClientVersion: "1.02",
			DeviceMake:    "Apple",
			DeviceModel:   "RealityDevice17,1",
			UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) " +
				"AppleWebKit/605.1.15 (KHTML, like Gecko) " +
				"Version/26.0 Safari/605.1.15",
			OSName:           "visionOS",
			OSVersion:        "26.5.23O471",
			Language:         "en",
			TimeZone:         "UTC",
			UTCOffsetMinutes: 0,
		},
		Capabilities: ClientCapabilities{
			MetadataInspection: true,
			DirectExtraction:   true,
			HLSExtraction:      true,
			SignatureDecipher:  false,
			NChallengeSolve:    false,
			POTokenProvider:    false,
		},
	}
}

func webProfile() ClientProfile {
	return ClientProfile{
		Name:     ClientWeb,
		HeaderID: "1",
		Context: clientContext{
			ClientName:       "WEB",
			ClientVersion:    "2.20240101.00.00",
			UserAgent:        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			Language:         "en",
			TimeZone:         "UTC",
			UTCOffsetMinutes: 0,
		},
		Capabilities: ClientCapabilities{
			MetadataInspection: true,
			DirectExtraction:   true,
			HLSExtraction:      true,
			SignatureDecipher:  false,
			NChallengeSolve:    false,
			POTokenProvider:    true,
		},
	}
}
