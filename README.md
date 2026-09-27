# goyt

`goyt` is a native Go media extraction and download library with a consolidated command-line tool for YouTube media retrieval, format planning, resilient downloads, stream inspection, and FFmpeg-backed remuxing and verification.

## License

MIT. See [LICENSE](LICENSE).

---

## Requirements

- **Go**: 1.22 or newer (specified in `go.mod`).
- **FFmpeg & ffprobe**: Required for stream merging, remuxing, metadata verification, and decode checks. Ensure `ffmpeg` and `ffprobe` are available on your system `PATH`.

---

## Build & Test

Build the consolidated binary:

```sh
make build
```

This generates the executable in `./bin/`:
- `bin/goyt` — Consolidated media download, inspection, and HLS CLI

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

`goyt` provides subcommands for downloading, inspecting YouTube streams, and downloading arbitrary HLS playlists, as well as root help and version flags.

### 1. `goyt download` (YouTube Downloader)

Downloads a YouTube video using either direct HTTP streams or HLS transport in video (MP4) or audio-only mode:

```sh
# Download video via direct HTTP streams (merges separate video and audio streams into MP4)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -height 1080 -out video.mp4

# Download video via HLS manifest with full decode verification
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -height 1080 -out video.mp4 -decode-check

# Download best available audio stream without re-encoding (e.g. Opus -> .opus or AAC -> .m4a)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -audio-only -audio-format best

# Download audio as MP3 with VBR quality 2 (~190 kbps)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -audio-only -audio-format mp3 -audio-quality 2 -out song.mp3

# Download audio as M4A (AAC) via HLS with explicit bitrate and decode check
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -audio-only -audio-format m4a -audio-bitrate 192k -out audio.m4a -decode-check

# Download lossless FLAC audio via HTTP
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -audio-only -audio-format flac -out audio.flac

# Download Opus audio via HTTP with explicit bitrate
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -audio-only -audio-format opus -audio-bitrate 160k -out audio.opus
```

**Flags:**
- `-url`: YouTube video URL (required).
- `-transport`: Download transport protocol: `http` or `hls` (default `http`).
- `-audio-only`: Download audio without video. Defaults output format to `best` and output filename to `audio.<resolved_ext>` when `-out` is omitted.
- `-audio-format`: Output format for audio-only mode (default `best`). Supported formats: `best`, `aac`, `alac`, `flac`, `m4a`, `mp3`, `opus`, `vorbis`, `wav`. Only valid with `-audio-only`.
- `-audio-quality`: MP3 VBR quality level, integer `0` (highest quality, ~245 kbps) to `9` (lowest quality, ~65 kbps), default `2` (~190 kbps). Lower values request higher quality. Only valid for `mp3` format with `-audio-only`. Mutually exclusive with `-audio-bitrate`.
- `-audio-bitrate`: Target audio bitrate for lossy encoders (e.g. `128k`, `192k`, `320k`). Supported for `aac`, `m4a`, `mp3`, `opus`, and `vorbis`. Disallowed for `best`, `alac`, `flac`, and `wav`. Mutually exclusive with `-audio-quality`.
- `-height`: Maximum desired video height in pixels (default `1080`). Disallowed when explicitly specified in `-audio-only` mode.
- `-audio-language`: Audio language tag, e.g. `en` or `en-US` (supports HLS and HTTP formats with language metadata).
- `-out`: Destination file path. In video mode, must have an `.mp4` extension (default `video.mp4`). In audio-only mode, extension must match the resolved format (default `audio.<ext>`).
- `-decode-check`: Optionally decodes the complete output after verification to check for stream errors.
- `-version`: Print `goyt` version.

**Legacy Shorthand:**
The legacy shorthand format without a subcommand is fully preserved:
```sh
./bin/goyt -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -audio-only -audio-format mp3 -out audio.mp3
```

### 2. `goyt inspect` (Diagnostic Inspector)

Inspects available YouTube formats, streaming endpoints (HLS/DASH/SABR), signature/N-parameter challenge requirements, player audio tracks, HLS renditions, and client profile responses:

```sh
# Inspect using default (all) client profiles:
./bin/goyt inspect -url "https://www.youtube.com/watch?v=VIDEO_ID"

# Inspect specific client profile (web or visionos):
./bin/goyt inspect -url "https://www.youtube.com/watch?v=VIDEO_ID" -client visionos
```

**Flags:**
- `-url`: Public YouTube video URL (required).
- `-client`: Client response to inspect: `web`, `visionos`, or `all` (default `all`).

### 3. `goyt hls` (Direct HLS Playlist Downloader)

Downloads arbitrary HLS master or media playlists directly from a URL:

```sh
./bin/goyt hls -url "https://example.com/playlist/master.m3u8" -height 1080 -out output.mp4
```

**Flags:**
- `-url`: Completed MPEG-TS media playlist or master playlist URL (required).
- `-height`: Maximum master-playlist variant height (default `1080`).
- `-out`: Destination file path, must have an `.mp4` extension (default `hls.mp4`).

### 4. Version & Help

```sh
# Print version
./bin/goyt -version

# Print general help and command list
./bin/goyt -help

# Subcommand-specific help
./bin/goyt download -help
./bin/goyt inspect -help
./bin/goyt hls -help
```

---

## Supported Scope

- Selected public, non-live YouTube videos accessible without authentication.
- Individual track URLs on `music.youtube.com` (`https://music.youtube.com/watch?v=VIDEO_ID`) for both video and audio downloads and stream inspection; album and playlist URLs remain unsupported.
- Explicit selection of direct HTTP or HLS downloading.
- H.264 video and AAC audio output in MP4 through the main YouTube CLI (video mode).
- Multi-format audio-only downloads across 9 formats (audio-only mode).
- Maximum-height selection from supported available formats in video mode; the requested
  height is a ceiling, not a guaranteed output resolution.
- Completed HLS playlists containing MPEG-TS or supported ID3-prefixed
  packed AAC segments, including supported separate audio renditions.
- In audio-only HLS mode, downloads only audio playlist manifests and audio segments without fetching video resources.
- Metadata checks for expected stream codecs, stream counts, and duration.
- Optional full audio/video decoding through the main YouTube CLI.

YouTube extraction depends on behavior that can change. Successful extraction
does not guarantee that every discovered media URL will accept downloads.

## Audio Selection & Formats

### Supported Audio Formats

| Format (`-audio-format`) | Container / Muxer | Extension | Codec | Stream Copy vs Re-encode | Quality / Bitrate Support |
|---|---|---|---|---|---|
| `best` (default) | Codec-dependent (`ipod`, `opus`, `ogg`, `flac`, etc.) | `.m4a` (AAC/ALAC), `.opus` (Opus), `.ogg` (Vorbis), `.flac` (FLAC), `.mp3` (MP3) | Source Codec | Stream copy (`-c:a copy`) without lossy transcoding | Quality/Bitrate rejected |
| `aac` | ADTS (`-f adts`) | `.aac` | `aac` | Copy if source is AAC & no bitrate; else re-encode (`aac`) | `-audio-bitrate` supported |
| `m4a` | MP4/M4A (`-f ipod`) | `.m4a` | `aac` | Copy if source is AAC & no bitrate; else re-encode (`aac`) | `-audio-bitrate` supported |
| `alac` | MP4/M4A (`-f ipod`) | `.m4a` | `alac` | Re-encode (`alac`) | Quality/Bitrate rejected |
| `flac` | FLAC (`-f flac`) | `.flac` | `flac` | Copy if source is FLAC; else re-encode (`flac`) | Quality/Bitrate rejected |
| `mp3` | MP3 (`-f mp3`) | `.mp3` | `mp3` | Copy if source is MP3 & no quality/bitrate; else re-encode (`libmp3lame`) | `-audio-quality` (0–9) OR `-audio-bitrate` |
| `opus` | Ogg/Opus (`-f opus`) | `.opus` | `opus` | Copy if source is Opus & no bitrate; else re-encode (`libopus`) | `-audio-bitrate` supported |
| `vorbis` | Ogg (`-f ogg`) | `.ogg` | `vorbis` | Copy if source is Vorbis & no bitrate; else re-encode (`libvorbis`) | `-audio-bitrate` supported |
| `wav` | WAV (`-f wav`) | `.wav` | `pcm_s16le` | Re-encode (`pcm_s16le`, 16-bit PCM little-endian) | Quality/Bitrate rejected |

### Format & Codec Notes
- **`best` Mode**: Preserves the exact audio stream provided by YouTube (normally Opus or AAC) using stream copy (`-c:a copy`). This prevents generational audio quality loss. `best` represents codec fidelity preservation, not an artificial upscaling of source audio.
- **Lossless Encoders (ALAC, FLAC, WAV)**: Re-encoding a lossy YouTube source (AAC or Opus) into FLAC, ALAC, or WAV produces a bit-exact lossless representation of the *decoded* audio, but cannot restore audio frequencies or fidelity previously lost during YouTube's initial lossy compression.
- **Lossy Encoders & Bitrate Control**: For `aac`, `m4a`, `mp3`, `opus`, and `vorbis`, an explicit bitrate can be requested via `-audio-bitrate` (e.g. `-audio-bitrate 192k`). An explicit bitrate forces re-encoding and avoids stream copy.
- **MP3 VBR Quality (`-audio-quality`)**: Specifies LAME VBR quality level `0` (highest quality, ~245 kbps) to `9` (lowest quality, ~65 kbps), default `2` (~190 kbps). Lower numbers request higher quality.

### Language & Original Track Preferences
For HLS and supported HTTP formats with audio track metadata, `goyt` prefers audio renditions whose names or metadata contain a recognized original-audio marker.

```sh
./bin/goyt download \
  -url "https://www.youtube.com/watch?v=VIDEO_ID" \
  -transport hls \
  -audio-only \
  -audio-format best
```

Original-audio recognition is a metadata heuristic, not verified provenance.
If no original marker is found, `goyt` selects fallback audio and emits a
warning. Multiple renditions marked original also produce an ambiguity
warning.

To explicitly request a language:

```sh
./bin/goyt download \
  -url "https://www.youtube.com/watch?v=VIDEO_ID" \
  -transport hls \
  -audio-only \
  -audio-language en \
  -audio-format m4a \
  -out audio-english.m4a \
  -decode-check
```

- `en` matches `en` and regional English tags such as `en-US`.
- `en-US` requires an exact language-tag match, ignoring case.
- If no supported matching external rendition exists, selection fails.
- Audio with missing language metadata cannot satisfy an explicit request.
- This flag selects existing audio; it does not translate audio.

## Verification & Safety

The main downloader checks the output stream metadata and duration with `ffprobe`:
- **Video mode (MP4)**: Verifies presence of H.264 video and AAC audio streams and compares duration against expected presentation duration.
- **Audio-only mode (all formats)**: Verifies exactly one audio stream of the expected resolved codec, zero video streams, matching container format, and validates duration against expected audio duration (accounting for encoder delay and padding tolerances).

Use `-decode-check` to additionally decode the complete output with FFmpeg (video and audio in MP4 mode; audio stream only in audio-only mode). This checks decodability but does not establish spoken language, perceptual quality, or lip-sync.

### Encoder Probing & Media Transfer Safety
- When a format requires re-encoding (`alac`, `flac`, `wav`, or re-encoded `mp3`, `opus`, `vorbis`, `aac`), `goyt` pre-probes FFmpeg for encoder availability (`HasEncoder`).
- Encoder capability checks happen before media transfer (downloading stream payloads or audio segments); metadata extraction and manifest requests may occur first to determine available stream codecs and select appropriate tracks.
- If a required encoder is missing, execution halts immediately with a clear error without downloading media payloads.

### Staging & Destination Preservation
- The output is staged in a temporary file in the destination directory and verified prior to replacing the user's destination file.
- Both metadata verification (`VerifyAudio` / `VerifyMP4`) AND requested full decode verification (`-decode-check`) finish before replacing the destination.
- If conversion, metadata verification, full decoding, or cancellation fails:
  - Any preexisting destination file is preserved untouched.
  - Useful intermediate files are retained in the work directory and the directory path is reported on standard error for inspection.
- On complete success and verification, the staged file is atomically committed to the destination.

## Development Checks

```sh
go fmt ./...
go vet ./...
go test -race ./...
go build ./...
```

Run the local FFmpeg and executor integration tests (including MP3 audio conversions):

```sh
go test -race -tags=integration \
  -run '^(TestFFmpegIntegration|TestExecutorIntegration|TestAudioExecutorIntegration|TestHLSAudioIntegration)$' \
  -count=1 -v ./...
```

These integration tests require `ffmpeg` and `ffprobe` on `PATH`.

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
- Audio-only downloads require a standalone audio stream (direct HTTP) or separate audio rendition (HLS). Muxed-only video/audio streams cannot be converted in audio-only mode and return an explicit unsupported error.
- Direct HLS media playlists must be confirmed audio-only to be downloaded in audio-only mode.
- MP3 audio conversion is lossy; AAC/Opus sources are re-encoded via `libmp3lame`.
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
- Executor merge/remux/audio-conversion jobs and HLS jobs retain intermediate files after
  failure, but subsequent invocations do not automatically resume those jobs as whole operations.
- Full decoding checks decodability, not perceptual audio/video synchronization.
- There is no dedicated stalled-transfer timeout. Cancellation and configured
  context/client timeouts bound operations.

---

## Package Layout

| Package / Directory | Description |
| --- | --- |
| `github.com/ammyy9908/goyt` (root) | Core public API: `Downloader`, `Executor`, `Planner`, `HLSDownloader`, `FFmpeg`, `Verifier`, and data models. |
| `extractor/youtube` | YouTube extractor implementation, Innertube API client, visitor data extraction, and HLS manifest extraction. |
| `internal/cli` | Consolidated CLI implementation: subcommands (`download`, `inspect`, `hls`), flag sets, and routing. |
| `cmd/goyt` | Thin entry point for the unified `goyt` CLI binary. |
| `scripts/` | Automated compatibility testing and verification runners (`check_youtube.py`). |
| `testdata/` | Unit and integration test fixtures (including mock local HLS streams). |
