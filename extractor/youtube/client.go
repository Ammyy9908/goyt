package youtube

// clientProfile keeps client-specific request details together.
// These values may need updates as YouTube changes its clients.
type clientProfile struct {
	Name     string
	HeaderID string
	Context  clientContext
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

// Profile observed in yt-dlp's YouTube client definitions.
//
// This client does not require a JavaScript player in that configuration.
// That does not guarantee downloadable streams for every request.
func visionOSProfile() clientProfile {
	return clientProfile{
		Name:     "visionos",
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
	}
}
