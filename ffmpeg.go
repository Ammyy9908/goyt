package goyt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrFFmpegNotFound means the FFmpeg executable could not be located.
var ErrFFmpegNotFound = errors.New("goyt: ffmpeg executable not found")

// FFmpegError contains bounded diagnostic output from a failed process.
type FFmpegError struct {
	Operation string
	Details   string
	Err       error
}

func (e *FFmpegError) Error() string {
	message := fmt.Sprintf("goyt: ffmpeg %s failed: %v", e.Operation, e.Err)

	if e.Details != "" {
		message += ": " + e.Details
	}

	return message
}

func (e *FFmpegError) Unwrap() error {
	return e.Err
}

type ffmpegRunner func(
	context.Context,
	string,
	[]string,
	io.Writer,
) error

// FFmpeg invokes an external FFmpeg executable.
//
// Different output paths may be processed concurrently.
// Do not process the same destination concurrently.
type FFmpeg struct {
	path string
	run  ffmpegRunner
}

// NewFFmpeg locates an executable.
//
// An empty path searches PATH for "ffmpeg".
// Locating the executable does not verify its codecs or version.
func NewFFmpeg(path string) (*FFmpeg, error) {
	if path == "" {
		path = "ffmpeg"
	}

	resolved, err := exec.LookPath(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFFmpegNotFound, err)
	}

	return &FFmpeg{
		path: resolved,
		run:  runFFmpeg,
	}, nil
}

func runFFmpeg(
	ctx context.Context,
	path string,
	args []string,
	stderr io.Writer,
) error {
	// Arguments are passed directly. No shell is involved.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stderr = stderr
	cmd.Stdout = stderr

	// Bound waiting for process I/O after cancellation or process exit.
	cmd.WaitDelay = 2 * time.Second

	return cmd.Run()
}

// Merge combines the first video stream from videoPath with the first audio
// stream from audioPath. It copies streams without re-encoding.
//
// Input files remain unchanged.
// An existing destination is replaced only after successful processing.
func (f *FFmpeg) Merge(
	ctx context.Context,
	videoPath string,
	audioPath string,
	destination string,
) error {
	return f.process(
		ctx,
		"merge",
		[]string{videoPath, audioPath},
		[]string{"-map", "0:v:0", "-map", "1:a:0"},
		destination,
	)
}

// Remux copies the first video and audio streams into another container.
//
// Both streams must exist. Additional tracks, subtitles, attachments, and
// metadata preservation are outside this initial API's guarantees.
//
// Codecs must be compatible with the output container.
// No transcoding fallback is performed.
func (f *FFmpeg) Remux(
	ctx context.Context,
	inputPath string,
	destination string,
) error {
	return f.process(
		ctx,
		"remux",
		[]string{inputPath},
		[]string{"-map", "0:v:0", "-map", "0:a:0"},
		destination,
	)
}

// HasEncoder checks if the specified audio encoder is available in FFmpeg.
func (f *FFmpeg) HasEncoder(ctx context.Context, encoder string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if f == nil || f.path == "" || f.run == nil {
		return false, errors.New("goyt: create FFmpeg with NewFFmpeg first")
	}

	encoder = strings.ToLower(strings.TrimSpace(encoder))
	if encoder == "" {
		return false, errors.New("goyt: encoder name is empty")
	}

	diagnostics := &diagnosticTail{limit: 65536}

	err := f.run(ctx, f.path, []string{"-hide_banner", "-encoders"}, diagnostics)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, fmt.Errorf("goyt: probe ffmpeg encoders failed: %w", err)
	}

	lines := strings.Split(string(diagnostics.data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 8 {
			continue
		}
		// Audio encoders start with A in flag position (index 1 in " A....D")
		// e.g. "A....D aac" or " A....D libmp3lame"
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 {
			flags := fields[0]
			if strings.HasPrefix(flags, "A") || (len(flags) >= 2 && flags[0] == 'A') {
				encName := strings.ToLower(fields[1])
				if encName == encoder {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// ConvertAudio converts an input audio media file or local HLS playlist
// to the output format and container specified by AudioOutputSpec.
func (f *FFmpeg) ConvertAudio(
	ctx context.Context,
	inputPath string,
	destination string,
	spec AudioOutputSpec,
	isHLS bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if f == nil || f.path == "" || f.run == nil {
		return errors.New("goyt: create FFmpeg with NewFFmpeg first")
	}

	if destination == "" {
		return errors.New("goyt: destination is empty")
	}

	if spec.Extension == "" || spec.Container == "" {
		return errors.New("goyt: invalid audio output specification")
	}

	if !strings.EqualFold(filepath.Ext(destination), spec.Extension) {
		return fmt.Errorf("goyt: destination extension must be %s", spec.Extension)
	}

	output, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	outputInfo, err := os.Stat(output)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if outputInfo != nil && !outputInfo.Mode().IsRegular() {
		return errors.New("goyt: destination must be a regular file")
	}

	if inputPath == "" {
		return errors.New("goyt: input path is empty")
	}

	absoluteInput, err := filepath.Abs(inputPath)
	if err != nil {
		return err
	}

	inputInfo, err := os.Stat(absoluteInput)
	if err != nil {
		return fmt.Errorf("goyt: inspect input: %w", err)
	}

	if !inputInfo.Mode().IsRegular() {
		return errors.New("goyt: inputs must be regular local files")
	}

	if absoluteInput == output || (outputInfo != nil && os.SameFile(inputInfo, outputInfo)) {
		return errors.New("goyt: output must differ from input files")
	}

	temp, err := os.CreateTemp(
		filepath.Dir(output),
		".goyt-ffmpeg-*"+spec.Extension,
	)
	if err != nil {
		return err
	}

	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Close(); err != nil {
		return err
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
	}

	if isHLS {
		// Supported HLS presentations are unencrypted, fully downloaded to disk, and
		// reference only local media files. Restrict FFmpeg to local file access only.
		// Note that while protocol whitelisting restricts external protocol/network schemes,
		// it is not a full filesystem sandbox.
		args = append(args,
			"-copyts", "-start_at_zero",
			"-protocol_whitelist", "file",
			"-allowed_extensions", "m3u8,ts,aac",
			"-f", "hls",
		)
	}

	args = append(args, "-i", absoluteInput)
	args = append(args,
		"-vn",
		"-map", "0:a:0",
	)

	if spec.Copy {
		args = append(args, "-c:a", "copy")
	} else {
		if spec.Encoder == "" {
			return errors.New("goyt: audio encoder is required when not copying")
		}
		args = append(args, "-c:a", spec.Encoder)
		if (spec.Encoder == "libmp3lame" || spec.ResolvedCodec == "mp3") && spec.Quality >= 0 {
			args = append(args, "-q:a", fmt.Sprintf("%d", spec.Quality))
		}
		if spec.Bitrate != "" {
			args = append(args, "-b:a", spec.Bitrate)
		}
	}

	args = append(args,
		"-f", spec.Container,
		tempPath,
	)

	diagnostics := &diagnosticTail{limit: 8192}

	if err := f.run(ctx, f.path, args, diagnostics); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		return &FFmpegError{
			Operation: "audio conversion",
			Details:   strings.TrimSpace(string(diagnostics.data)),
			Err:       err,
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		return err
	}

	if info.Size() == 0 {
		return errors.New("goyt: ffmpeg produced an empty output")
	}

	if err := os.Rename(tempPath, output); err != nil {
		return fmt.Errorf("goyt: commit ffmpeg output: %w", err)
	}

	return nil
}

// ConvertToMP3 converts an input audio media file or local HLS playlist
// to an MP3 file using libmp3lame with the specified VBR quality (0–9).
//
// Lower quality values request higher VBR quality (e.g. 0 is highest quality,
// 2 is high quality default, 9 is lowest quality).
// Video streams are explicitly excluded.
func (f *FFmpeg) ConvertToMP3(
	ctx context.Context,
	inputPath string,
	destination string,
	quality int,
	isHLS bool,
) error {
	if quality < 0 || quality > 9 {
		return errors.New("goyt: audio quality must be between 0 and 9 (inclusive)")
	}

	spec := AudioOutputSpec{
		RequestedFormat: AudioFormatMP3,
		ResolvedCodec:   "mp3",
		Container:       "mp3",
		Extension:       ".mp3",
		Copy:            false,
		Encoder:         "libmp3lame",
		Quality:         quality,
	}

	return f.ConvertAudio(ctx, inputPath, destination, spec, isHLS)
}

func (f *FFmpeg) process(
	ctx context.Context,
	operation string,
	inputs []string,
	mapping []string,
	destination string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if f == nil || f.path == "" || f.run == nil {
		return errors.New("goyt: create FFmpeg with NewFFmpeg first")
	}

	if destination == "" {
		return errors.New("goyt: destination is empty")
	}

	output, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	muxer, err := outputMuxer(output)
	if err != nil {
		return err
	}

	outputInfo, err := os.Stat(output)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if outputInfo != nil && !outputInfo.Mode().IsRegular() {
		return errors.New("goyt: destination must be a regular file")
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-y",
	}

	if operation == "local-hls" {
		if len(inputs) != 1 {
			return errors.New("goyt: local HLS requires one playlist input")
		}

		args = append(args, "-copyts", "-start_at_zero")
	}

	for _, input := range inputs {
		if input == "" {
			return errors.New("goyt: input path is empty")
		}

		absolute, err := filepath.Abs(input)
		if err != nil {
			return err
		}

		info, err := os.Stat(absolute)
		if err != nil {
			return fmt.Errorf("goyt: inspect input: %w", err)
		}

		if !info.Mode().IsRegular() {
			return errors.New("goyt: inputs must be regular local files")
		}

		if absolute == output ||
			(outputInfo != nil && os.SameFile(info, outputInfo)) {
			return errors.New("goyt: output must differ from input files")
		}

		if operation == "local-hls" {
			// These playlists are generated by HLSDownloader and reference only
			// downloaded local segments. FFmpeg must not fetch remote resources.
			args = append(args,
				"-protocol_whitelist", "file",
				"-allowed_extensions", "m3u8,ts,aac",
				"-f", "hls",
			)
		}

		args = append(args, "-i", absolute)
	}

	// Create the temporary output beside the destination so the final rename
	// stays on the same filesystem. The parent directory must already exist.
	temp, err := os.CreateTemp(
		filepath.Dir(output),
		".goyt-ffmpeg-*",
	)
	if err != nil {
		return err
	}

	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Close(); err != nil {
		return err
	}

	args = append(args, mapping...)
	args = append(args,
		"-c", "copy",
		"-f", muxer,
		tempPath,
	)

	diagnostics := &diagnosticTail{limit: 8192}

	if err := f.run(ctx, f.path, args, diagnostics); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		return &FFmpegError{
			Operation: operation,
			Details:   strings.TrimSpace(string(diagnostics.data)),
			Err:       err,
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		return err
	}

	if info.Size() == 0 {
		return errors.New("goyt: ffmpeg produced an empty output")
	}

	if err := os.Rename(tempPath, output); err != nil {
		return fmt.Errorf("goyt: commit ffmpeg output: %w", err)
	}

	return nil
}

func outputMuxer(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "mp4", nil
	case ".webm":
		return "webm", nil
	case ".mkv":
		return "matroska", nil
	default:
		return "", errors.New(
			"goyt: output extension must be .mp4, .webm, or .mkv",
		)
	}
}

// diagnosticTail retains only the last limit bytes of FFmpeg stderr.
type diagnosticTail struct {
	data  []byte
	limit int
}

func (w *diagnosticTail) Write(p []byte) (int, error) {
	n := len(p)

	if w.limit <= 0 {
		return n, nil
	}

	if len(p) >= w.limit {
		w.data = append(w.data[:0], p[len(p)-w.limit:]...)
		return n, nil
	}

	overflow := len(w.data) + len(p) - w.limit
	if overflow > 0 {
		copy(w.data, w.data[overflow:])
		w.data = w.data[:len(w.data)-overflow]
	}

	w.data = append(w.data, p...)
	return n, nil
}
