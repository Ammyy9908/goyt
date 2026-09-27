package goyt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutorCancellationBeforeCommitPreservesDestination(t *testing.T) {
	server := executorHTTPServer()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	output := filepath.Join(t.TempDir(), "output.mkv")
	if err := os.WriteFile(output, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}

	processor := &executorProcessor{
		remux: func(
			_ context.Context,
			_ string,
			staged string,
		) error {
			if err := os.WriteFile(
				staged, []byte("replacement"), 0600,
			); err != nil {
				return err
			}

			// Simulate cancellation just as processing finishes.
			// The executor must check context even if the processor
			// returns success.
			cancel()
			return nil
		},
	}

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		processor,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := executor.Execute(
		ctx,
		&DownloadPlan{
			OutputContainer: "mkv",
			NeedsRemux:      true,
			Streams: []Format{
				executorFormat(
					server.URL, "/combined", "h264", "aac",
				),
			},
		},
		output,
		ExecuteOptions{},
	)

	if result != nil {
		t.Fatal("canceled execution returned a successful result")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v; want context.Canceled", err)
	}

	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || executionErr.WorkDir == "" {
		t.Fatalf("expected retained work directory: %v", err)
	}

	executorAssertFile(t, output, "existing")
	executorAssertFile(
		t,
		filepath.Join(executionErr.WorkDir, "stream-0.media"),
		"combined",
	)
	executorAssertFile(
		t,
		filepath.Join(executionErr.WorkDir, "output.mkv"),
		"replacement",
	)
}

func TestExecutorRejectsUnusableProcessedOutput(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "directory"} {
		t.Run(mode, func(t *testing.T) {
			server := executorHTTPServer()
			defer server.Close()

			output := filepath.Join(t.TempDir(), "output.mkv")
			if err := os.WriteFile(
				output, []byte("existing"), 0600,
			); err != nil {
				t.Fatal(err)
			}

			processor := &executorProcessor{
				remux: func(
					_ context.Context,
					_ string,
					staged string,
				) error {
					switch mode {
					case "empty":
						return os.WriteFile(staged, nil, 0600)
					case "directory":
						return os.Mkdir(staged, 0700)
					default:
						// Reports success without creating output.
						return nil
					}
				},
			}

			executor, err := NewExecutor(
				NewDownloader(server.Client()),
				processor,
			)
			if err != nil {
				t.Fatal(err)
			}

			result, err := executor.Execute(
				context.Background(),
				&DownloadPlan{
					OutputContainer: "mkv",
					NeedsRemux:      true,
					Streams: []Format{
						executorFormat(
							server.URL,
							"/combined",
							"h264",
							"aac",
						),
					},
				},
				output,
				ExecuteOptions{},
			)

			if err == nil || result != nil {
				t.Fatal("unusable output must fail execution")
			}

			var executionErr *ExecutionError
			if !errors.As(err, &executionErr) ||
				executionErr.Stage != "inspect processed output" ||
				executionErr.WorkDir == "" {
				t.Fatalf("unexpected error: %v", err)
			}

			executorAssertFile(t, output, "existing")
			executorAssertFile(
				t,
				filepath.Join(executionErr.WorkDir, "stream-0.media"),
				"combined",
			)
		})
	}
}

func TestExecutorSecondDownloadFailurePreservesFirstInput(t *testing.T) {
	server := executorHTTPServer()
	defer server.Close()

	output := filepath.Join(t.TempDir(), "output.mp4")
	if err := os.WriteFile(output, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}

	processingCalled := false
	processor := &executorProcessor{
		merge: func(
			context.Context,
			string,
			string,
			string,
		) error {
			processingCalled = true
			return errors.New("processor must not run")
		},
	}

	executor, err := NewExecutor(
		NewDownloader(server.Client()),
		processor,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := executor.Execute(
		context.Background(),
		&DownloadPlan{
			OutputContainer: "mp4",
			NeedsMerge:      true,
			Streams: []Format{
				executorFormat(
					server.URL, "/video", "h264", "none",
				),
				// The local server returns HTTP 404 for this path.
				executorFormat(
					server.URL, "/missing-audio", "none", "aac",
				),
			},
		},
		output,
		ExecuteOptions{},
	)

	if err == nil || result != nil {
		t.Fatal("expected second-stream download failure")
	}
	if processingCalled {
		t.Fatal("processing ran with a missing input")
	}

	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) ||
		executionErr.Stage != "download stream 1" ||
		executionErr.WorkDir == "" {
		t.Fatalf("unexpected error: %v", err)
	}

	executorAssertFile(t, output, "existing")
	executorAssertFile(
		t,
		filepath.Join(executionErr.WorkDir, "stream-0.media"),
		"video",
	)
}
