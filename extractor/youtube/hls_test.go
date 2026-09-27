package youtube

import "testing"

func TestYouTubeManifestResource(t *testing.T) {
	resource, err := youtubeManifestResource(
		"https://manifest.test/api/expire/2000000000/index.m3u8",
		visionOSProfile(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if resource.ExpiresAt == nil ||
		resource.ExpiresAt.Unix() != 2000000000 {
		t.Fatal("manifest expiry was not parsed")
	}

	if resource.Headers.Get("User-Agent") == "" {
		t.Fatal("missing client user agent")
	}
}

func TestYouTubeManifestQueryExpiry(t *testing.T) {
	resource, err := youtubeManifestResource(
		"https://manifest.test/index.m3u8?expire=2000000000",
		visionOSProfile(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if resource.ExpiresAt == nil ||
		resource.ExpiresAt.Unix() != 2000000000 {
		t.Fatal("query expiry was not parsed")
	}
}

func TestYouTubeManifestRejectsUnsupportedURLs(t *testing.T) {
	for _, input := range []string{
		"",
		"/relative.m3u8",
		"http://manifest.test/index.m3u8",
		"https://user:password@manifest.test/index.m3u8",
		"https://manifest.test/index.m3u8?n=challenge",
		"https://manifest.test/api/n/challenge/index.m3u8",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := youtubeManifestResource(
				input,
				visionOSProfile(),
			); err == nil {
				t.Fatal("unsupported URL was accepted")
			}
		})
	}
}
