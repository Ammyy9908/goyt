package goyt

import (
	"testing"
	"time"
)

func TestVerifyMatchingOutput(t *testing.T) {
	expected := 262 * time.Second

	result, err := inspectProbeJSON([]byte(`{
		"streams": [
			{
				"codec_type":"video",
				"codec_name":"h264",
				"duration":"262.0"
			},
			{
				"codec_type":"audio",
				"codec_name":"aac",
				"duration":"262.106848"
			}
		],
		"format":{"duration":"262.106848"}
	}`), &expected)

	if err != nil {
		t.Fatal(err)
	}

	if result.VideoCodec != "h264" ||
		result.AudioCodec != "aac" ||
		result.Duration < expected {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsInvalidOutput(t *testing.T) {
	expected := 262 * time.Second

	cases := map[string]string{
		"missing audio": `{
			"streams":[{"codec_type":"video","codec_name":"h264"}],
			"format":{"duration":"262"}
		}`,
		"wrong codec": `{
			"streams":[
				{"codec_type":"video","codec_name":"vp9"},
				{"codec_type":"audio","codec_name":"aac"}
			],
			"format":{"duration":"262"}
		}`,
		"short output": `{
			"streams":[
				{"codec_type":"video","codec_name":"h264"},
				{"codec_type":"audio","codec_name":"aac"}
			],
			"format":{"duration":"30"}
		}`,
		"short audio": `{
			"streams":[
				{"codec_type":"video","codec_name":"h264","duration":"262"},
				{"codec_type":"audio","codec_name":"aac","duration":"30"}
			],
			"format":{"duration":"262"}
		}`,
		"invalid duration": `{
			"streams":[
				{"codec_type":"video","codec_name":"h264"},
				{"codec_type":"audio","codec_name":"aac"}
			],
			"format":{"duration":"NaN"}
		}`,
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectProbeJSON(
				[]byte(input),
				&expected,
			); err == nil {
				t.Fatal("invalid output accepted")
			}
		})
	}
}

func TestVerifyMatchingMP3Output(t *testing.T) {
	expected := 180 * time.Second

	result, err := inspectProbeJSONAudio([]byte(`{
		"streams": [
			{
				"codec_type":"audio",
				"codec_name":"mp3",
				"duration":"180.05"
			}
		],
		"format":{"duration":"180.05"}
	}`), &expected)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.AudioCodec != "mp3" || result.Duration < expected {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsInvalidMP3Output(t *testing.T) {
	expected := 180 * time.Second

	cases := map[string]string{
		"video stream present": `{
			"streams":[
				{"codec_type":"video","codec_name":"h264","duration":"180.0"},
				{"codec_type":"audio","codec_name":"mp3","duration":"180.0"}
			],
			"format":{"duration":"180.0"}
		}`,
		"wrong audio codec": `{
			"streams":[
				{"codec_type":"audio","codec_name":"aac","duration":"180.0"}
			],
			"format":{"duration":"180.0"}
		}`,
		"multiple audio streams": `{
			"streams":[
				{"codec_type":"audio","codec_name":"mp3","duration":"180.0"},
				{"codec_type":"audio","codec_name":"mp3","duration":"180.0"}
			],
			"format":{"duration":"180.0"}
		}`,
		"short duration": `{
			"streams":[
				{"codec_type":"audio","codec_name":"mp3","duration":"10.0"}
			],
			"format":{"duration":"10.0"}
		}`,
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectProbeJSONAudio([]byte(input), &expected); err == nil {
				t.Fatal("invalid MP3 output accepted")
			}
		})
	}
}

func TestVerifyMatchingAudioOutput(t *testing.T) {
	expected := 180 * time.Second

	tests := []struct {
		name      string
		spec      AudioOutputSpec
		probeJSON string
		wantCodec string
	}{
		{
			name: "opus audio",
			spec: AudioOutputSpec{RequestedFormat: "opus", ResolvedCodec: "opus"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"opus","duration":"180.05"}],
				"format":{"duration":"180.05"}
			}`,
			wantCodec: "opus",
		},
		{
			name: "flac audio",
			spec: AudioOutputSpec{RequestedFormat: "flac", ResolvedCodec: "flac"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"flac","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
			wantCodec: "flac",
		},
		{
			name: "wav pcm_s16le audio",
			spec: AudioOutputSpec{RequestedFormat: "wav", ResolvedCodec: "pcm_s16le"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"pcm_s16le","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
			wantCodec: "pcm_s16le",
		},
		{
			name: "alac audio",
			spec: AudioOutputSpec{RequestedFormat: "alac", ResolvedCodec: "alac"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"alac","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
			wantCodec: "alac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := inspectProbeJSONAudioSpec([]byte(tt.probeJSON), tt.spec, &expected)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.AudioCodec != tt.wantCodec {
				t.Errorf("got codec %q, want %q", result.AudioCodec, tt.wantCodec)
			}
		})
	}
}
