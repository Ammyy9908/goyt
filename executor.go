package goyt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StreamDownloader is implemented by *Downloader.
type StreamDownloader interface {
	Download(
		context.Context,
		Resource,
		string,
		DownloadOptions,
	) (*DownloadResult, error)
}

// MediaProcessor is implemented by *FFmpeg.
type MediaProcessor interface {
	Merge(context.Context, string, string, string) error
	Remux(context.Context, string, string) error
}

// ExecutionError describes a failed plan execution.
//
// WorkDir identifies retained intermediate files, when applicable.
// The underlying error remains available through errors.Is/errors.As.
type ExecutionError struct {
	Stage   string
	WorkDir string
	Err     error
}

func (e *ExecutionError) Error() string {
	message := fmt.Sprintf("goyt: %s failed: %v", e.Stage, e.Err)

	if e.WorkDir != "" {
		message += "; intermediate files: " + e.WorkDir
	}

	return message
}

func (e *ExecutionError) Unwrap() error {
	return e.Err
}

// ExecutionResult describes the completed output.
type ExecutionResult struct {
	Path      string
	SizeBytes int64

	// WorkDir is non-empty if successful processing left intermediate files
	// because cleanup failed.
	WorkDir string

	// CleanupError does not mean the media operation failed.
	CleanupError error
}

// ExecutionProgress identifies which input stream is being downloaded.
//
// StreamIndex is zero-based.
// Download reports progress for that stream, not the whole job.
// FFmpeg processing progress is not reported in this initial implementation.
type ExecutionProgress struct {
	StreamIndex int
	StreamCount int
	FormatID    string
	Download    Progress
}

type ExecuteOptions struct {
	Download DownloadOptions

	// OnProgress runs synchronously during stream downloads.
	OnProgress func(ExecutionProgress)
}

// Executor coordinates downloads and optional processing.
//
// Construct it with NewExecutor. Dependencies must be concurrency-safe
// if an Executor is shared. Do not execute jobs for the same destination
// concurrently.
type Executor struct {
	downloader StreamDownloader
	processor  MediaProcessor
}

// NewExecutor accepts an optional processor.
//
// Without a processor, only direct-download plans can be executed.
// Dependencies must be non-nil concrete instances, not typed nil pointers.
func NewExecutor(
	downloader StreamDownloader,
	processor MediaProcessor,
) (*Executor, error) {
	if downloader == nil {
		return nil, errors.New("goyt: downloader is required")
	}

	return &Executor{
		downloader: downloader,
		processor:  processor,
	}, nil
}

// Execute downloads a plan and performs any required merge or remux.
//
// The output extension must match the planned container.
// An existing destination is replaced only after its replacement is ready.
//
// Direct downloads retain their normal .part files after interruption.
// Processing jobs retain a separate work directory after failure.
// Resuming a later Execute call from that work directory is not yet implemented.
func (e *Executor) Execute(
	ctx context.Context,
	plan *DownloadPlan,
	destination string,
	options ExecuteOptions,
) (*ExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if e == nil || e.downloader == nil {
		return nil, errors.New("goyt: create Executor with NewExecutor first")
	}

	if err := validateExecutionPlan(plan, destination); err != nil {
		return nil, err
	}

	if options.Download.MaxRetries < 0 ||
		options.Download.RetryDelay < 0 {
		return nil, errors.New("goyt: invalid retry options")
	}

	output, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}

	if info, err := os.Stat(output); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("goyt: destination must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if plan.DirectDownloadable() {
		result, err := e.downloader.Download(
			ctx,
			plan.Streams[0].Resource,
			output,
			executionDownloadOptions(options, plan, 0),
		)
		if err != nil {
			return nil, &ExecutionError{
				Stage: "download",
				Err:   err,
			}
		}

		if result == nil {
			return nil, errors.New("goyt: downloader returned no result")
		}

		return &ExecutionResult{
			Path:      result.Path,
			SizeBytes: result.SizeBytes,
		}, nil
	}

	if e.processor == nil {
		return nil, errors.New(
			"goyt: this plan requires a media processor",
		)
	}

	workDir, err := os.MkdirTemp(
		filepath.Dir(output),
		".goyt-work-*",
	)
	if err != nil {
		return nil, err
	}

	fail := func(stage string, err error) (*ExecutionResult, error) {
		return nil, &ExecutionError{
			Stage:   stage,
			WorkDir: workDir,
			Err:     err,
		}
	}

	inputs := make([]string, len(plan.Streams))

	for i, stream := range plan.Streams {
		if err := ctx.Err(); err != nil {
			return fail("download", err)
		}

		// FFmpeg detects the input format from its contents.
		inputs[i] = filepath.Join(
			workDir,
			fmt.Sprintf("stream-%d.media", i),
		)

		_, err := e.downloader.Download(
			ctx,
			stream.Resource,
			inputs[i],
			executionDownloadOptions(options, plan, i),
		)
		if err != nil {
			return fail(fmt.Sprintf("download stream %d", i), err)
		}

		info, err := os.Stat(inputs[i])
		if err != nil {
			return fail("inspect downloaded stream", err)
		}
		if !info.Mode().IsRegular() {
			return fail(
				"inspect downloaded stream",
				errors.New("downloaded stream is not a regular file"),
			)
		}
	}

	if err := ctx.Err(); err != nil {
		return fail("processing", err)
	}

	// Stage the processed result separately from the user's destination.
	stagedOutput := filepath.Join(
		workDir,
		"output."+normalizeContainer(plan.OutputContainer),
	)

	if plan.NeedsMerge {
		err = e.processor.Merge(
			ctx,
			inputs[0],
			inputs[1],
			stagedOutput,
		)
	} else {
		err = e.processor.Remux(
			ctx,
			inputs[0],
			stagedOutput,
		)
	}

	if err != nil {
		return fail("processing", err)
	}

	if err := ctx.Err(); err != nil {
		return fail("processing", err)
	}

	info, err := os.Stat(stagedOutput)
	if err != nil {
		return fail("inspect processed output", err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fail(
			"inspect processed output",
			errors.New("processor produced no usable output"),
		)
	}

	if err := os.Rename(stagedOutput, output); err != nil {
		return fail("commit output", err)
	}

	result := &ExecutionResult{
		Path:      output,
		SizeBytes: info.Size(),
	}

	if err := os.RemoveAll(workDir); err != nil {
		result.WorkDir = workDir
		result.CleanupError = err
	}

	return result, nil
}

// ExecuteAudio downloads an audio plan and converts it according to its OutputSpec.
//
// The output extension must match the plan's OutputSpec.Extension.
// An existing destination is replaced only after its replacement is ready.
// Failure or cancellation preserves any existing destination and retains intermediate files.
func (e *Executor) ExecuteAudio(
	ctx context.Context,
	plan *AudioPlan,
	destination string,
	options ExecuteOptions,
) (*ExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if e == nil || e.downloader == nil {
		return nil, errors.New("goyt: create Executor with NewExecutor first")
	}

	if err := validateAudioExecutionPlan(plan, destination); err != nil {
		return nil, err
	}

	if options.Download.MaxRetries < 0 || options.Download.RetryDelay < 0 {
		return nil, errors.New("goyt: invalid retry options")
	}

	output, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}

	if info, err := os.Stat(output); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("goyt: destination must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	audioConverter, ok := e.processor.(interface {
		ConvertAudio(context.Context, string, string, AudioOutputSpec, bool) error
	})
	if !ok || e.processor == nil {
		return nil, errors.New("goyt: this plan requires an audio processor with audio conversion support")
	}

	if !plan.OutputSpec.Copy && plan.OutputSpec.Encoder != "" {
		if encoderChecker, ok := e.processor.(interface {
			HasEncoder(context.Context, string) (bool, error)
		}); ok {
			has, err := encoderChecker.HasEncoder(ctx, plan.OutputSpec.Encoder)
			if err == nil && !has {
				return nil, fmt.Errorf("goyt: required FFmpeg audio encoder %q is not available", plan.OutputSpec.Encoder)
			}
		}
	}

	workDir, err := os.MkdirTemp(filepath.Dir(output), ".goyt-work-*")
	if err != nil {
		return nil, err
	}

	fail := func(stage string, err error) (*ExecutionResult, error) {
		return nil, &ExecutionError{
			Stage:   stage,
			WorkDir: workDir,
			Err:     err,
		}
	}

	if err := ctx.Err(); err != nil {
		return fail("download", err)
	}

	streamInput := filepath.Join(workDir, "stream-0.media")

	downloadOptions := options.Download
	origProgress := downloadOptions.OnProgress
	downloadOptions.OnProgress = func(progress Progress) {
		if origProgress != nil {
			origProgress(progress)
		}
		if options.OnProgress != nil {
			options.OnProgress(ExecutionProgress{
				StreamIndex: 0,
				StreamCount: 1,
				FormatID:    plan.Stream.ID,
				Download:    progress,
			})
		}
	}

	_, err = e.downloader.Download(ctx, plan.Stream.Resource, streamInput, downloadOptions)
	if err != nil {
		return fail("download audio stream", err)
	}

	info, err := os.Stat(streamInput)
	if err != nil {
		return fail("inspect downloaded stream", err)
	}
	if !info.Mode().IsRegular() {
		return fail("inspect downloaded stream", errors.New("downloaded stream is not a regular file"))
	}

	if err := ctx.Err(); err != nil {
		return fail("processing", err)
	}

	stagedOutput := filepath.Join(workDir, "output"+plan.OutputSpec.Extension)

	if err := audioConverter.ConvertAudio(ctx, streamInput, stagedOutput, plan.OutputSpec, false); err != nil {
		return fail("processing audio", err)
	}

	if err := ctx.Err(); err != nil {
		return fail("processing", err)
	}

	stagedInfo, err := os.Stat(stagedOutput)
	if err != nil {
		return fail("inspect processed output", err)
	}
	if !stagedInfo.Mode().IsRegular() || stagedInfo.Size() == 0 {
		return fail("inspect processed output", errors.New("processor produced no usable output"))
	}

	if err := os.Rename(stagedOutput, output); err != nil {
		return fail("commit output", err)
	}

	result := &ExecutionResult{
		Path:      output,
		SizeBytes: stagedInfo.Size(),
	}

	if err := os.RemoveAll(workDir); err != nil {
		result.WorkDir = workDir
		result.CleanupError = err
	}

	return result, nil
}

func executionDownloadOptions(
	options ExecuteOptions,
	plan *DownloadPlan,
	index int,
) DownloadOptions {
	download := options.Download
	original := download.OnProgress

	download.OnProgress = func(progress Progress) {
		if original != nil {
			original(progress)
		}

		if options.OnProgress != nil {
			options.OnProgress(ExecutionProgress{
				StreamIndex: index,
				StreamCount: len(plan.Streams),
				FormatID:    plan.Streams[index].ID,
				Download:    progress,
			})
		}
	}

	return download
}

func validateExecutionPlan(
	plan *DownloadPlan,
	destination string,
) error {
	if plan == nil {
		return errors.New("goyt: download plan is nil")
	}

	if destination == "" {
		return errors.New("goyt: destination is empty")
	}

	container := normalizeContainer(plan.OutputContainer)
	switch container {
	case "mp4", "webm", "mkv":
	default:
		return errors.New("goyt: unsupported output container")
	}

	extension := strings.TrimPrefix(
		strings.ToLower(filepath.Ext(destination)),
		".",
	)
	if extension != container {
		return errors.New(
			"goyt: destination extension does not match planned container",
		)
	}

	switch len(plan.Streams) {
	case 1:
		if plan.NeedsMerge {
			return errors.New("goyt: merge requires two streams")
		}

		stream := plan.Streams[0]

		if !compatible(container, stream.VideoCodec, stream.AudioCodec) {
			return errors.New("goyt: incompatible codecs in plan")
		}

		expectedRemux := normalizeContainer(stream.Container) != container
		if plan.NeedsRemux != expectedRemux {
			return errors.New("goyt: inconsistent remux flag")
		}

	case 2:
		if !plan.NeedsMerge || plan.NeedsRemux {
			return errors.New("goyt: inconsistent merge plan")
		}

		video := plan.Streams[0]
		audio := plan.Streams[1]

		if normalizeCodec(video.AudioCodec) != "none" ||
			normalizeCodec(audio.VideoCodec) != "none" ||
			!compatible(container, video.VideoCodec, audio.AudioCodec) {
			return errors.New(
				"goyt: merge plan must contain video-only then audio-only",
			)
		}

	default:
		return errors.New("goyt: plan must contain one or two streams")
	}

	for _, stream := range plan.Streams {
		if stream.Protocol != ProtocolHTTP {
			return fmt.Errorf(
				"goyt: executor does not support protocol %q yet",
				stream.Protocol,
			)
		}

		if !usableResource(stream) {
			return errors.New("goyt: invalid stream resource")
		}
	}

	return nil
}

func validateAudioExecutionPlan(plan *AudioPlan, destination string) error {
	if plan == nil {
		return errors.New("goyt: audio plan is nil")
	}

	if destination == "" {
		return errors.New("goyt: destination is empty")
	}

	if plan.OutputSpec.Extension == "" {
		return errors.New("goyt: audio output specification missing extension")
	}

	if !strings.EqualFold(filepath.Ext(destination), plan.OutputSpec.Extension) {
		return fmt.Errorf("goyt: destination extension must be %s", plan.OutputSpec.Extension)
	}

	if plan.Stream.Protocol != ProtocolHTTP {
		return fmt.Errorf(
			"goyt: executor does not support protocol %q yet",
			plan.Stream.Protocol,
		)
	}

	if !usableResource(plan.Stream) {
		return errors.New("goyt: invalid stream resource")
	}

	if normalizeCodec(plan.Stream.VideoCodec) != "none" {
		return errors.New("goyt: audio plan must not contain a video stream")
	}

	if !knownAudio(plan.Stream.AudioCodec) {
		return errors.New("goyt: unsupported audio codec in plan")
	}

	return nil
}
