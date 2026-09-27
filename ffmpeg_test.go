package goyt

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ffmpegFixture(t *testing.T, directory, name string) string {
	t.Helper()

	path := filepath.Join(directory, name)

	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestFFmpegMerge(t *testing.T) {
	directory := t.TempDir()
	video := ffmpegFixture(t, directory, "video.mp4")
	audio := ffmpegFixture(t, directory, "audio.m4a")
	output := filepath.Join(directory, "merged.mp4")

	ffmpeg := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			expected := []string{
				"-hide_banner",
				"-loglevel", "error",
				"-nostdin",
				"-y",
				"-i", video,
				"-i", audio,
				"-map", "0:v:0",
				"-map", "1:a:0",
				"-c", "copy",
				"-f", "mp4",
			}

			if !reflect.DeepEqual(args[:len(args)-1], expected) {
				t.Fatalf("unexpected arguments: %v", args)
			}

			tempOutput := args[len(args)-1]
			if tempOutput == output {
				t.Fatal("FFmpeg must write to a temporary file")
			}

			return os.WriteFile(tempOutput, []byte("merged"), 0600)
		},
	}

	if err := ffmpeg.Merge(
		context.Background(),
		video,
		audio,
		output,
	); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(output)
	if err != nil || string(data) != "merged" {
		t.Fatalf("unexpected output: %q, error: %v", data, err)
	}

	for _, input := range []string{video, audio} {
		data, err := os.ReadFile(input)
		if err != nil || string(data) != "fixture" {
			t.Fatal("input file was changed")
		}
	}
}

func TestFFmpegRemux(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")
	output := filepath.Join(directory, "output.mkv")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			expected := []string{
				"-hide_banner",
				"-loglevel", "error",
				"-nostdin",
				"-y",
				"-i", input,
				"-map", "0:v:0",
				"-map", "0:a:0",
				"-c", "copy",
				"-f", "matroska",
			}

			if !reflect.DeepEqual(args[:len(args)-1], expected) {
				t.Fatalf("unexpected remux arguments: %v", args)
			}

			return os.WriteFile(
				args[len(args)-1],
				[]byte("remuxed"),
				0600,
			)
		},
	}

	if err := ffmpeg.Remux(
		context.Background(),
		input,
		output,
	); err != nil {
		t.Fatal(err)
	}
}

func TestFFmpegFailurePreservesDestination(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")
	output := ffmpegFixture(t, directory, "output.mkv")
	cause := errors.New("process failed")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			io.WriteString(stderr, "codec incompatible with container")
			return cause
		},
	}

	err := ffmpeg.Remux(context.Background(), input, output)

	var processErr *FFmpegError
	if !errors.As(err, &processErr) {
		t.Fatalf("got %v, want FFmpegError", err)
	}

	if !errors.Is(err, cause) {
		t.Fatal("underlying process error was lost")
	}

	if !strings.Contains(processErr.Details, "codec incompatible") {
		t.Fatal("missing FFmpeg diagnostics")
	}

	data, err := os.ReadFile(output)
	if err != nil || string(data) != "fixture" {
		t.Fatal("failed processing changed the destination")
	}

	leftovers, err := filepath.Glob(
		filepath.Join(directory, ".goyt-ffmpeg-*"),
	)
	if err != nil || len(leftovers) != 0 {
		t.Fatal("temporary output was not cleaned up")
	}
}

func TestFFmpegCancellation(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")
	output := filepath.Join(directory, "output.mkv")

	ctx, cancel := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	defer cancel()

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			ctx context.Context,
			path string,
			args []string,
			stderr io.Writer,
		) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
	}

	err := ffmpeg.Remux(ctx, input, output)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}

	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation should not create the destination")
	}
}

func TestFFmpegRejectsSameInputAndOutput(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			context.Context,
			string,
			[]string,
			io.Writer,
		) error {
			t.Fatal("invalid paths should not reach FFmpeg")
			return nil
		},
	}

	if err := ffmpeg.Remux(
		context.Background(),
		input,
		input,
	); err == nil {
		t.Fatal("same input and output should be rejected")
	}
}

func TestFFmpegRejectsEmptyOutput(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "input.mp4")

	ffmpeg := &FFmpeg{
		path: "fake",
		run: func(
			context.Context,
			string,
			[]string,
			io.Writer,
		) error {
			// Simulate a process that succeeds without writing anything.
			return nil
		},
	}

	err := ffmpeg.Remux(
		context.Background(),
		input,
		filepath.Join(directory, "output.mkv"),
	)

	if err == nil {
		t.Fatal("empty output should be rejected")
	}
}

func TestFFmpegMissingExecutable(t *testing.T) {
	_, err := NewFFmpeg(
		filepath.Join(t.TempDir(), "missing-ffmpeg"),
	)

	if !errors.Is(err, ErrFFmpegNotFound) {
		t.Fatalf("got %v, want ErrFFmpegNotFound", err)
	}
}

func TestFFmpegDiagnosticLimit(t *testing.T) {
	tail := &diagnosticTail{limit: 8}

	tail.Write([]byte("123456"))
	tail.Write([]byte("7890"))

	if string(tail.data) != "34567890" {
		t.Fatalf("unexpected diagnostic tail: %q", tail.data)
	}

	tail.Write([]byte("abcdefghijkl"))

	if string(tail.data) != "efghijkl" {
		t.Fatalf("unexpected diagnostic tail: %q", tail.data)
	}
}

func TestFFmpegConvertToMP3(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "audio.aac")
	output := filepath.Join(directory, "output.mp3")

	t.Run("converts media file with quality mapping and excludes video", func(t *testing.T) {
		var capturedArgs []string
		ffmpeg := &FFmpeg{
			path: "fake-ffmpeg",
			run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
				capturedArgs = args
				tempOutput := args[len(args)-1]
				return os.WriteFile(tempOutput, []byte("mp3data"), 0600)
			},
		}

		err := ffmpeg.ConvertToMP3(context.Background(), input, output, 3, false)
		if err != nil {
			t.Fatalf("ConvertToMP3 failed: %v", err)
		}

		expectedPrefix := []string{
			"-hide_banner",
			"-loglevel", "error",
			"-nostdin",
			"-y",
			"-i", input,
			"-vn",
			"-map", "0:a:0",
			"-c:a", "libmp3lame",
			"-q:a", "3",
			"-f", "mp3",
		}

		if !reflect.DeepEqual(capturedArgs[:len(capturedArgs)-1], expectedPrefix) {
			t.Fatalf("got args %v, want prefix %v", capturedArgs, expectedPrefix)
		}

		data, err := os.ReadFile(output)
		if err != nil || string(data) != "mp3data" {
			t.Fatalf("unexpected output file content: %q, err: %v", data, err)
		}
	})

	t.Run("converts HLS local playlist with protocol whitelist", func(t *testing.T) {
		var capturedArgs []string
		ffmpeg := &FFmpeg{
			path: "fake-ffmpeg",
			run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
				capturedArgs = args
				tempOutput := args[len(args)-1]
				return os.WriteFile(tempOutput, []byte("mp3data"), 0600)
			},
		}

		hlsInput := ffmpegFixture(t, directory, "audio.m3u8")
		hlsOutput := filepath.Join(directory, "hls_output.mp3")

		err := ffmpeg.ConvertToMP3(context.Background(), hlsInput, hlsOutput, 0, true)
		if err != nil {
			t.Fatalf("ConvertToMP3 HLS failed: %v", err)
		}

		expectedPrefix := []string{
			"-hide_banner",
			"-loglevel", "error",
			"-nostdin",
			"-y",
			"-copyts", "-start_at_zero",
			"-protocol_whitelist", "file",
			"-allowed_extensions", "m3u8,ts,aac",
			"-f", "hls",
			"-i", hlsInput,
			"-vn",
			"-map", "0:a:0",
			"-c:a", "libmp3lame",
			"-q:a", "0",
			"-f", "mp3",
		}

		if !reflect.DeepEqual(capturedArgs[:len(capturedArgs)-1], expectedPrefix) {
			t.Fatalf("got args %v, want prefix %v", capturedArgs, expectedPrefix)
		}
	})

	t.Run("rejects invalid quality values", func(t *testing.T) {
		ffmpeg := &FFmpeg{path: "fake", run: func(context.Context, string, []string, io.Writer) error { return nil }}

		if err := ffmpeg.ConvertToMP3(context.Background(), input, output, -1, false); err == nil {
			t.Error("expected error for quality -1, got nil")
		}
		if err := ffmpeg.ConvertToMP3(context.Background(), input, output, 10, false); err == nil {
			t.Error("expected error for quality 10, got nil")
		}
	})

	t.Run("rejects non-mp3 destination", func(t *testing.T) {
		ffmpeg := &FFmpeg{path: "fake", run: func(context.Context, string, []string, io.Writer) error { return nil }}

		if err := ffmpeg.ConvertToMP3(context.Background(), input, filepath.Join(directory, "out.aac"), 2, false); err == nil {
			t.Error("expected error for non-mp3 extension, got nil")
		}
	})
}

func TestFFmpegConvertToMP3FailurePreservesDestination(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "audio.aac")
	output := filepath.Join(directory, "existing.mp3")

	// Write existing content to destination
	if err := os.WriteFile(output, []byte("original-unmodified-content"), 0600); err != nil {
		t.Fatal(err)
	}

	ffmpeg := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
			return errors.New("simulated conversion failure")
		},
	}

	err := ffmpeg.ConvertToMP3(context.Background(), input, output, 2, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "original-unmodified-content" {
		t.Fatalf("destination was modified or corrupted: %q, err: %v", data, readErr)
	}
}

func TestFFmpegConvertAudioFailurePreservesDestination(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "audio.aac")
	output := filepath.Join(directory, "existing.flac")

	if err := os.WriteFile(output, []byte("original-audio-destination"), 0600); err != nil {
		t.Fatal(err)
	}

	ffmpeg := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
			return errors.New("simulated conversion failure")
		},
	}

	spec := AudioOutputSpec{
		RequestedFormat: AudioFormatFLAC,
		ResolvedCodec:   "flac",
		Container:       "flac",
		Extension:       ".flac",
		Encoder:         "flac",
	}

	err := ffmpeg.ConvertAudio(context.Background(), input, output, spec, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "original-audio-destination" {
		t.Fatalf("destination was modified: %q, err: %v", data, readErr)
	}
}

func TestFFmpegConvertAudio(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "audio.media")

	tests := []struct {
		name         string
		destName     string
		spec         AudioOutputSpec
		isHLS        bool
		wantArgsPart []string
	}{
		{
			name:     "m4a stream copy",
			destName: "output.m4a",
			spec: AudioOutputSpec{
				RequestedFormat: "m4a",
				ResolvedCodec:   "aac",
				Container:       "ipod",
				Extension:       ".m4a",
				Copy:            true,
				Quality:         -1,
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "copy", "-f", "ipod"},
		},
		{
			name:     "mp3 quality 2 encode",
			destName: "output.mp3",
			spec: AudioOutputSpec{
				RequestedFormat: "mp3",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Copy:            false,
				Encoder:         "libmp3lame",
				Quality:         2,
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "libmp3lame", "-q:a", "2", "-f", "mp3"},
		},
		{
			name:     "mp3 bitrate encode",
			destName: "output.mp3",
			spec: AudioOutputSpec{
				RequestedFormat: "mp3",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Copy:            false,
				Encoder:         "libmp3lame",
				Quality:         -1,
				Bitrate:         "192k",
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "libmp3lame", "-b:a", "192k", "-f", "mp3"},
		},
		{
			name:     "opus encode with bitrate",
			destName: "output.opus",
			spec: AudioOutputSpec{
				RequestedFormat: "opus",
				ResolvedCodec:   "opus",
				Container:       "opus",
				Extension:       ".opus",
				Copy:            false,
				Encoder:         "libopus",
				Quality:         -1,
				Bitrate:         "128k",
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "libopus", "-b:a", "128k", "-f", "opus"},
		},
		{
			name:     "flac encode",
			destName: "output.flac",
			spec: AudioOutputSpec{
				RequestedFormat: "flac",
				ResolvedCodec:   "flac",
				Container:       "flac",
				Extension:       ".flac",
				Copy:            false,
				Encoder:         "flac",
				Quality:         -1,
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "flac", "-f", "flac"},
		},
		{
			name:     "wav encode",
			destName: "output.wav",
			spec: AudioOutputSpec{
				RequestedFormat: "wav",
				ResolvedCodec:   "pcm_s16le",
				Container:       "wav",
				Extension:       ".wav",
				Copy:            false,
				Encoder:         "pcm_s16le",
				Quality:         -1,
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "pcm_s16le", "-f", "wav"},
		},
		{
			name:     "alac encode",
			destName: "output.m4a",
			spec: AudioOutputSpec{
				RequestedFormat: "alac",
				ResolvedCodec:   "alac",
				Container:       "ipod",
				Extension:       ".m4a",
				Copy:            false,
				Encoder:         "alac",
				Quality:         -1,
			},
			wantArgsPart: []string{"-vn", "-map", "0:a:0", "-c:a", "alac", "-f", "ipod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := filepath.Join(directory, tt.destName)
			var capturedArgs []string
			ffmpeg := &FFmpeg{
				path: "fake-ffmpeg",
				run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
					capturedArgs = args
					tempOutput := args[len(args)-1]
					return os.WriteFile(tempOutput, []byte("processed-audio"), 0600)
				},
			}

			err := ffmpeg.ConvertAudio(context.Background(), input, output, tt.spec, tt.isHLS)
			if err != nil {
				t.Fatalf("ConvertAudio failed: %v", err)
			}

			// Check that arguments contain the expected sequence
			joined := strings.Join(capturedArgs, " ")
			joinedPart := strings.Join(tt.wantArgsPart, " ")
			if !strings.Contains(joined, joinedPart) {
				t.Errorf("arguments %q do not contain %q", joined, joinedPart)
			}
		})
	}
}

func TestFFmpegHasEncoder(t *testing.T) {
	encoderOutput := `
Encoders:
 V..... = Video
 A..... = Audio
 S..... = Subtitle
 ------
 V..... libsvtav1            SVT-AV1(Scalable Video Technology for AV1) encoder (codec av1)
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 V....D libvpx-vp9           libvpx VP9 (codec vp9)
 A....D aac                  AAC (Advanced Audio Coding)
 A....D alac                 ALAC (Apple Lossless Audio Codec)
 A....D flac                 FLAC (Free Lossless Audio Codec)
 A....D libmp3lame           libmp3lame MP3 (codec mp3)
 A....D libopus              libopus Opus (codec opus)
 A....D pcm_s16le            PCM signed 16-bit little-endian
 S..... srt                  SubRip subtitle (codec subrip)
`
	ffmpeg := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
			if len(args) >= 2 && args[1] == "-encoders" {
				stderr.Write([]byte(encoderOutput))
			}
			return nil
		},
	}

	for _, enc := range []string{"libsvtav1", "libx264", "libvpx-vp9", "aac", "alac", "flac", "libmp3lame", "libopus", "pcm_s16le", "srt"} {
		has, err := ffmpeg.HasEncoder(context.Background(), enc)
		if err != nil || !has {
			t.Errorf("expected encoder %q to be found, got has=%v, err=%v", enc, has, err)
		}
	}

	hasMissing, err := ffmpeg.HasEncoder(context.Background(), "nonexistent_encoder")
	if err != nil || hasMissing {
		t.Errorf("expected nonexistent_encoder to be false, got has=%v, err=%v", hasMissing, err)
	}
}

func TestFFmpegCheckDecodeAudio(t *testing.T) {
	directory := t.TempDir()
	input := ffmpegFixture(t, directory, "song.mp3")

	var capturedArgs []string
	ffmpeg := &FFmpeg{
		path: "fake-ffmpeg",
		run: func(ctx context.Context, path string, args []string, stderr io.Writer) error {
			capturedArgs = args
			return nil
		},
	}

	if err := ffmpeg.CheckDecodeAudio(context.Background(), input); err != nil {
		t.Fatalf("CheckDecodeAudio failed: %v", err)
	}

	expected := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-xerror",
		"-i", input,
		"-vn",
		"-map", "0:a:0",
		"-f", "null",
		"-",
	}

	if !reflect.DeepEqual(capturedArgs, expected) {
		t.Fatalf("got decode args %v, want %v", capturedArgs, expected)
	}
}
