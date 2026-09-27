package goyt_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/ammyy9908/goyt"
)

type fixtureExtractor struct {
	name string
	run  func(context.Context, *url.URL) (*goyt.Media, error)
}

func (f *fixtureExtractor) Name() string {
	return f.name
}

func (f *fixtureExtractor) Match(u *url.URL) bool {
	return u.Hostname() == "fixture.test"
}

func (f *fixtureExtractor) Extract(
	ctx context.Context,
	u *url.URL,
) (*goyt.Media, error) {
	if f.run != nil {
		return f.run(ctx, u)
	}

	zero := int64(0)

	return &goyt.Media{
		ID:    "demo",
		Title: "Synthetic video",
		Formats: []goyt.Format{
			{
				ID:        "mp4",
				Protocol:  goyt.ProtocolHTTP,
				Container: "mp4",
				SizeBytes: &zero,
				Resource: goyt.Resource{
					URL: "https://fixture.test/media.mp4",
				},
			},
		},
	}, nil
}

func newClient(
	t *testing.T,
	adapters ...goyt.Extractor,
) *goyt.Client {
	t.Helper()

	client, err := goyt.New(adapters...)
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func assertKind(
	t *testing.T,
	err error,
	kind goyt.ErrorKind,
) {
	t.Helper()

	var detail *goyt.ExtractionError

	if !errors.As(err, &detail) || detail.Kind != kind {
		t.Fatalf("got %v, want error kind %s", err, kind)
	}
}

func TestExtractFixture(t *testing.T) {
	client := newClient(t, &fixtureExtractor{name: "fixture"})
	input := "https://fixture.test/watch/demo"

	media, err := client.Extract(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if media.ID != "demo" ||
		media.Title != "Synthetic video" ||
		media.SourceURL != input {
		t.Fatalf("unexpected metadata: %+v", media)
	}

	if len(media.Formats) != 1 {
		t.Fatalf("got %d formats, want 1", len(media.Formats))
	}

	format := media.Formats[0]

	if media.Duration != nil || format.Height != nil {
		t.Fatal("unknown metadata should remain nil")
	}

	if format.SizeBytes == nil || *format.SizeBytes != 0 {
		t.Fatal("known zero must be distinguishable from unknown")
	}
}

func TestInvalidURLs(t *testing.T) {
	client := newClient(t)

	inputs := []string{
		"",
		"/relative",
		"ftp://fixture.test/a",
		"https://",
		"https://user:secret@fixture.test/a",
		"%",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			_, err := client.Extract(context.Background(), input)
			assertKind(t, err, goyt.ErrorInvalidURL)
		})
	}
}

func TestUnsupportedURL(t *testing.T) {
	client := newClient(t, &fixtureExtractor{name: "fixture"})

	_, err := client.Extract(
		context.Background(),
		"https://unknown.test/a",
	)
	assertKind(t, err, goyt.ErrorUnsupportedURL)

	var empty goyt.Client

	_, err = empty.Extract(
		context.Background(),
		"https://fixture.test/a",
	)
	assertKind(t, err, goyt.ErrorUnsupportedURL)
}

func TestInvalidRegistration(t *testing.T) {
	var typedNil *fixtureExtractor

	cases := [][]goyt.Extractor{
		{nil},
		{typedNil},
		{&fixtureExtractor{name: " "}},
		{
			&fixtureExtractor{name: "same"},
			&fixtureExtractor{name: "same"},
		},
	}

	for i, adapters := range cases {
		if _, err := goyt.New(adapters...); err == nil {
			t.Fatalf("case %d accepted invalid registration", i)
		}
	}
}

func TestFirstMatchPreservesError(t *testing.T) {
	cause := errors.New("fixture failed")

	first := &fixtureExtractor{
		name: "first",
		run: func(context.Context, *url.URL) (*goyt.Media, error) {
			return nil, cause
		},
	}

	second := &fixtureExtractor{
		name: "second",
		run: func(context.Context, *url.URL) (*goyt.Media, error) {
			t.Fatal("must not fall through after extraction fails")
			return nil, nil
		},
	}

	client := newClient(t, first, second)

	_, err := client.Extract(
		context.Background(),
		"https://fixture.test/a",
	)

	assertKind(t, err, goyt.ErrorExtraction)

	if !errors.Is(err, cause) {
		t.Fatal("underlying error was lost")
	}

	var detail *goyt.ExtractionError

	if !errors.As(err, &detail) || detail.Extractor != "first" {
		t.Fatal("extractor name was lost")
	}
}

func TestInvalidResults(t *testing.T) {
	results := []*goyt.Media{
		nil,
		{},
		{ID: " "},
	}

	for _, result := range results {
		adapter := &fixtureExtractor{
			name: "invalid",
			run: func(context.Context, *url.URL) (*goyt.Media, error) {
				return result, nil
			},
		}

		client := newClient(t, adapter)

		_, err := client.Extract(
			context.Background(),
			"https://fixture.test/a",
		)

		assertKind(t, err, goyt.ErrorInvalidResult)
	}
}

func TestAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	adapter := &fixtureExtractor{
		name: "unused",
		run: func(context.Context, *url.URL) (*goyt.Media, error) {
			t.Fatal("canceled request reached extractor")
			return nil, nil
		},
	}

	client := newClient(t, adapter)
	_, err := client.Extract(ctx, "https://fixture.test/a")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestCancellationDuringExtraction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	done := make(chan error, 1)

	adapter := &fixtureExtractor{
		name: "blocking",
		run: func(ctx context.Context, _ *url.URL) (*goyt.Media, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	client := newClient(t, adapter)

	go func() {
		_, err := client.Extract(ctx, "https://fixture.test/a")
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("extractor did not start")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("extraction did not stop after cancellation")
	}
}

func TestExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(-time.Second),
	)
	defer cancel()

	client := newClient(t)
	_, err := client.Extract(ctx, "https://fixture.test/a")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
}
