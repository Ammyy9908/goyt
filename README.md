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
# Download video via direct HTTP streams with specific codec and container (e.g. VP9 in WebM)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -video-codec vp9 -container webm

# Download video via direct HTTP streams with AV1 in MKV container
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -video-codec av1 -container mkv

# Download video via direct HTTP streams with AV1 in MP4 container (inferred from -out)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -video-codec av1 -out video.mp4

# Download video via direct HTTP streams (merges separate video and audio streams into MP4 by default)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -height 1080 -out video.mp4

# Download video via HLS manifest with MKV container output
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -container mkv -out video.mkv

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

# Download with custom overall job timeout and network inactivity stall protection
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -timeout 2h -stall-timeout 45s

# Download with bounded URL refresh enabled (default 1 refresh attempt)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -url-refreshes 1

# Download with URL refresh disabled
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -url-refreshes 0

# Download with CLI overall timeout disabled (only parent context applies)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -timeout 0

# Start a new persistent download job in ./my-job (HTTP VP9 WebM)
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -video-codec vp9 -container webm -job-dir ./my-job

# Start a new persistent HLS download job in ./hls-job with decode check
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -job-dir ./hls-job -out video.mp4 -decode-check

# Start a new persistent HLS audio-only job in ./audio-job
./bin/goyt download -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport hls -audio-only -audio-format mp3 -job-dir ./audio-job -out song.mp3

# Resume an interrupted persistent download job
./bin/goyt download -resume-job ./my-job

# Resume an interrupted job with operational overrides
./bin/goyt download -resume-job ./my-job -timeout 1h -stall-timeout 30s -url-refreshes 2
```

**Flags:**
- `-url`: YouTube video URL (required for new jobs; rejected with `-resume-job`).
- `-video-codec`: Video codec: `h264`, `vp9`, or `av1` (default `h264`). Explicit selection is strict; no silent fallback to another codec occurs if unavailable. Rejected with `-audio-only`.
- `-container`: Video output container: `mp4`, `webm`, or `mkv` (default `mp4`). Rejected with `-audio-only`.
  - If `-container` is explicit, it is used.
  - If only `-out` is explicit, the container is inferred from its extension (`.mp4`, `.webm`, `.mkv`).
  - If both `-container` and `-out` are explicit, their container types must agree.
  - If `-out` is omitted, defaults to `video.<container>`.
- `-transport`: Download transport protocol: `http` or `hls` (default `http`). Persistent jobs support both `http` and `hls` transports for YouTube downloads.
- `-job-dir`: Path to create a new persistent download job directory. Fails if the directory already exists.
- `-resume-job`: Path to an existing persistent download job directory to resume. Source, selection, audio, and output flags are loaded from the job manifest and cannot be overridden.
- `-audio-only`: Download audio without video. Defaults output format to `best` and output filename to `audio.<resolved_ext>` when `-out` is omitted. Explicitly supplied `-video-codec` or `-container` is rejected.
- `-audio-format`: Output format for audio-only mode (default `best`). Supported formats: `best`, `aac`, `alac`, `flac`, `m4a`, `mp3`, `opus`, `vorbis`, `wav`. Only valid with `-audio-only`.
- `-audio-quality`: MP3 VBR quality level, integer `0` (highest quality, ~245 kbps) to `9` (lowest quality, ~65 kbps), default `2` (~190 kbps). Lower values request higher quality. Only valid for `mp3` format with `-audio-only`. Mutually exclusive with `-audio-bitrate`.
- `-audio-bitrate`: Target audio bitrate for lossy encoders (e.g. `128k`, `192k`, `320k`). Supported for `aac`, `m4a`, `mp3`, `opus`, and `vorbis`. Disallowed for `best`, `alac`, `flac`, and `wav`. Mutually exclusive with `-audio-quality`.
- `-height`: Maximum desired video height in pixels (default `1080`). Disallowed when explicitly specified in `-audio-only` mode.
- `-audio-language`: Audio language tag, e.g. `en` or `en-US` (supports HLS and HTTP formats with language metadata).
- `-timeout`: Overall job timeout covering extraction, downloads, processing, and verification (default `30m`). Set to `0` to disable the CLI-imposed overall deadline. Resumed jobs receive a fresh deadline.
- `-stall-timeout`: Network inactivity timeout per media request (default `60s`). Limits inactivity while waiting for response headers or receiving body bytes. Set to `0` to disable inactivity detection.
- `-url-refreshes`: Maximum URL re-extraction attempts across the entire job on expired or forbidden media (default `1`, `0` disables). Resumed jobs receive a fresh refresh budget.
- `-out`: Destination file path. In video mode, extension must match the container (`.mp4`, `.webm`, `.mkv`, default `video.<container>`). In audio-only mode, extension must match the resolved format (default `audio.<ext>`).
- `-decode-check`: Optionally decodes the complete output after verification to check for stream errors.
- `-version`: Print `goyt` version.

**Legacy Shorthand:**
The legacy shorthand format without a subcommand is fully preserved:
```sh
./bin/goyt -url "https://www.youtube.com/watch?v=VIDEO_ID" -transport http -audio-only -audio-format mp3 -out audio.mp3
```

### 2. `goyt inspect` (Diagnostic Inspector)

Inspects available YouTube formats, streaming endpoints (HLS/DASH/SABR), signature/N-parameter challenge requirements, player audio tracks, HLS renditions, and client profile responses in human-readable table or structured machine-readable JSON format:

```sh
# Inspect using default (all) client profiles (human-readable tables):
./bin/goyt inspect -url "https://www.youtube.com/watch?v=VIDEO_ID"

# Inspect specific client profile (web or visionos):
./bin/goyt inspect -url "https://www.youtube.com/watch?v=VIDEO_ID" -client visionos

# Inspect in machine-readable JSON format (visionos client):
./bin/goyt inspect -url "https://youtu.be/VIDEO_ID" -client visionos -json > info.json

# Inspect YouTube Music track URL with JSON output:
./bin/goyt inspect -url "https://music.youtube.com/watch?v=VIDEO_ID" -json

# Inspect all client profiles with JSON output:
./bin/goyt inspect -url "https://www.youtube.com/watch?v=VIDEO_ID" -client all -json
```

**Flags:**
- `-url`: Public YouTube video or YouTube Music track URL (required).
- `-client`: Client response to inspect: `web`, `visionos`, or `all` (default `all`).
- `-json`: Emit stable, machine-readable JSON output to stdout.

#### Machine-Readable JSON Output (`-json`)

When `-json` is specified:
- `stdout` contains **exactly one valid JSON document followed by a newline**. No headings, table formatting, progress messages, or trailing errors are emitted to `stdout`.
- Operational diagnostics and error messages are written exclusively to `stderr`.
- Network requests are never duplicated; inspection results are reused.
- FFmpeg is not required and no media segments are downloaded during inspection.

##### Schema Specification (Version 1)

Top-level structure:
```json
{
  "schema_version": 1,
  "results": [
    {
      "client": "visionos",
      "status": "ok",
      "media": {
        "id": "VIDEO_ID",
        "title": "Example Video Title",
        "duration_seconds": 262.0
      },
      "playback": {
        "status": "OK",
        "reason": null
      },
      "streaming": {
        "hls": true,
        "dash": false,
        "sabr": true
      },
      "available_video_heights": [144, 240, 360, 480, 720, 1080],
      "formats": [
        {
          "id": 137,
          "mime_type": "video/mp4",
          "codecs": "avc1.640028",
          "quality": "1080p",
          "width": 1920,
          "height": 1080,
          "bitrate": 4500000,
          "has_direct_url": false,
          "signature_challenge": true,
          "n_challenge": true,
          "drm_reported": false
        }
      ],
      "audio_tracks": [
        {
          "id": "en.4",
          "name": "English (original)",
          "language": null,
          "default": true,
          "original_hint": true
        }
      ],
      "hls_audio_renditions": [
        {
          "group_id": "audio",
          "name": "English (original)",
          "language": "en",
          "default": true,
          "autoselect": true,
          "original_hint": true
        }
      ],
      "limitations": [
        "Discovered URLs have not been verified for playback or downloading."
      ],
      "error": null
    }
  ]
}
```

##### Field Semantics & Rules
- **Units**: Durations are represented in floating-point seconds (`duration_seconds`), not nanoseconds.
- **Null Semantics**: Unknown numeric values (`width`, `height`, `bitrate`, `duration_seconds`) and unavailable string fields (`reason`, `codecs`, `quality`, `language`) use `null` rather than misleading zeroes or empty strings.
- **Collection Semantics**: Lists (`results`, `available_video_heights`, `formats`, `audio_tracks`, `hls_audio_renditions`, `limitations`) always marshal as empty arrays `[]`, never `null`.
- **`available_video_heights`**: Array of positive, unique video heights sorted in ascending order. These heights describe discovered formats in the player response, **not guaranteed downloadable or CLI-supported resolutions**.
- **Format Ordering**: Format entries preserve the discovery order from the player response; duplicate format IDs (e.g., adaptive formats associated with different audio tracks) are preserved deterministically.
- **Original-Audio Heuristic**: The `original_hint` boolean reflects the existing heuristic (e.g. rendition name containing `(original)` or track ID containing `original`) with documented provenance; it does not represent verified creator provenance. If language metadata is not exposed by YouTube, `language` is left as `null` without guessing from display names.
- **Exclusion of Sensitive Data**: JSON output uses an explicit field allowlist. It never emits signed media/manifest URLs, session cookies, authorization headers, visitor data identifiers (`VISITOR_DATA` / `X-Goog-Visitor-Id`), PO tokens, or raw API response payloads.
- **Errors & Exit Codes**:
  - If a requested client fails extraction, `status` is set to `"error"` with a structured `error` object (`code` and `message`), while successful client results in `-client all` mode are fully preserved.
  - If any requested client fails extraction, `goyt inspect` exits with a non-zero exit code.
  - A restricted playback response (e.g., `LOGIN_REQUIRED` or `UNPLAYABLE`) is an inspected response returned with `status: "ok"` and `playback.status: "LOGIN_REQUIRED"`; it does not cause a non-zero exit code.
  - CLI argument parsing errors are printed to `stderr` with a non-zero exit code and emit no stdout output.

### 3. `goyt hls` (Direct HLS Playlist Downloader)

Downloads arbitrary HLS master or media playlists directly from a URL:

```sh
./bin/goyt hls -url "https://example.com/playlist/master.m3u8" -height 1080 -out output.mp4
```

**Flags:**
- `-url`: Completed MPEG-TS media playlist or master playlist URL (required).
- `-height`: Maximum master-playlist variant height (default `1080`).
- `-timeout`: Overall job timeout covering playlist fetching, segment downloads, processing, and verification (default `30m`). Set to `0` to disable the CLI-imposed overall deadline.
- `-stall-timeout`: Network inactivity timeout per segment request (default `60s`). Limits inactivity while waiting for response headers or receiving body bytes. Set to `0` to disable inactivity detection.
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
- Explicit video codec selection (`h264`, `vp9`, `av1`) and output container selection (`mp4`, `webm`, `mkv`) for direct HTTP video downloads.
- Multi-container output (`mp4`, `mkv`) for supported H.264/AAC HLS video downloads.
- Multi-format audio-only downloads across 9 formats (audio-only mode).
- Maximum-height selection from supported available formats in video mode; the requested height is a ceiling, not a guaranteed output resolution.
- Completed HLS playlists containing MPEG-TS or supported ID3-prefixed packed AAC segments, including supported separate audio renditions.
- In audio-only HLS mode, downloads only audio playlist manifests and audio segments without fetching video resources.
- Metadata checks for expected stream codecs, stream counts, dimensions, container, and duration.
- Optional full audio/video decoding through the main YouTube CLI.

YouTube extraction depends on behavior that can change. Successful extraction does not guarantee that every discovered media URL will accept downloads.

## Video Selection & Containers

### Supported Video Codec & Container Matrix

`goyt` implements a centralized compatibility policy for video and audio stream combinations without video transcoding (all video downloads perform direct stream copy `-c copy`):

| Container (`-container`) | Extension | Supported Video Codecs (`-video-codec`) | Supported Audio Codec | Notes & Scope |
|---|---|---|---|---|
| `mp4` (default) | `.mp4` | `h264` (default), `av1` | `aac` | Standard MP4 container with AAC audio. Supported across HTTP and HLS (HLS supports `h264`). |
| `webm` | `.webm` | `vp9`, `av1` | `opus` | Matroska-based WebM container with Opus audio. Supported for direct HTTP downloads. |
| `mkv` | `.mkv` | `h264`, `vp9`, `av1` | `opus` or `aac` | Matroska container supporting all video codecs with Opus or AAC audio. Supported across HTTP and HLS (HLS supports `h264`). |

### Strict Codec Selection & Stream Copy
- **No Video Transcoding**: `goyt` uses FFmpeg stream copy exclusively (`-c copy`) for video downloads. It never performs lossy video re-encoding or changes the requested video codec.
- **Strict Requests**: If a requested video codec is not available in the source media or cannot be packaged into the requested container, `goyt` returns an actionable error immediately without silently falling back to a different codec.
- **Audio Pairing for MKV**: MKV supports both Opus and AAC audio tracks. `goyt` selects audio by prioritizing:
  1. Explicitly requested audio language (`-audio-language`).
  2. Original audio tracks (heuristics/metadata markers).
  3. Default audio tracks.
  4. Deterministic audio codec preference: `opus` is preferred over `aac`.
  5. Bitrate comparison only between identical audio codecs (avoiding misleading cross-codec bitrate comparisons).

### HTTP vs. HLS Scope
- **Direct HTTP**: Supports all 10 valid combinations across H.264, VP9, and AV1 video codecs and MP4, WebM, and MKV containers.
- **HLS Downloads**: Supports H.264 video with AAC audio output as either `mp4` or `mkv`. Requests for `vp9` or `av1` video or `webm` container with HLS transport are rejected early with an explanation that it is a current HLS implementation limitation.

### Player & Device Compatibility
- Player and hardware support varies significantly across codecs and containers:
  - **H.264 / MP4**: Broadest compatibility across virtually all operating systems, hardware decoders, browsers, and mobile devices (including Apple QuickTime and Safari).
  - **VP9 / WebM & MKV**: Excellent compatibility with modern browsers (Chrome, Firefox, Edge), Android devices, and media players like VLC, mpv, and IINA; native playback in macOS QuickTime Player may be limited.
  - **AV1**: State-of-the-art compression efficiency. Requires modern hardware with AV1 decode acceleration or sufficiently capable CPU decoding. Players like VLC, mpv, and modern browsers fully support AV1.

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

### Timeouts & Stalled-Transfer Recovery
- **Overall Job Timeout (`-timeout`)**: Enforces a total wall-clock deadline spanning metadata extraction, playlist resolution, media downloads, FFmpeg processing, and verification checks (default `30m`). Setting `-timeout 0` disables the CLI-imposed overall deadline (any parent context deadline still applies).
- **Network Inactivity Protection (`-stall-timeout` / `DownloadOptions.StallTimeout`)**: Monitors media transfers and cancels individual requests that hang while waiting for response headers or body bytes (default `60s`).
  - The inactivity timer resets whenever data is received (`n > 0` bytes read). Slow transfers that continuously stream data will **not** be treated as stalled merely because the total transfer takes a long time.
  - Non-network operations (disk writes, progress callbacks, retry backoff, FFmpeg conversion, and verification) are not counted against the network inactivity timeout.
  - Stalled transfers yield an error wrapping `ErrDownloadStalled` and are retried within the configured `MaxRetries` budget (default 2 retries).
  - Safe partial resumption is attempted when the server provides a strong ETag and valid byte ranges; otherwise the transfer restarts cleanly without appending corrupt data.
  - Parent context cancellation (`context.Canceled`) and parent deadlines (`context.DeadlineExceeded`) take strict precedence over stall classification and immediately abort the job without retry.
  - **Caller HTTP Client Configuration**: Custom `http.Client.Timeout` or custom `http.RoundTripper` implementations configured by library callers apply independently to HTTP operations; custom transport implementations that ignore request context cancellation will not benefit from request-scoped stall watchdog cancellation.

### Bounded URL Refresh & Media Access Recovery
- **Single Job Budget (`-url-refreshes`)**: Maintains a single, job-scoped URL refresh budget (default `1`, `0` disables) across all streams, segments, and formats in the download job. Every re-extraction attempt, including failed attempts, charges the budget.
- **Trigger Policy**:
  - Automatically refreshes on known-expiration errors (`ErrResourceExpired` or `Resource.ExpiresAt` passed prior to making a network request).
  - On media access failures (HTTP 403 Forbidden or HTTP 410 Gone), a bounded refresh is attempted as a recovery probe (`Refreshing playback URLs after media access failure (X/Y).`). HTTP 403/410 status codes are treated as access failures rather than definitive proof of expiration.
  - Non-refreshable errors (user cancellation, job deadline exceeded, disk/verification errors, unsupported codecs, rate limits 429, or server 5xx errors handled by network retries) do not trigger URL refresh.
- **Strict Stream Identity Preservation**:
  - **Direct HTTP**: Re-extraction matches the original YouTube video ID, format itag, container, video/audio codecs, pixel dimensions, and audio track identity (language, track ID, original/default markers). If representation identity is ambiguous or disappeared, execution halts with a clear error rather than silently falling back to a different quality or language.
  - **HLS**: Re-extraction matches the original variant resolution, codecs, and audio rendition metadata (name, language, default, autoselect). Heuristic audio selection is never re-run, preventing silent language or rendition switches.
- **Safe Restart & Partial File Isolation**:
  - When an HTTP media URL changes, incomplete transfers restart cleanly from byte 0 in isolation without appending new data to stale partial files.
  - HLS downloads restart in a fresh attempt directory, ensuring old and new segment generations or local playlist files are never mixed.
  - Same-resource strong-ETag and Content-Range resume rules continue to apply for network retries of unchanged URLs.
- **Destination Safety & Retention**:
  - Preexisting destination files remain untouched until all downloads, processing, metadata verification, and optional decode checks succeed.
  - Failed attempt directories are retained under the standard work-directory policy and logged to standard error.

### Persistent Download Jobs & Resume (`-job-dir` / `-resume-job`)

`goyt` supports durable, multi-stream persistent download jobs for direct HTTP and YouTube HLS video and audio-only downloads that survive process restarts and network interruptions:

- **Creating a Job (`-job-dir DIR`)**:
  - Supports both direct HTTP (`-transport http`) and YouTube HLS (`-transport hls`) transports. Direct arbitrary-URL `goyt hls` does not support job persistence.
  - Fails immediately if the directory `DIR` already exists, ensuring existing jobs and directories are never overwritten.
  - Resolves and records the absolute destination path in a versioned job manifest (`schema_version: 1`).
  - Writes manifests atomically via temporary file and rename (`0600` file permissions on supported systems).
  - Holds an OS-backed exclusive file lock (`flock` on Unix, `LockFileEx` on Windows) on `job.lock` for the entire process invocation. If another process attempts to open the same job, it fails immediately with `ErrJobLocked`. Locks automatically release on process exit or abnormal crash without relying on stale PID files.
- **Resuming a Job (`-resume-job DIR`)**:
  - Restores the YouTube source URL, video ID, transport, video codec, output container, format selection constraints, audio settings, decode check preference, and absolute destination path directly from the manifest.
  - Explicit specification of source, selection, codec, container, audio, or output flags (`-url`, `-out`, `-height`, `-video-codec`, `-container`, `-transport`, `-decode-check`, `-audio-only`, `-audio-format`, `-audio-quality`, `-audio-bitrate`, `-audio-language`) is rejected rather than silently overriding stored job parameters.
  - Operational parameters (`-timeout`, `-stall-timeout`, `-url-refreshes`) may be overridden on resume; otherwise, stored operational defaults apply.
  - Each resumed invocation receives a fresh overall deadline and fresh URL refresh budget.
  - Completed-job recognition supports MP4, WebM, and MKV outputs.
  - Legacy manifests without new container/codec fields are cleanly loaded and retain default H.264/AAC MP4 semantics.
  - On cancellation (e.g. Ctrl+C), execution halts promptly while retaining resumable partial and completed stream/segment state.
- **Initial Execution & Duplicate Extraction Elimination**:
  - Newly created persistent jobs (`-job-dir DIR`) pass the freshly resolved presentation directly into initial execution in memory. Execution proceeds immediately without redundant network extraction or premature generation bumping.
- **Conservative HLS Segment Reuse & Generation Isolation**:
  - Persistent HLS jobs support:
    1. *Segment reuse*: Completed segments are reused within an unchanged presentation generation when compatibility checks pass (matching pinned variant/audio identities, ordered playlist structure, segment URL fingerprints, and local SHA-256 file integrity).
    2. *Safe full restart*: If signed URLs, query parameters, tokens, or playlist structure change upon re-extraction in a new process or during URL refresh, the presentation safely restarts from segment 0 in an isolated new generation (e.g. `gen-1` -> `gen-2`). Old and refreshed segment generations are never mixed. Standard error clearly reports that previously completed segments will not be reused because remote compatibility could not be established.
    3. *Offline processing*: When all segments across all tracks are complete and verified, packaging and verification proceed entirely offline without network extraction.
    4. *Completed-output recognition*: Resuming a completed job verifies the final destination output identity without downloading.
  - *URL Fingerprints*: `goyt` fingerprints the exact resolved segment URL using SHA-256 without persisting signed URLs, request headers, cookies, or tokens. This proves exact URL identity against the checkpointed playlist. It does not attempt to guess remote byte continuity when YouTube signs fresh URLs across process restarts.
- **Completed Input Reuse & Offline Processing**:
  - Checkpointed HTTP streams or HLS segments marked complete are verified against their recorded SHA-256 integrity hash and byte size.
  - If all inputs/segments across all tracks are already complete and verified, merging/conversion, local playlist generation, and verification proceed entirely offline without requiring network extraction.
  - If a completed file's size or SHA-256 mismatches the manifest, it is safely re-downloaded from scratch.
- **Incomplete Input Resume & Refresh**:
  - When incomplete streams or segments exist, `goyt` re-extracts fresh media URLs using the canonical video ID.
  - Pinned format and variant matching ensures only the exact originally selected representation is matched without switching codecs, containers, languages, or representations. Best-format heuristics are never re-run.
  - Safe same-resource strong-ETag and byte-range resumes continue where possible for HTTP streams.
- **Runnable Resume Command**:
  - When a job is interrupted or fails, `goyt` prints a directly runnable command using the resolved executable binary path and absolute job directory with shell-appropriate argument quoting.
- **Execution Stages & Crash Recovery**:
  - Stages tracked in manifest: `planned`, `downloading`, `processing`, `verifying`, `ready_to_commit`, and `completed`.
  - Incomplete processing outputs are discarded and re-processed safely.
  - Output staging files are generated on the destination filesystem (or copied via temporary destination files) to prevent cross-device rename failures.
  - Pre-commit identity (final SHA-256 and byte size) is recorded before committing. If a crash occurs during destination rename, subsequent resume recognizes the committed output and completes cleanly.
  - Once committed to the destination, intermediate input and segment directories (`inputs/` and `hls/`) inside the job directory are deleted to free disk space, while retaining a lightweight completed manifest.
  - Resuming a completed job verifies the final destination output identity (SHA-256 and size) across MP4, WebM, and MKV and reports success without network requests. If the destination file was deleted or altered, resume returns a clear error (`ErrCompletedOutputMismatch`).
- **Security & Untrusted State**:
  - Manifest files and internal relative paths are validated against directory traversal, absolute paths, and symlinks. State is treated as untrusted input.

## Development Checks

```sh
go fmt ./...
go vet ./...
go test -race ./...
go build ./...
```

Run the local FFmpeg, executor, and persistent job integration tests:

```sh
go test -race -tags=integration \
  -run '^(TestFFmpegIntegration|TestExecutorIntegration|TestAudioExecutorIntegration|TestHLSAudioIntegration|TestVideoExecutorIntegration_AllCombinations|TestHLSVideoIntegration_MKV|TestDecodeFailurePreservesExistingDestination|TestJobIntegration|TestHLSJobIntegration)$' \
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
- No automatic transport switching between HTTP and HLS, client fallback, or quality fallback. Arbitrary-URL `goyt hls` does not support YouTube re-extraction or persistent jobs.
- Persistent jobs (`-job-dir` / `-resume-job`) are supported for direct HTTP and YouTube HLS video and audio-only downloads. Persistent arbitrary-URL `goyt hls` and automatic discovery of old temporary directories are not implemented in this phase.
- Conservative HLS reuse: changed signed URLs or playlist structure require a complete HLS restart in a new generation. This conservative policy guarantees generation isolation and playlist integrity, but may reduce segment reuse after signed URLs change.
- Concurrency on persistent jobs is strictly single-process; concurrent invocations on the same job directory fail fast via OS-backed locking.
- If a completed persistent job's destination file is deleted or modified, resuming it will fail with `ErrCompletedOutputMismatch` and requires creating a new job.
- HLS support excludes encryption, fragmented MP4 initialization sections,
  byte-range segments, discontinuities, nested master playlists, and alternate
  video renditions.
- Subtitle references may be ignored for audio/video downloads; subtitle
  downloading and muxing are not implemented.
- Individual HTTP transfers can resume matching partial files when the server
  provides a strong ETag and valid byte-range responses. An interrupted HTTP
  transfer may restart the affected stream from zero when safe byte-range
  resumption cannot be established, such as when a strong ETag is unavailable
  or the server ignores the range request. This can increase download time and
  bandwidth usage. Recovery diagnostics report whether a transfer resumes or restarts.
- Nonpersistent downloads retain temporary attempt directories on failure for manual inspection without whole-job resume.
- Full decoding checks decodability, not perceptual audio/video synchronization.

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
