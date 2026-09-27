package goyt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Verification describes metadata observed in the completed file.
type Verification struct {
	Duration   time.Duration
	VideoCodec string
	AudioCodec string
}

// AudioVerification describes audio metadata observed in the completed file.
type AudioVerification struct {
	Duration   time.Duration
	AudioCodec string
}

// Verifier inspects completed files using ffprobe.
type Verifier struct {
	path string
	run  ffmpegRunner
}

func NewVerifier(path string) (*Verifier, error) {
	if path == "" {
		path = "ffprobe"
	}

	resolved, err := exec.LookPath(path)
	if err != nil {
		return nil, fmt.Errorf("goyt: ffprobe not found: %w", err)
	}

	return &Verifier{
		path: resolved,
		run:  runFFmpeg,
	}, nil
}

// VerifyMP4 checks the output expected by the current YouTube CLI:
// one H.264 video stream, one AAC audio stream, and positive duration.
//
// If expected is supplied, duration must be within the larger of
// two seconds or one percent of the expected duration.
//
// This checks metadata, not every encoded frame.
func (v *Verifier) VerifyMP4(
	ctx context.Context,
	path string,
	expected *time.Duration,
) (*Verification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if v == nil || v.run == nil {
		return nil, errors.New("goyt: create Verifier with NewVerifier")
	}

	if expected != nil && *expected <= 0 {
		return nil, errors.New("goyt: expected duration must be positive")
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("goyt: output is not a non-empty regular file")
	}

	// Ask ffprobe to write its JSON to a temporary file.
	// The runner's writer remains reserved for stderr diagnostics.
	file, err := os.CreateTemp("", "goyt-probe-*.json")
	if err != nil {
		return nil, err
	}
	probePath := file.Name()
	defer os.Remove(probePath)

	if err := file.Close(); err != nil {
		return nil, err
	}

	diagnostics := &diagnosticTail{limit: 8192}

	args := []string{
		"-v", "error",
		"-show_entries",
		"format=duration:stream=codec_type,codec_name,duration",
		"-of", "json",
		"-o", probePath,
		absolute,
	}

	if err := v.run(ctx, v.path, args, diagnostics); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf(
			"goyt: ffprobe failed: %w; %s",
			err,
			diagnostics.data,
		)
	}

	data, err := os.ReadFile(probePath)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return inspectProbeJSON(data, expected)
}

func inspectProbeJSON(
	data []byte,
	expected *time.Duration,
) (*Verification, error) {
	var probe struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Duration string `json:"duration"`
		} `json:"streams"`

		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("goyt: invalid ffprobe JSON: %w", err)
	}

	seconds, err := parseMediaSeconds(probe.Format.Duration)
	if err != nil {
		return nil, err
	}

	var videoCount, audioCount int

	for _, stream := range probe.Streams {
		switch stream.Type {
		case "video":
			videoCount++
			if stream.Codec != "h264" {
				return nil, fmt.Errorf(
					"goyt: expected H.264 video, got %q",
					stream.Codec,
				)
			}

		case "audio":
			audioCount++
			if stream.Codec != "aac" {
				return nil, fmt.Errorf(
					"goyt: expected AAC audio, got %q",
					stream.Codec,
				)
			}

		default:
			continue
		}

		// Check individual stream durations when ffprobe exposes them.
		if expected != nil &&
			stream.Duration != "" &&
			stream.Duration != "N/A" {
			actual, err := parseMediaSeconds(stream.Duration)
			if err != nil {
				return nil, err
			}

			if err := compareDuration(actual, *expected); err != nil {
				return nil, fmt.Errorf("%s stream: %w", stream.Type, err)
			}
		}
	}

	if videoCount != 1 || audioCount != 1 {
		return nil, fmt.Errorf(
			"goyt: expected one video and one audio stream, got %d and %d",
			videoCount,
			audioCount,
		)
	}

	if expected != nil {
		if err := compareDuration(seconds, *expected); err != nil {
			return nil, err
		}
	}

	return &Verification{
		Duration:   time.Duration(seconds * float64(time.Second)),
		VideoCodec: "h264",
		AudioCodec: "aac",
	}, nil
}

// VerifyAudio checks the audio output against the AudioOutputSpec:
// exactly one audio stream matching spec.ResolvedCodec, zero video streams,
// and duration within tolerance of expected duration.
func (v *Verifier) VerifyAudio(
	ctx context.Context,
	path string,
	spec AudioOutputSpec,
	expected *time.Duration,
) (*AudioVerification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if v == nil || v.run == nil {
		return nil, errors.New("goyt: create Verifier with NewVerifier")
	}

	if expected != nil && *expected <= 0 {
		return nil, errors.New("goyt: expected duration must be positive")
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("goyt: output is not a non-empty regular file")
	}

	file, err := os.CreateTemp("", "goyt-probe-*.json")
	if err != nil {
		return nil, err
	}
	probePath := file.Name()
	defer os.Remove(probePath)

	if err := file.Close(); err != nil {
		return nil, err
	}

	diagnostics := &diagnosticTail{limit: 8192}

	args := []string{
		"-v", "error",
		"-show_entries",
		"format=duration,format_name:stream=codec_type,codec_name,duration",
		"-of", "json",
		"-o", probePath,
		absolute,
	}

	if err := v.run(ctx, v.path, args, diagnostics); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf(
			"goyt: ffprobe failed: %w; %s",
			err,
			diagnostics.data,
		)
	}

	data, err := os.ReadFile(probePath)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return inspectProbeJSONAudioSpec(data, spec, expected)
}

// VerifyMP3 checks the output expected in audio-only MP3 mode:
// exactly one MP3 audio stream, zero video streams, and positive duration.
func (v *Verifier) VerifyMP3(
	ctx context.Context,
	path string,
	expected *time.Duration,
) (*AudioVerification, error) {
	spec := AudioOutputSpec{
		RequestedFormat: AudioFormatMP3,
		ResolvedCodec:   "mp3",
		Container:       "mp3",
		Extension:       ".mp3",
	}
	return v.VerifyAudio(ctx, path, spec, expected)
}

func inspectProbeJSONAudio(
	data []byte,
	expected *time.Duration,
) (*AudioVerification, error) {
	spec := AudioOutputSpec{
		RequestedFormat: AudioFormatMP3,
		ResolvedCodec:   "mp3",
		Container:       "mp3",
		Extension:       ".mp3",
	}
	return inspectProbeJSONAudioSpec(data, spec, expected)
}

func inspectProbeJSONAudioSpec(
	data []byte,
	spec AudioOutputSpec,
	expected *time.Duration,
) (*AudioVerification, error) {
	var probe struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Duration string `json:"duration"`
		} `json:"streams"`

		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}

	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("goyt: invalid ffprobe JSON: %w", err)
	}

	seconds, err := parseMediaSeconds(probe.Format.Duration)
	if err != nil {
		return nil, err
	}

	var videoCount, audioCount int
	var detectedCodec string

	for _, stream := range probe.Streams {
		switch stream.Type {
		case "video":
			videoCount++

		case "audio":
			audioCount++
			detectedCodec = stream.Codec
			expectedCodec := spec.ResolvedCodec
			if expectedCodec == "" {
				expectedCodec = "mp3"
			}
			if !isMatchingAudioCodec(stream.Codec, expectedCodec) {
				return nil, fmt.Errorf(
					"goyt: expected %s audio, got %q",
					expectedCodec,
					stream.Codec,
				)
			}

			if expected != nil &&
				stream.Duration != "" &&
				stream.Duration != "N/A" {
				actual, err := parseMediaSeconds(stream.Duration)
				if err != nil {
					return nil, err
				}

				if err := compareDuration(actual, *expected); err != nil {
					return nil, fmt.Errorf("audio stream: %w", err)
				}
			}

		default:
			continue
		}
	}

	if videoCount > 0 {
		return nil, fmt.Errorf(
			"goyt: expected no video streams in audio output, got %d",
			videoCount,
		)
	}

	if audioCount != 1 {
		return nil, fmt.Errorf(
			"goyt: expected exactly one audio stream, got %d",
			audioCount,
		)
	}

	if expected != nil {
		if err := compareDuration(seconds, *expected); err != nil {
			return nil, err
		}
	}

	return &AudioVerification{
		Duration:   time.Duration(seconds * float64(time.Second)),
		AudioCodec: detectedCodec,
	}, nil
}

func isMatchingAudioCodec(actual, expected string) bool {
	act := strings.ToLower(strings.TrimSpace(actual))
	exp := strings.ToLower(strings.TrimSpace(expected))
	if act == exp {
		return true
	}
	// PCM variant matching
	if strings.HasPrefix(exp, "pcm_") && strings.HasPrefix(act, "pcm_") {
		return act == exp
	}
	return false
}

func parseMediaSeconds(value string) (float64, error) {
	seconds, err := strconv.ParseFloat(value, 64)
	maxSeconds := float64(math.MaxInt64) / float64(time.Second)

	if err != nil ||
		math.IsNaN(seconds) ||
		math.IsInf(seconds, 0) ||
		seconds <= 0 ||
		seconds >= maxSeconds {
		return 0, errors.New("goyt: missing or invalid output duration")
	}

	return seconds, nil
}

func compareDuration(actual float64, expected time.Duration) error {
	tolerance := math.Max(2, expected.Seconds()*0.01)

	if math.Abs(actual-expected.Seconds()) > tolerance {
		return fmt.Errorf(
			"goyt: duration mismatch: got %.3fs, expected %.3fs (tolerance %.3fs)",
			actual,
			expected.Seconds(),
			tolerance,
		)
	}

	return nil
}

// CheckDecode decodes the first video and audio streams without saving output.
// Unlike metadata verification, this reads the entire file.
func (f *FFmpeg) CheckDecode(
	ctx context.Context,
	path string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if f == nil || f.run == nil {
		return errors.New("goyt: create FFmpeg with NewFFmpeg")
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	diagnostics := &diagnosticTail{limit: 8192}

	err = f.run(ctx, f.path, []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-xerror",
		"-i", absolute,
		"-map", "0:v:0",
		"-map", "0:a:0",
		"-f", "null",
		"-",
	}, diagnostics)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err != nil {
		return &FFmpegError{
			Operation: "decode check",
			Details:   string(diagnostics.data),
			Err:       err,
		}
	}

	return nil
}

// CheckDecodeAudio decodes the first audio stream without saving output.
// It explicitly excludes video.
func (f *FFmpeg) CheckDecodeAudio(
	ctx context.Context,
	path string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if f == nil || f.run == nil {
		return errors.New("goyt: create FFmpeg with NewFFmpeg")
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	diagnostics := &diagnosticTail{limit: 8192}

	err = f.run(ctx, f.path, []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-xerror",
		"-i", absolute,
		"-vn",
		"-map", "0:a:0",
		"-f", "null",
		"-",
	}, diagnostics)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err != nil {
		return &FFmpegError{
			Operation: "audio decode check",
			Details:   string(diagnostics.data),
			Err:       err,
		}
	}

	return nil
}
