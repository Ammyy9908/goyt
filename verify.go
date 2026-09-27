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

// VideoVerificationSpec specifies the expected attributes of a verified video file.
type VideoVerificationSpec struct {
	ExpectedContainer  string // "mp4", "webm", "mkv"
	ExpectedVideoCodec string // "h264", "vp9", "av1"
	ExpectedAudioCodec string // "aac", "opus" (empty accepts any compatible audio)
	ExpectedWidth      *int
	ExpectedHeight     *int
}

// Verification describes metadata observed in the completed file.
type Verification struct {
	Duration   time.Duration
	VideoCodec string
	AudioCodec string
	Container  string
	Width      int
	Height     int
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

// VerifyVideo checks the video output against the VideoVerificationSpec:
// exactly one video stream matching spec.ExpectedVideoCodec (and dimensions if supplied),
// exactly one audio stream matching spec.ExpectedAudioCodec (if supplied),
// the expected output container (accounting for ffprobe format-name aliases),
// and duration within tolerance of expected duration.
func (v *Verifier) VerifyVideo(
	ctx context.Context,
	path string,
	spec VideoVerificationSpec,
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
		"format=duration,format_name:stream=codec_type,codec_name,duration,width,height",
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

	return inspectProbeJSONVideoSpec(data, spec, expected)
}

// VerifyMP4 checks the output expected by backward-compatible callers:
// one H.264 video stream, one AAC audio stream, and positive duration in an MP4 container.
func (v *Verifier) VerifyMP4(
	ctx context.Context,
	path string,
	expected *time.Duration,
) (*Verification, error) {
	spec := VideoVerificationSpec{
		ExpectedContainer:  "mp4",
		ExpectedVideoCodec: "h264",
		ExpectedAudioCodec: "aac",
	}
	return v.VerifyVideo(ctx, path, spec, expected)
}

func inspectProbeJSON(
	data []byte,
	expected *time.Duration,
) (*Verification, error) {
	spec := VideoVerificationSpec{
		ExpectedContainer:  "mp4",
		ExpectedVideoCodec: "h264",
		ExpectedAudioCodec: "aac",
	}
	return inspectProbeJSONVideoSpec(data, spec, expected)
}

func inspectProbeJSONVideoSpec(
	data []byte,
	spec VideoVerificationSpec,
	expected *time.Duration,
) (*Verification, error) {
	var probe struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Duration string `json:"duration"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
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

	if spec.ExpectedContainer != "" && probe.Format.FormatName != "" {
		if !isMatchingContainer(probe.Format.FormatName, spec.ExpectedContainer) {
			return nil, fmt.Errorf(
				"goyt: expected %s container, got %q",
				spec.ExpectedContainer,
				probe.Format.FormatName,
			)
		}
	}

	var videoCount, audioCount int
	var detectedVideoCodec, detectedAudioCodec string
	var detectedWidth, detectedHeight int

	for _, stream := range probe.Streams {
		switch stream.Type {
		case "video":
			videoCount++
			detectedVideoCodec = stream.Codec
			detectedWidth = stream.Width
			detectedHeight = stream.Height

			expectedVideo := spec.ExpectedVideoCodec
			if expectedVideo == "" {
				expectedVideo = "h264"
			}
			if !isMatchingVideoCodec(stream.Codec, expectedVideo) {
				return nil, fmt.Errorf(
					"goyt: expected %s video, got %q",
					normalizeCodec(expectedVideo),
					stream.Codec,
				)
			}

			if spec.ExpectedWidth != nil && *spec.ExpectedWidth > 0 {
				if stream.Width != *spec.ExpectedWidth {
					return nil, fmt.Errorf(
						"goyt: expected video width %d, got %d",
						*spec.ExpectedWidth,
						stream.Width,
					)
				}
			}

			if spec.ExpectedHeight != nil && *spec.ExpectedHeight > 0 {
				if stream.Height != *spec.ExpectedHeight {
					return nil, fmt.Errorf(
						"goyt: expected video height %d, got %d",
						*spec.ExpectedHeight,
						stream.Height,
					)
				}
			}

		case "audio":
			audioCount++
			detectedAudioCodec = stream.Codec

			if spec.ExpectedAudioCodec != "" {
				if !isMatchingAudioCodec(stream.Codec, spec.ExpectedAudioCodec) {
					return nil, fmt.Errorf(
						"goyt: expected %s audio, got %q",
						normalizeCodec(spec.ExpectedAudioCodec),
						stream.Codec,
					)
				}
			} else if !knownAudio(stream.Codec) {
				return nil, fmt.Errorf(
					"goyt: unsupported audio codec %q",
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
		VideoCodec: detectedVideoCodec,
		AudioCodec: detectedAudioCodec,
		Container:  probe.Format.FormatName,
		Width:      detectedWidth,
		Height:     detectedHeight,
	}, nil
}

func isMatchingContainer(actualFormatName, expectedContainer string) bool {
	expected := normalizeContainer(expectedContainer)
	tokens := strings.Split(actualFormatName, ",")
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		switch expected {
		case "mp4":
			if tok == "mp4" || tok == "mov" || tok == "m4a" || tok == "3gp" || tok == "3g2" || tok == "mj2" {
				return true
			}
		case "webm":
			if tok == "webm" || tok == "matroska" {
				return true
			}
		case "mkv":
			if tok == "matroska" || tok == "mkv" || tok == "webm" {
				return true
			}
		}
	}
	return false
}

func isMatchingVideoCodec(actual, expected string) bool {
	act := normalizeCodec(actual)
	exp := normalizeCodec(expected)
	return act == exp
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
					normalizeCodec(expectedCodec),
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
	act := normalizeCodec(actual)
	exp := normalizeCodec(expected)
	return act == exp
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
