package goyt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type executorProcessor struct {
	merge        func(context.Context, string, string, string) error
	remux        func(context.Context, string, string) error
	convertAudio func(context.Context, string, string, AudioOutputSpec, bool) error
	hasEncoder   func(context.Context, string) (bool, error)
}

func (p *executorProcessor) Merge(
	ctx context.Context,
	video string,
	audio string,
	output string,
) error {
	return p.merge(ctx, video, audio, output)
}

func (p *executorProcessor) Remux(
	ctx context.Context,
	input string,
	output string,
) error {
	return p.remux(ctx, input, output)
}

func (p *executorProcessor) ConvertAudio(
	ctx context.Context,
	input string,
	output string,
	spec AudioOutputSpec,
	isHLS bool,
) error {
	if p.convertAudio != nil {
		return p.convertAudio(ctx, input, output, spec, isHLS)
	}
	return nil
}

func (p *executorProcessor) HasEncoder(
	ctx context.Context,
	encoder string,
) (bool, error) {
	if p.hasEncoder != nil {
		return p.hasEncoder(ctx, encoder)
	}
	return true, nil
}

func executorHTTPServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/video":
				w.Write([]byte("video"))
			case "/audio":
				w.Write([]byte("audio"))
			case "/combined":
				w.Write([]byte("combined"))
			default:
				http.NotFound(w, r)
			}
		},
	))
}

func executorFormat(
	baseURL string,
	path string,
	videoCodec string,
	audioCodec string,
) Format {
	return Format{
		ID:         path,
		Protocol:   ProtocolHTTP,
		Container:  "mp4",
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Resource: Resource{
			URL: baseURL + path,
		},
	}
}

func executorAssertFile(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != want {
		t.Fatalf("got %q, want %q", data, want)
	}
}

func TestExecutorDirect(t *testing.T) {
	server := executorHTTPServer()
	defer server.Close()

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	plan := &DownloadPlan{
		OutputContainer: "mp4",
		Streams: []Format{
			executorFormat(server.URL, "/combined", "h264", "aac"),
		},
	}

	var last ExecutionProgress
	output := filepath.Join(t.TempDir(), "output.mp4")

	result, err := executor.Execute(
		context.Background(),
		plan,
		output,
		ExecuteOptions{
			OnProgress: func(p ExecutionProgress) {
				last = p
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	executorAssertFile(t, result.Path, "combined")

	if last.StreamCount != 1 ||
		last.StreamIndex != 0 ||
		last.Download.DownloadedBytes != 8 {
		t.Fatalf("unexpected progress: %+v", last)
	}
}

func TestExecutorMerge(t *testing.T) {
	server := executorHTTPServer()
	defer server.Close()

	directory := t.TempDir()
	output := filepath.Join(directory, "output.mp4")

	processor := &executorProcessor{
		merge: func(
			ctx context.Context,
			video string,
			audio string,
			output string,
		) error {
			executorAssertFile(t, video, "video")
			executorAssertFile(t, audio, "audio")
			return os.WriteFile(output, []byte("merged"), 0600)
		},
	}

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		processor,
	)
	if err != nil {
		t.Fatal(err)
	}

	plan := &DownloadPlan{
		OutputContainer: "mp4",
		NeedsMerge:      true,
		Streams: []Format{
			executorFormat(server.URL, "/video", "h264", "none"),
			executorFormat(server.URL, "/audio", "none", "aac"),
		},
	}

	result, err := executor.Execute(
		context.Background(),
		plan,
		output,
		ExecuteOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	executorAssertFile(t, result.Path, "merged")

	if result.CleanupError != nil {
		t.Fatal(result.CleanupError)
	}

	leftovers, err := filepath.Glob(
		filepath.Join(directory, ".goyt-work-*"),
	)
	if err != nil || len(leftovers) != 0 {
		t.Fatal("successful job left intermediate files")
	}
}

func TestExecutorRemux(t *testing.T) {
	server := executorHTTPServer()
	defer server.Close()

	processor := &executorProcessor{
		remux: func(
			ctx context.Context,
			input string,
			output string,
		) error {
			executorAssertFile(t, input, "combined")
			return os.WriteFile(output, []byte("remuxed"), 0600)
		},
	}

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		processor,
	)
	if err != nil {
		t.Fatal(err)
	}

	plan := &DownloadPlan{
		OutputContainer: "mkv",
		NeedsRemux:      true,
		Streams: []Format{
			executorFormat(server.URL, "/combined", "h264", "aac"),
		},
	}

	output := filepath.Join(t.TempDir(), "output.mkv")

	_, err = executor.Execute(
		context.Background(),
		plan,
		output,
		ExecuteOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	executorAssertFile(t, output, "remuxed")
}

func TestExecutorFailureRetainsInputs(t *testing.T) {
	server := executorHTTPServer()
	defer server.Close()

	cause := errors.New("processing failed")
	processor := &executorProcessor{
		remux: func(
			context.Context,
			string,
			string,
		) error {
			return cause
		},
	}

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		processor,
	)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	output := filepath.Join(directory, "output.mkv")

	if err := os.WriteFile(output, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}

	plan := &DownloadPlan{
		OutputContainer: "mkv",
		NeedsRemux:      true,
		Streams: []Format{
			executorFormat(server.URL, "/combined", "h264", "aac"),
		},
	}

	_, err = executor.Execute(
		context.Background(),
		plan,
		output,
		ExecuteOptions{},
	)

	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) ||
		!errors.Is(err, cause) ||
		executionErr.WorkDir == "" {
		t.Fatalf("unexpected error: %v", err)
	}

	executorAssertFile(t, output, "existing")
	executorAssertFile(
		t,
		filepath.Join(executionErr.WorkDir, "stream-0.media"),
		"combined",
	)
}

func TestExecutorRejectsUnsupportedProtocol(t *testing.T) {
	executor, err := NewExecutor(NewDownloader(nil), nil)
	if err != nil {
		t.Fatal(err)
	}

	stream := executorFormat(
		"https://fixture.test",
		"/combined",
		"h264",
		"aac",
	)
	stream.Protocol = ProtocolHLS

	_, err = executor.Execute(
		context.Background(),
		&DownloadPlan{
			OutputContainer: "mp4",
			Streams:         []Format{stream},
		},
		filepath.Join(t.TempDir(), "output.mp4"),
		ExecuteOptions{},
	)

	if err == nil {
		t.Fatal("HLS should be rejected before downloading")
	}
}

func TestExecutorCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	executor, err := NewExecutor(NewDownloader(nil), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = executor.Execute(
		ctx,
		nil,
		"",
		ExecuteOptions{},
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
