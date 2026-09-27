# goyt

`goyt` is a native Go media extraction and download library with command-line tools for YouTube media retrieval, format planning, resilient downloads, and FFmpeg-backed remuxing and verification.

## License

MIT. See [LICENSE](LICENSE).

---

## Requirements

- **Go**: 1.22 or newer (specified in `go.mod`).
- **FFmpeg & ffprobe**: Required for stream merging, remuxing, metadata verification, and decode checks. Ensure `ffmpeg` and `ffprobe` are available on your system `PATH`.

---

## Build & Test

Build all canonical binaries:

```sh
make build
```

This generates binaries in `./bin/`:
- `bin/goyt` — Primary download CLI
- `bin/goyt-inspect` — YouTube format and diagnostic inspector
- `bin/goyt-hls` — Direct HLS playlist downloader

Run code formatting, static checks, and unit tests with race detection:

```sh
make check
```

Or run individual commands:

```sh
go fmt ./...
go vet ./...
go test -race ./...
```

---

## CLI Usage

### 1. `goyt` (Canonical Downloader)

Downloads a YouTube video using either direct HTTP streams or HLS transport:

```sh
# Download via direct HTTP streams (merges separate video and audio streams)
./bin/goyt -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -height 1080 -out video.mp4

# Download via HLS manifest with full decode verification
./bin/goyt -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -height 1080 -out video.mp4 -decode-check

# Display version
./bin/goyt -version
```

**Flags:**
- `-url`: YouTube video URL (required).
- `-transport`: Download transport protocol: `http` or `hls` (default `http`).
- `-height`: Maximum desired video height in pixels (default `1080`).
- `-out`: Destination file path, must have an `.mp4` extension (default `video.mp4`).
- `-decode-check`: Optionally decodes the entire output after verification to check for frame errors.
- `-version`: Print `goyt` version.

### 2. `goyt-inspect` (Diagnostic Inspector)

Inspects available YouTube formats, streaming endpoints (HLS/DASH/SABR), signature/N-parameter challenge requirements, and client profile responses:

```sh
# Inspect using default (all) client profiles:
./bin/goyt-inspect -url "https://www.youtube.com/watch?v=VIDEO_ID"

# Inspect specific client profile (web or visionos):
./bin/goyt-inspect -url "https://www.youtube.com/watch?v=VIDEO_ID" -client visionos
```

### 3. `goyt-hls` (Direct HLS Playlist Downloader)

Downloads arbitrary HLS master or media playlists directly from a URL:

```sh
./bin/goyt-hls -url "https://example.com/playlist/master.m3u8" -height 1080 -out output.mp4
```

---

## Supported Scope

- Selected public, non-live YouTube videos accessible without authentication.
- Explicit selection of direct HTTP or HLS downloading.
- H.264 video and AAC audio output in MP4 through the main YouTube CLI.
- Maximum-height selection from supported available formats; the requested
  height is a ceiling, not a guaranteed output resolution.
- Completed HLS playlists containing MPEG-TS or supported ID3-prefixed
  packed AAC segments, including supported separate audio renditions.
- Metadata checks for expected stream codecs and duration.
- Optional full audio/video decoding through the main YouTube CLI.

YouTube extraction depends on behavior that can change. Successful extraction
does not guarantee that every discovered media URL will accept downloads.


## Audio Selection

For HLS downloads, goyt prefers audio renditions whose names contain a
recognized original-audio marker. This works regardless of the language.

```sh
./bin/goyt \
  -url "https://www.youtube.com/watch?v=VIDEO_ID" \
  -transport hls \
  -height 1080 \
  -out video.mp4
```

Original-audio recognition is a metadata heuristic, not verified provenance.
If no original marker is found, goyt selects fallback audio and emits a
warning. Multiple renditions marked original also produce an ambiguity
warning.

The selected track may differ from the audio YouTube chooses in your browser.

To explicitly request a language:

```sh
./bin/goyt \
  -url "https://www.youtube.com/watch?v=VIDEO_ID" \
  -transport hls \
  -audio-language en \
  -height 1080 \
  -out video-english.mp4 \
  -decode-check
```

- `en` matches `en` and regional English tags such as `en-US`.
- `en-US` requires an exact language-tag match, ignoring case.
- If no supported matching external rendition exists, selection fails.
- Audio with missing language metadata cannot satisfy an explicit request.
- This flag selects existing audio; it does not translate audio.
- Language selection through this flag currently applies only to HLS.

Direct HTTP downloads do not yet have the same original-audio selection
guarantees. Multi-language videos may require the HLS path.

## Verification

The main downloader checks the output's expected video/audio codecs and
duration with ffprobe.

Use `-decode-check` to additionally decode the complete output with FFmpeg.
This checks decodability but does not establish spoken language, perceptual
quality, or lip-sync.

Verification occurs after the downloaded/processed output is committed.
If verification fails, the output remains available for inspection; an older
destination is not restored.

## Development Checks

```sh
go fmt ./...
go vet ./...
go test -race ./...
go build ./...
```

Run the local FFmpeg and executor integration tests:

```sh
go test -race -tags=integration \
  -run '^(TestFFmpegIntegration|TestExecutorIntegration)$' \
  -count=1 -v ./...
```

These integration tests require ffmpeg and ffprobe on PATH.

The optional public YouTube compatibility runner performs full downloads and
decode checks:

```sh
python3 scripts/check_youtube.py
```

Results are stored under `compat-results/`. These checks depend on network
conditions and YouTube's current responses. Manual playback checks are
separate from automated PASS results.

## Limitations

- No authenticated, members-only, DRM-protected, or live-video support.
- No JavaScript signature/N-challenge solver or PO-token provider.
- No DASH or SABR downloading.
- No automatic fallback between HTTP and HLS, or automatic refresh of expired
  media URLs.
- HLS support excludes encryption, fragmented MP4 initialization sections,
  byte-range segments, discontinuities, nested master playlists, and alternate
  video renditions.
- Subtitle references may be ignored for audio/video downloads; subtitle
  downloading and muxing are not implemented.
- Individual HTTP transfers can resume matching partial files when the server
  provides a strong ETag and valid byte-range responses. Otherwise, transfers
  restart.
- Executor merge/remux jobs and HLS jobs retain intermediate files after
  failure, but subsequent invocations do not automatically resume those jobs.
- Metadata and decode verification happen after the CLI's download/processing
  step commits the output. A verification failure retains that output; it does
  not restore a previously replaced destination.
- Full decoding checks decodability, not perceptual audio/video synchronization.
- There is no dedicated stalled-transfer timeout. Cancellation and configured
  context/client timeouts bound operations.

---

## Package Layout

| Package / Directory | Description |
| --- | --- |
| `github.com/ammyy9908/goyt` (root) | Core public API: `Downloader`, `Executor`, `Planner`, `HLSDownloader`, `FFmpeg`, `Verifier`, and data models. |
| `extractor/youtube` | YouTube extractor implementation, Innertube API client, visitor data extraction, and HLS manifest extraction. |
| `cmd/goyt` | Unified YouTube downloader CLI. |
| `cmd/goyt-inspect` | Diagnostic inspection CLI for YouTube streams and client profiles. |
| `cmd/goyt-hls` | Standalone HLS playlist download CLI for arbitrary streams. |
| `scripts/` | Automated compatibility testing and verification runners (`check_youtube.py`). |
| `testdata/` | Unit and integration test fixtures (including mock local HLS streams). |
