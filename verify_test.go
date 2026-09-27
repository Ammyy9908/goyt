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

func TestVerifyRejectsInvalidAudioOutput(t *testing.T) {
	expected := 180 * time.Second

	tests := []struct {
		name      string
		spec      AudioOutputSpec
		probeJSON string
	}{
		{
			name: "pcm_s16le expected, got pcm_s24le",
			spec: AudioOutputSpec{RequestedFormat: "wav", ResolvedCodec: "pcm_s16le"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"pcm_s24le","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
		},
		{
			name: "pcm_s16le expected, got pcm_f32le",
			spec: AudioOutputSpec{RequestedFormat: "wav", ResolvedCodec: "pcm_s16le"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"pcm_f32le","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
		},
		{
			name: "opus expected, got aac",
			spec: AudioOutputSpec{RequestedFormat: "opus", ResolvedCodec: "opus"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"aac","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
		},
		{
			name: "flac expected, got alac",
			spec: AudioOutputSpec{RequestedFormat: "flac", ResolvedCodec: "flac"},
			probeJSON: `{
				"streams": [{"codec_type":"audio","codec_name":"alac","duration":"180.0"}],
				"format":{"duration":"180.0"}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := inspectProbeJSONAudioSpec([]byte(tt.probeJSON), tt.spec, &expected); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestVerifyVideoSpec(t *testing.T) {
	expected := 120 * time.Second
	w1920 := 1920
	h1080 := 1080

	t.Run("valid combinations accepted", func(t *testing.T) {
		tests := []struct {
			name      string
			spec      VideoVerificationSpec
			probeJSON string
			wantVideo string
			wantAudio string
		}{
			{
				name: "AV1 AAC MP4",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mp4",
					ExpectedVideoCodec: "av1",
					ExpectedAudioCodec: "aac",
					ExpectedWidth:      &w1920,
					ExpectedHeight:     &h1080,
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"av01","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
				wantVideo: "av01",
				wantAudio: "aac",
			},
			{
				name: "VP9 Opus WebM",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "webm",
					ExpectedVideoCodec: "vp9",
					ExpectedAudioCodec: "opus",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"vp9","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "vp9",
				wantAudio: "opus",
			},
			{
				name: "AV1 Opus WebM",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "webm",
					ExpectedVideoCodec: "av1",
					ExpectedAudioCodec: "opus",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"av1","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "av1",
				wantAudio: "opus",
			},
			{
				name: "H264 Opus MKV",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mkv",
					ExpectedVideoCodec: "h264",
					ExpectedAudioCodec: "opus",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "h264",
				wantAudio: "opus",
			},
			{
				name: "AV1 (av01.0.05m.08) + mp4a.40.2 in MP4 accepted against ffprobe av1/aac",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mp4",
					ExpectedVideoCodec: "av01.0.05m.08",
					ExpectedAudioCodec: "mp4a.40.2",
					ExpectedWidth:      &w1920,
					ExpectedHeight:     &h1080,
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"av1","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
				wantVideo: "av1",
				wantAudio: "aac",
			},
			{
				name: "H.264 (avc1.640028) + mp4a.40.5 in MP4 accepted against ffprobe h264/aac",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mp4",
					ExpectedVideoCodec: "avc1.640028",
					ExpectedAudioCodec: "mp4a.40.5",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
				wantVideo: "h264",
				wantAudio: "aac",
			},
			{
				name: "H.264 (avc3.42001e) + mp4a.40.29 in MKV accepted against ffprobe h264/aac",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mkv",
					ExpectedVideoCodec: "avc3.42001e",
					ExpectedAudioCodec: "mp4a.40.29",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "h264",
				wantAudio: "aac",
			},
			{
				name: "VP9 (vp09.00.41.08) + opus in WebM accepted against ffprobe vp9/opus",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "webm",
					ExpectedVideoCodec: "vp09.00.41.08",
					ExpectedAudioCodec: "opus",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"vp9","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "vp9",
				wantAudio: "opus",
			},
			{
				name: "VP9 (vp09.00.41.08) + mp4a.40.2 in MKV accepted against ffprobe vp9/aac",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mkv",
					ExpectedVideoCodec: "vp09.00.41.08",
					ExpectedAudioCodec: "mp4a.40.2",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"vp9","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "vp9",
				wantAudio: "aac",
			},
			{
				name: "AV1 (av01.0.08m.08) + opus in MKV accepted against ffprobe av1/opus",
				spec: VideoVerificationSpec{
					ExpectedContainer:  "mkv",
					ExpectedVideoCodec: "av01.0.08m.08",
					ExpectedAudioCodec: "opus",
				},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"av1","duration":"120.0","width":1920,"height":1080},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
				wantVideo: "av1",
				wantAudio: "opus",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := inspectProbeJSONVideoSpec([]byte(tt.probeJSON), tt.spec, &expected)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result.VideoCodec != tt.wantVideo || result.AudioCodec != tt.wantAudio {
					t.Errorf("got video=%q audio=%q, want video=%q audio=%q", result.VideoCodec, result.AudioCodec, tt.wantVideo, tt.wantAudio)
				}
			})
		}
	})

	t.Run("invalid outputs rejected", func(t *testing.T) {
		tests := []struct {
			name      string
			spec      VideoVerificationSpec
			probeJSON string
		}{
			{
				name: "container mismatch",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "aac"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
			},
			{
				name: "video codec mismatch",
				spec: VideoVerificationSpec{ExpectedContainer: "webm", ExpectedVideoCodec: "vp9", ExpectedAudioCodec: "opus"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
			},
			{
				name: "audio codec mismatch",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "aac"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "AV1 source identifier expected, got h264",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "av01.0.05m.08", ExpectedAudioCodec: "mp4a.40.2"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "AAC source identifier expected, got opus",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "av01.0.05m.08", ExpectedAudioCodec: "mp4a.40.2"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"av1","duration":"120.0"},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "VP9 source identifier expected, got av1",
				spec: VideoVerificationSpec{ExpectedContainer: "webm", ExpectedVideoCodec: "vp09.00.41.08", ExpectedAudioCodec: "opus"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"av1","duration":"120.0"},
						{"codec_type":"audio","codec_name":"opus","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"matroska,webm"}
				}`,
			},
			{
				name: "non-AAC mp4a identifier rejected (mp4a.6b)",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "mp4a.6b"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "non-allowlisted AAC object type rejected (mp4a.40.34)",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "mp4a.40.34"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "unsupported AAC object type rejected (mp4a.40.999)",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "mp4a.40.999"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "malformed mp4a identifier rejected (mp4a.40.)",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "mp4a.40."},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "width mismatch",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "aac", ExpectedWidth: &w1920},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0","width":1280,"height":720},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "height mismatch",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "aac", ExpectedHeight: &h1080},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0","width":1920,"height":720},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
			{
				name: "multiple video streams",
				spec: VideoVerificationSpec{ExpectedContainer: "mp4", ExpectedVideoCodec: "h264", ExpectedAudioCodec: "aac"},
				probeJSON: `{
					"streams": [
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"video","codec_name":"h264","duration":"120.0"},
						{"codec_type":"audio","codec_name":"aac","duration":"120.0"}
					],
					"format":{"duration":"120.0","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}
				}`,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := inspectProbeJSONVideoSpec([]byte(tt.probeJSON), tt.spec, &expected); err == nil {
					t.Fatalf("expected error for %s, got nil", tt.name)
				}
			})
		}
	})
}
