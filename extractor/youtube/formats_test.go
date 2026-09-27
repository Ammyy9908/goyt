package youtube

import "testing"

func TestConvertDirectVideo(t *testing.T) {
	format, ok := convertDirectFormat(playerFormat{
		Itag:          137,
		MIMEType:      `video/mp4; codecs="avc1.640028"`,
		URL:           "https://media.test/video?expire=2000000000",
		Height:        1080,
		Width:         1920,
		Bitrate:       2_000_000,
		ContentLength: "12345",
	}, visionOSProfile())

	if !ok {
		t.Fatal("valid direct video was rejected")
	}

	if format.ID != "137" ||
		format.VideoCodec != "avc1.640028" ||
		format.AudioCodec != "none" {
		t.Fatalf("incorrect format: %+v", format)
	}

	if format.Height == nil || *format.Height != 1080 {
		t.Fatal("missing height")
	}

	if format.SizeBytes == nil || *format.SizeBytes != 12345 {
		t.Fatal("missing size")
	}

	if format.Resource.ExpiresAt == nil {
		t.Fatal("missing expiry")
	}

	if format.Resource.Headers.Get("User-Agent") == "" {
		t.Fatal("missing client user agent")
	}
}

func TestConvertDirectAudio(t *testing.T) {
	format, ok := convertDirectFormat(playerFormat{
		Itag:          140,
		MIMEType:      `audio/mp4; codecs="mp4a.40.2"`,
		URL:           "https://media.test/audio",
		AudioChannels: 2,
	}, visionOSProfile())

	if !ok ||
		format.VideoCodec != "none" ||
		format.AudioCodec != "mp4a.40.2" {
		t.Fatalf("incorrect audio conversion: %+v", format)
	}
}

func TestConvertCombinedFormat(t *testing.T) {
	format, ok := convertDirectFormat(playerFormat{
		Itag:     18,
		MIMEType: `video/mp4; codecs="avc1.42001E, mp4a.40.2"`,
		URL:      "https://media.test/combined",
	}, visionOSProfile())

	if !ok ||
		format.VideoCodec == "none" ||
		format.AudioCodec == "none" {
		t.Fatal("combined stream was misclassified")
	}
}

func TestRejectUnsupportedDirectFormats(t *testing.T) {
	cases := []playerFormat{
		{
			MIMEType: `video/mp4; codecs="avc1.640028"`,
		},
		{
			MIMEType: `video/mp4; codecs="avc1.640028"`,
			URL:      "https://media.test/video?n=challenge",
		},
		{
			MIMEType:        `video/mp4; codecs="avc1.640028"`,
			URL:             "https://media.test/video",
			SignatureCipher: "s=challenge",
		},
		{
			MIMEType:    `video/mp4; codecs="avc1.640028"`,
			URL:         "https://media.test/video",
			DRMFamilies: []string{"reported-drm"},
		},
		{
			MIMEType: `video/mp4; codecs="unknown"`,
			URL:      "https://media.test/video",
		},
		{
			MIMEType:      `video/mp4; codecs="avc1.640028"`,
			URL:           "https://media.test/video",
			AudioChannels: 2,
		},
	}

	for i, raw := range cases {
		if _, ok := convertDirectFormat(raw, visionOSProfile()); ok {
			t.Fatalf("case %d should be rejected", i)
		}
	}
}
