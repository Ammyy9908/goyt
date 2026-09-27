package goyt

import "testing"

func TestOriginalAudioNameMarkers(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"American English - original", true},
		{"日本語 - original", true},
		{"Spanish (original)", true},
		{"English [original]", true},
		{"Original", true},
		{"[original]", true},
		{"English - ORIGINAL", true},

		{"", false},
		{"English", false},
		{"Original Soundtrack", false},
		{"Originally English", false},
		{"not original", false},
		{"English - original commentary", false},
		{"English (original) - dubbed", false},
		{"English - dubbed (original)", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOriginalAudioName(tc.name); got != tc.want {
				t.Fatalf(
					"isOriginalAudioName(%q) = %v; want %v",
					tc.name, got, tc.want,
				)
			}
		})
	}
}

func TestMultipleOriginalAudioMarkersWarn(t *testing.T) {
	master := &HLSMaster{
		Audio: []HLSAudioRendition{
			{
				GroupID:  "audio",
				Name:     "English - original",
				Language: "en",
				URL:      "https://fixture.test/en.m3u8",
			},
			{
				GroupID:  "audio",
				Name:     "Japanese - original",
				Language: "ja",
				URL:      "https://fixture.test/ja.m3u8",
				Default:  true,
			},
		},
	}

	selected, markedOriginal, warning := master.selectAudioLanguage(
		"audio", "",
	)

	if selected == nil || selected.Language != "ja" {
		t.Fatalf("unexpected selection: %+v", selected)
	}
	if !markedOriginal {
		t.Fatal("selected rendition should retain its original marker")
	}
	if warning == "" {
		t.Fatal("multiple original markers must produce a warning")
	}
}
