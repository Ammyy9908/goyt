# Changelog

## Unreleased — planned v0.1.0

### Added

- Native Go YouTube metadata inspection and supported direct-format extraction.
- Explicit HTTP and HLS transport selection in the main download CLI.
- Maximum-height selection with codec and container compatibility checks.
- Direct HTTP retries, cancellation, and strong-ETag-based partial resume.
- Resource-expiry checks before direct HTTP download attempts.
- FFmpeg stream merging and remuxing without re-encoding.
- Output codec/duration verification and optional full decoding.
- Completed HLS media and master playlist handling.
- Supported separate audio renditions and ID3-prefixed packed AAC segments.
- Local HLS playlist generation that retains segment timing metadata.
- HLS audio-language selection.
- Preference for HLS audio renditions explicitly marked original in their names.
- Warnings for uncertain or ambiguous automatic audio selection.
- YouTube inspection, generic HLS downloading, and compatibility-test tools.
- Unified multi-subcommand CLI (`goyt download`, `goyt inspect`, `goyt hls`) with independent help.
- Backward-compatible legacy flag shorthand (`goyt -url URL ...`).
- Support for individual YouTube Music track URLs (`https://music.youtube.com/watch?v=VIDEO_ID`) in `goyt download` and `goyt inspect`, extracting the canonical video ID while ignoring extraneous query/tracking parameters (`si`, `list`, etc.).
- Multi-format audio-only download support in `goyt download` (`-audio-only`, `-audio-format`, `-audio-quality`, `-audio-bitrate`).
- Support for 9 output formats: `best`, `aac`, `alac`, `flac`, `m4a`, `mp3`, `opus`, `vorbis`, and `wav`.
- Centralized `AudioOutputSpec` defining requested format, resolved codec, container/muxer, extension, copy/encode decision, and validated encoder settings.
- Stream copy preservation for compatible formats in `best`, `aac`, and `m4a` when no encoding overrides are specified.
- Lossy audio bitrate control (`-audio-bitrate`) for `aac`, `m4a`, `mp3`, `opus`, and `vorbis`.
- MP3 VBR quality control (`-audio-quality 0-9`, default 2).
- Strict mutual exclusivity between `-audio-quality` and `-audio-bitrate`, with rejection of quality/bitrate settings for lossless/passthrough formats (`best`, `alac`, `flac`, `wav`).
- Pre-transfer FFmpeg encoder capability probing (`HasEncoder`) preventing media stream downloads when a required encoder is missing (following initial metadata extraction/planning).
- Deterministic HTTP audio format ranking with language and original track selection.
- HLS audio-only selection and download that avoids fetching video playlists and segments.
- Rejection of muxed-only sources in audio-only mode (`ErrMuxedOnlySource`).
- Output codec, stream count, container, duration, and decode verification (`VerifyAudio` and `CheckDecodeAudio`).
- Pre-commit staging of audio and video outputs where metadata verification and full decode verification finish before replacing the destination file.
- Preservation of existing destination files on failure, invalid verification, decode error, or cancellation.
- Comprehensive unit and integration test coverage for all audio output formats across HTTP and HLS pipelines.

### Changed

- Consolidated `goyt`, `goyt-inspect`, and `goyt-hls` binaries into a single unified `goyt` executable.
- Moved CLI command handlers and flag parsing into `internal/cli`, keeping `cmd/goyt/main.go` as a thin entry point.
- Updated build targets, CI workflow, release packaging, and documentation for the unified binary.
- Updated release archives to contain strictly `goyt` (`goyt.exe` on Windows), `README.md`, and `LICENSE`.
- Allowed optional subtitle references during audio/video-only HLS selection.

### Known Limitations

- YouTube support covers a subset of public, non-live videos.
- YouTube Music support is limited to individual track watch URLs; albums, playlists, and browse endpoints on `music.youtube.com` are unsupported.
- No authenticated, DRM-protected, DASH, or SABR downloading.
- No JavaScript challenge solver or PO-token provider.
- No automatic transport fallback or expired-URL refresh.
- Audio-only mode requires standalone audio streams; muxed-only sources are rejected.
- Lossy transcoding (`mp3`, `opus`, `vorbis`, lossy `aac`/`m4a`) decodes and re-encodes source audio.
- Converting lossy source audio to lossless formats (`alac`, `flac`, `wav`) does not restore lost audio quality or frequency data.
- Encoding to specific formats requires FFmpeg built with the appropriate encoder library (e.g. `libmp3lame`, `libopus`, `libvorbis`).
- HLS excludes encryption, fragmented MP4 initialization sections, byte-range
  segments, discontinuities, nested masters, and alternate video renditions.
- Original-audio recognition relies on rendition-name metadata.
- Explicit language requests fail when a supported matching rendition cannot
  be identified.
- Failed processing and HLS jobs retain intermediate files but cannot
  automatically resume as whole jobs on a later invocation.
- No dedicated stalled-transfer timeout.
- Successful decoding does not verify language or perceptual synchronization.