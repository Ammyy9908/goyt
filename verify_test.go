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
