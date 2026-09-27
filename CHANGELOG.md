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
- Comprehensive regression coverage for command routing, help, validation, and error states.

### Changed

- Consolidated `goyt`, `goyt-inspect`, and `goyt-hls` binaries into a single unified `goyt` executable.
- Moved CLI command handlers and flag parsing into `internal/cli`, keeping `cmd/goyt/main.go` as a thin entry point.
- Updated build targets, CI workflow, release packaging, and documentation for the unified binary.
- Updated release archives to contain strictly `goyt` (`goyt.exe` on Windows), `README.md`, and `LICENSE`.
- Allowed optional subtitle references during audio/video-only HLS selection.

### Known Limitations

- YouTube support covers a subset of public, non-live videos.
- No authenticated, DRM-protected, DASH, or SABR downloading.
- No JavaScript challenge solver or PO-token provider.
- No automatic transport fallback or expired-URL refresh.
- HLS excludes encryption, fragmented MP4 initialization sections, byte-range
  segments, discontinuities, nested masters, and alternate video renditions.
- Original-audio recognition relies on rendition-name metadata.
- Direct HTTP audio selection does not yet have equivalent original-track
  preference.
- Explicit language requests fail when a supported matching rendition cannot
  be identified.
- Failed processing and HLS jobs retain intermediate files but cannot
  automatically resume as whole jobs on a later invocation.
- No dedicated stalled-transfer timeout.
- Successful decoding does not verify language or perceptual synchronization.