package goyt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrResourceExpired = errors.New("goyt: resource URL has expired")

// Progress describes the current transfer.
//
// TotalBytes is -1 when unknown. DownloadedBytes includes any
// bytes retained from a previous attempt.
type Progress struct {
	DownloadedBytes int64
	TotalBytes      int64
}

// DownloadOptions controls one direct HTTP download.
type DownloadOptions struct {
	// Resume enables resuming a matching partial download.
	Resume bool

	// MaxRetries is the number of additional attempts after the first.
	MaxRetries int

	// RetryDelay is the initial retry delay. Zero means one second.
	RetryDelay time.Duration

	// StallTimeout limits network inactivity during a media transfer.
	// Zero disables inactivity detection.
	StallTimeout time.Duration

	// OnProgress runs synchronously. Keep it fast.
	// TotalBytes may be unknown. Progress can reset if a transfer restarts.
	OnProgress func(Progress)
}

// stallWatcher tracks request-scoped network inactivity.
type stallWatcher struct {
	parentCtx context.Context
	cancel    context.CancelFunc
	timeout   time.Duration
	timer     *time.Timer
	mu        sync.Mutex
	stalled   bool
	stopped   bool
}

func newStallWatcher(
	parentCtx context.Context,
	timeout time.Duration,
) (context.Context, *stallWatcher) {
	if timeout <= 0 {
		return parentCtx, nil
	}

	reqCtx, cancel := context.WithCancel(parentCtx)
	w := &stallWatcher{
		parentCtx: parentCtx,
		cancel:    cancel,
		timeout:   timeout,
	}

	w.timer = time.AfterFunc(timeout, w.onTimeout)
	return reqCtx, w
}

func (w *stallWatcher) onTimeout() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}

	if w.parentCtx.Err() != nil {
		return
	}

	w.stalled = true
	w.cancel()
}

func (w *stallWatcher) Reset() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped || w.stalled {
		return
	}

	w.timer.Reset(w.timeout)
}

func (w *stallWatcher) IsStalled() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.parentCtx.Err() != nil {
		return false
	}

	return w.stalled
}

func (w *stallWatcher) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
}

// DownloadResult describes a completed file.
type DownloadResult struct {
	Path      string
	SizeBytes int64
}

// Downloader transfers direct HTTP(S) resources.
//
// Different destinations may be downloaded concurrently.
// Do not download to the same destination concurrently.
type Downloader struct {
	client *http.Client
}

// NewDownloader uses the supplied HTTP client or http.DefaultClient.
//
// Cross-origin redirects drop request headers before continuing.
// Redirects from HTTPS to HTTP are rejected.
func NewDownloader(client *http.Client) *Downloader {
	if client == nil {
		client = http.DefaultClient
	}

	cloned := *client
	previousRedirect := client.CheckRedirect

	cloned.CheckRedirect = func(
		req *http.Request,
		via []*http.Request,
	) error {
		if len(via) >= 10 {
			return errors.New("goyt: too many redirects")
		}

		previous := via[len(via)-1]

		if previous.URL.Scheme == "https" &&
			req.URL.Scheme == "http" {
			return errors.New("goyt: insecure redirect")
		}

		if previousRedirect != nil {
			if err := previousRedirect(req, via); err != nil {
				return err
			}
		}

		if origin(req.URL) != origin(previous.URL) {
			req.Header = make(http.Header)
		}

		// Byte ranges must refer to the original representation.
		req.Header.Set("Accept-Encoding", "identity")
		return nil
	}

	return &Downloader{client: &cloned}
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	return strings.ToLower(u.Scheme) + "://" +
		strings.ToLower(u.Hostname()) + ":" + port
}

// Download streams a resource to destination.
//
// It creates destination.part and destination.part.json.
// A successful transfer renames the partial file to destination.
// An existing destination is replaced on success.
//
// Interrupted partial downloads remain available for a later resume.
// Parent directories must already exist.
//
// Cancellation is cooperative through the HTTP request context.
func (d *Downloader) Download(
	ctx context.Context,
	resource Resource,
	destination string,
	options DownloadOptions,
) (*DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if d == nil || d.client == nil {
		return nil, errors.New(
			"goyt: create Downloader with NewDownloader first",
		)
	}

	if destination == "" {
		return nil, errors.New("goyt: destination is empty")
	}

	if options.MaxRetries < 0 || options.RetryDelay < 0 || options.StallTimeout < 0 {
		return nil, errors.New("goyt: invalid retry options")
	}

	u, err := url.Parse(resource.URL)
	if err != nil ||
		u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil {
		return nil, errors.New("goyt: invalid resource URL")
	}

	// Copy headers so retries use the same caller-supplied values.
	resource.Headers = resource.Headers.Clone()

	delay := options.RetryDelay
	if delay == 0 {
		delay = time.Second
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if resource.ExpiresAt != nil &&
			!resource.ExpiresAt.After(time.Now()) {
			return nil, ErrResourceExpired
		}
		result, retry, err := d.attempt(
			ctx,
			resource,
			destination,
			options,
		)

		if err == nil {
			return result, nil
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if !retry || attempt >= options.MaxRetries {
			return nil, err
		}

		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}

		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

type resumeState struct {
	Key   string
	ETag  string
	Total int64
}

// Store a fingerprint instead of saving URLs or headers containing secrets.
func resourceKey(resource Resource) string {
	data, _ := json.Marshal(struct {
		URL     string
		Headers http.Header
	}{
		URL:     resource.URL,
		Headers: resource.Headers,
	})

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func strongETag(value string) bool {
	return len(value) >= 2 &&
		strings.HasPrefix(value, `"`) &&
		strings.HasSuffix(value, `"`)
}

func loadResume(
	partPath string,
	statePath string,
	key string,
) (resumeState, int64) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return resumeState{}, 0
	}

	var state resumeState
	if json.Unmarshal(data, &state) != nil ||
		state.Key != key ||
		!strongETag(state.ETag) {
		return resumeState{}, 0
	}

	info, err := os.Stat(partPath)
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Size() <= 0 ||
		(state.Total >= 0 && info.Size() >= state.Total) {
		return resumeState{}, 0
	}

	return state, info.Size()
}

func saveResume(path string, state resumeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func (d *Downloader) attempt(
	ctx context.Context,
	resource Resource,
	destination string,
	options DownloadOptions,
) (*DownloadResult, bool, error) {
	partPath := destination + ".part"
	statePath := partPath + ".json"
	key := resourceKey(resource)

	var previous resumeState
	var offset int64

	if options.Resume {
		previous, offset = loadResume(partPath, statePath, key)
	}

	reqCtx, watcher := newStallWatcher(ctx, options.StallTimeout)
	if watcher != nil {
		defer watcher.Stop()
	}

	req, err := http.NewRequestWithContext(
		reqCtx,
		http.MethodGet,
		resource.URL,
		nil,
	)
	if err != nil {
		return nil, false, err
	}

	req.Header = resource.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}

	// These headers are controlled by the downloader.
	req.Header.Del("Range")
	req.Header.Del("If-Range")
	req.Header.Set("Accept-Encoding", "identity")

	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		req.Header.Set("If-Range", previous.ETag)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if watcher.IsStalled() {
			return nil, true, fmt.Errorf("%w: %v", ErrDownloadStalled, err)
		}
		return nil, true, fmt.Errorf("goyt: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf(
			"goyt: retryable HTTP status %d",
			resp.StatusCode,
		)
	}

	if offset > 0 &&
		resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// Invalidate the old checkpoint. A retry will request the full file.
		if err := os.Remove(statePath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}

		return nil, true, errors.New("goyt: server rejected resume range")
	}

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusPartialContent {
		return nil, false, fmt.Errorf(
			"goyt: unexpected HTTP status %d",
			resp.StatusCode,
		)
	}

	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" &&
		!strings.EqualFold(encoding, "identity") {
		return nil, false, errors.New(
			"goyt: encoded response cannot be downloaded as identity bytes",
		)
	}

	total := resp.ContentLength

	switch resp.StatusCode {
	case http.StatusOK:
		// The server ignored Range or If-Range detected a changed file.
		// Restart instead of appending a full response to a partial file.
		offset = 0

	case http.StatusPartialContent:
		total, err = validateRange(resp, offset, previous)
		if err != nil {
			return nil, false, err
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}

	file, err := os.OpenFile(partPath, flags, 0600)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	state := resumeState{
		Key:   key,
		ETag:  resp.Header.Get("ETag"),
		Total: total,
	}

	// A weak or absent ETag is saved but will not permit resumption.
	if err := saveResume(statePath, state); err != nil {
		return nil, false, err
	}

	report := func(written int64) {
		if options.OnProgress != nil {
			options.OnProgress(Progress{
				DownloadedBytes: offset + written,
				TotalBytes:      total,
			})
		}
	}

	report(0)

	written, retry, err := copyDownload(ctx, file, resp.Body, watcher, report)
	if err != nil {
		return nil, retry, err
	}

	size := offset + written
	if total >= 0 && size != total {
		return nil, true, fmt.Errorf(
			"goyt: incomplete transfer: got %d bytes, expected %d",
			size,
			total,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	if err := file.Sync(); err != nil {
		return nil, false, err
	}

	if err := file.Close(); err != nil {
		return nil, false, err
	}

	if err := os.Rename(partPath, destination); err != nil {
		return nil, false, err
	}

	// The completed file is already committed; checkpoint cleanup is best effort.
	_ = os.Remove(statePath)

	return &DownloadResult{
		Path:      filepath.Clean(destination),
		SizeBytes: size,
	}, false, nil
}

func validateRange(
	resp *http.Response,
	offset int64,
	previous resumeState,
) (int64, error) {
	if offset == 0 {
		return 0, errors.New("goyt: unexpected partial response")
	}

	value := resp.Header.Get("Content-Range")
	if !strings.HasPrefix(value, "bytes ") {
		return 0, errors.New("goyt: invalid Content-Range")
	}

	bounds, totalText, ok := strings.Cut(
		strings.TrimPrefix(value, "bytes "),
		"/",
	)
	if !ok {
		return 0, errors.New("goyt: invalid Content-Range")
	}

	startText, endText, ok := strings.Cut(bounds, "-")
	if !ok {
		return 0, errors.New("goyt: invalid Content-Range")
	}

	start, startErr := strconv.ParseInt(startText, 10, 64)
	end, endErr := strconv.ParseInt(endText, 10, 64)
	total, totalErr := strconv.ParseInt(totalText, 10, 64)

	if startErr != nil || endErr != nil || totalErr != nil ||
		start != offset || end < start || total <= end ||
		end != total-1 {
		return 0, errors.New("goyt: inconsistent resume range")
	}

	if resp.Header.Get("ETag") != previous.ETag ||
		(previous.Total >= 0 && previous.Total != total) {
		return 0, errors.New("goyt: remote resource changed during resume")
	}

	if resp.ContentLength >= 0 &&
		resp.ContentLength != end-start+1 {
		return 0, errors.New("goyt: inconsistent partial Content-Length")
	}

	return total, nil
}

// The boolean reports whether the failure came from reading the response.
// Disk write failures are not retried.
func copyDownload(
	ctx context.Context,
	dst io.Writer,
	src io.Reader,
	watcher *stallWatcher,
	report func(int64),
) (int64, bool, error) {
	buffer := make([]byte, 32*1024)
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, false, err
		}

		watcher.Reset()

		n, readErr := src.Read(buffer)
		if n > 0 {
			watcher.Reset()

			written, writeErr := dst.Write(buffer[:n])
			total += int64(written)
			report(total)

			if writeErr != nil {
				return total, false, writeErr
			}
			if written != n {
				return total, false, io.ErrShortWrite
			}
		}

		if readErr == io.EOF {
			return total, false, nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return total, false, ctx.Err()
			}
			if watcher.IsStalled() {
				return total, true, fmt.Errorf("%w: %v", ErrDownloadStalled, readErr)
			}
			return total, true, readErr
		}
	}
}
