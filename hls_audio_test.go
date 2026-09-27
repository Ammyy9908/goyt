package goyt

import (
	"net/url"
	"strings"
	"testing"
)

func TestSelectSeparateHLSAudio(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="French - dubbed",LANGUAGE="fr",AUTOSELECT=YES,URI="fr.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English - original",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,URI="en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := master.SelectWithAudio(1080)
	if err != nil {
		t.Fatal(err)
	}

	if selected.Audio == nil ||
		selected.Audio.Language != "en" ||
		selected.Audio.URL != "https://media.test/en.m3u8" ||
		!selected.AudioIsOriginal {
		t.Fatalf("incorrect audio selection: %+v", selected.Audio)
	}
}

// Regression Test 1: Translated rendition is marked DEFAULT=YES, while original audio is available.
func TestTranslatedDefaultDoesNotOverrideOriginalAudio(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Español (Latinoamérica) - dubbed",LANGUAGE="es-419",DEFAULT=YES,AUTOSELECT=YES,URI="es.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="American English - original",LANGUAGE="en-US",DEFAULT=NO,AUTOSELECT=YES,URI="en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := master.SelectWithAudio(1080)
	if err != nil {
		t.Fatal(err)
	}

	if selected.Audio == nil {
		t.Fatal("expected audio rendition to be selected")
	}
	if selected.Audio.URL != "https://media.test/en.m3u8" {
		t.Fatalf("selected %q; want original audio en.m3u8", selected.Audio.URL)
	}
	if !selected.AudioIsOriginal {
		t.Fatal("expected AudioIsOriginal to be true")
	}
	if selected.AudioWarning != "" {
		t.Fatalf("unexpected warning: %s", selected.AudioWarning)
	}
}

// Regression Test 2: Non-English original track.
func TestNonEnglishOriginalTrackSelection(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English - dubbed-auto",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,URI="en.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="日本語 - original",LANGUAGE="ja",DEFAULT=NO,AUTOSELECT=YES,URI="ja.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Français - dubbed-auto",LANGUAGE="fr",DEFAULT=NO,AUTOSELECT=YES,URI="fr.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := master.SelectWithAudio(1080)
	if err != nil {
		t.Fatal(err)
	}

	if selected.Audio == nil {
		t.Fatal("expected audio rendition to be selected")
	}
	if selected.Audio.Language != "ja" || selected.Audio.URL != "https://media.test/ja.m3u8" {
		t.Fatalf("selected %+v; want Japanese original track (ja.m3u8)", selected.Audio)
	}
	if !selected.AudioIsOriginal {
		t.Fatal("expected AudioIsOriginal to be true for non-English original")
	}
}

// Regression Test 3: Missing or ambiguous original-track metadata.
func TestAmbiguousOriginalTrackMetadataWarnsAndUsesDefault(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Track A",LANGUAGE="und",DEFAULT=YES,AUTOSELECT=YES,URI="a.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Track B",LANGUAGE="und",DEFAULT=NO,AUTOSELECT=YES,URI="b.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := master.SelectWithAudio(1080)
	if err != nil {
		t.Fatal(err)
	}

	if selected.Audio == nil {
		t.Fatal("expected audio rendition to be selected")
	}
	if selected.Audio.URL != "https://media.test/a.m3u8" {
		t.Fatalf("selected %q; want default a.m3u8", selected.Audio.URL)
	}
	if selected.AudioIsOriginal {
		t.Fatal("ambiguous track must not be marked as AudioIsOriginal")
	}
	if selected.AudioWarning == "" {
		t.Fatal("expected an explicit warning when original track identity cannot be established")
	}
	if !strings.Contains(selected.AudioWarning, "original audio track could not be established") {
		t.Fatalf("unexpected warning message: %q", selected.AudioWarning)
	}
}

// Regression Test 4: Explicit language selection.
func TestExplicitLanguageSelection(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="American English - original",LANGUAGE="en-US",DEFAULT=NO,AUTOSELECT=YES,URI="en.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Deutsch (Deutschland) - dubbed-auto",LANGUAGE="de-DE",DEFAULT=NO,AUTOSELECT=YES,URI="de.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Français - dubbed-auto",LANGUAGE="fr-FR",DEFAULT=YES,AUTOSELECT=YES,URI="fr.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	// Request German (de)
	selectedDe, err := master.SelectWithAudioLanguage(1080, "de")
	if err != nil {
		t.Fatal(err)
	}
	if selectedDe.Audio == nil || selectedDe.Audio.URL != "https://media.test/de.m3u8" {
		t.Fatalf("selected %+v; want German de.m3u8", selectedDe.Audio)
	}

	// Request English (en)
	selectedEn, err := master.SelectWithAudioLanguage(1080, "en")
	if err != nil {
		t.Fatal(err)
	}
	if selectedEn.Audio == nil || selectedEn.Audio.URL != "https://media.test/en.m3u8" {
		t.Fatalf("selected %+v; want English en.m3u8", selectedEn.Audio)
	}

	// Request non-existent language (it)
	_, err = master.SelectWithAudioLanguage(1080, "it")
	if err == nil {
		t.Fatal("expected error for unsupported language request")
	}
}

// Regression Test 5: Track matching across multiple HLS audio groups.
func TestMultipleAudioGroupsMatching(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-1080",NAME="Spanish - dubbed",LANGUAGE="es",DEFAULT=YES,AUTOSELECT=YES,URI="1080-es.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-1080",NAME="English - original",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="1080-en.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-720",NAME="Spanish - dubbed",LANGUAGE="es",DEFAULT=YES,AUTOSELECT=YES,URI="720-es.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-720",NAME="English - original",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="720-en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=4000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio-1080"
video-1080.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720,CODECS="avc1.64001f,mp4a.40.2",AUDIO="audio-720"
video-720.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	// 1080p selection should match audio from audio-1080 group
	selected1080, err := master.SelectWithAudio(1080)
	if err != nil {
		t.Fatal(err)
	}
	if selected1080.Audio == nil || selected1080.Audio.URL != "https://media.test/1080-en.m3u8" {
		t.Fatalf("selected %+v; want 1080-en.m3u8 from group audio-1080", selected1080.Audio)
	}

	// 720p selection should match audio from audio-720 group
	selected720, err := master.SelectWithAudio(720)
	if err != nil {
		t.Fatal(err)
	}
	if selected720.Audio == nil || selected720.Audio.URL != "https://media.test/720-en.m3u8" {
		t.Fatalf("selected %+v; want 720-en.m3u8 from group audio-720", selected720.Audio)
	}
}

func TestMissingAudioGroupFallsBack(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="missing"
video.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720,CODECS="avc1.64001f,mp4a.40.2"
combined.m3u8
`), base)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := master.SelectWithAudio(1080)
	if err != nil {
		t.Fatal(err)
	}

	if selected.Variant.Height != 720 || selected.Audio != nil {
		t.Fatal("expected combined variant fallback")
	}
}

func TestRejectMultipleDefaultAudio(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	_, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="One",DEFAULT=YES,URI="one.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Two",DEFAULT=YES,URI="two.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720,CODECS="avc1.64001f,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)

	if err == nil {
		t.Fatal("multiple default renditions were accepted")
	}
}

func TestSelectAudioOnly(t *testing.T) {
	base, _ := url.Parse("https://media.test/master.m3u8")

	t.Run("selects original audio over default", func(t *testing.T) {
		master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Español - dubbed",LANGUAGE="es",DEFAULT=YES,AUTOSELECT=YES,URI="es.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English - original",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
		if err != nil {
			t.Fatal(err)
		}

		selected, err := master.SelectAudioOnly("")
		if err != nil {
			t.Fatalf("SelectAudioOnly failed: %v", err)
		}
		if selected.Audio.URL != "https://media.test/en.m3u8" || !selected.AudioIsOriginal {
			t.Fatalf("expected English original track, got %+v", selected)
		}
	})

	t.Run("selects requested language", func(t *testing.T) {
		master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Spanish",LANGUAGE="es",DEFAULT=YES,AUTOSELECT=YES,URI="es.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="German",LANGUAGE="de",DEFAULT=NO,AUTOSELECT=YES,URI="de.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
		if err != nil {
			t.Fatal(err)
		}

		selected, err := master.SelectAudioOnly("de")
		if err != nil {
			t.Fatalf("SelectAudioOnly failed: %v", err)
		}
		if selected.Audio.Language != "de" || selected.Audio.URL != "https://media.test/de.m3u8" {
			t.Fatalf("expected German audio track, got %+v", selected)
		}

		// Missing language error
		if _, err := master.SelectAudioOnly("fr"); err == nil {
			t.Fatal("expected error for missing French language, got nil")
		}
	})

	t.Run("explicit language overrides original track preference", func(t *testing.T) {
		master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Español - original",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,URI="es.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Deutsch - dubbed-auto",LANGUAGE="de",DEFAULT=YES,AUTOSELECT=YES,URI="de.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video.m3u8
`), base)
		if err != nil {
			t.Fatal(err)
		}

		selected, err := master.SelectAudioOnly("de")
		if err != nil {
			t.Fatalf("SelectAudioOnly failed: %v", err)
		}
		if selected.Audio.Language != "de" || selected.Audio.URL != "https://media.test/de.m3u8" {
			t.Fatalf("expected German audio track, got %+v", selected)
		}
		if selected.AudioIsOriginal {
			t.Fatal("expected AudioIsOriginal to be false when explicit dubbed language is selected")
		}
	})

	t.Run("rejects when no separate audio renditions available", func(t *testing.T) {
		master, err := ParseHLSMaster([]byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720,CODECS="avc1.64001f,mp4a.40.2"
combined.m3u8
`), base)
		if err != nil {
			t.Fatal(err)
		}

		_, err = master.SelectAudioOnly("")
		if err == nil {
			t.Fatal("expected error when no separate audio renditions exist, got nil")
		}
		if !strings.Contains(err.Error(), "no supported separate audio rendition found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
