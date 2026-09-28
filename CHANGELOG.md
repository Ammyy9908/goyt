# Changelog

## [Unreleased]

### Added

- Centralized YouTube client registry in `extractor/youtube` consolidating client identities (`visionos`, `web`), headers, context configuration, and capability modeling (`MetadataInspection`, `DirectExtraction`, `HLSExtraction`, `SignatureDecipher`, `NChallengeSolve`, `POTokenProvider`).
- Explicit client selection flag `-client visionos|web` in `goyt download` (default `visionos`, preserving existing download behavior).
- Rejection of `-client all` and unknown client names in `goyt download` prior to network operations.
- Preserved `goyt inspect -client web|visionos|all` (default `all`) and Schema 1 JSON inspection compatibility.
- Typed extractor error `ExtractionError` with stable, machine-readable diagnostic error codes:
  - `playback_restricted`: Playback restricted by YouTube (e.g. `LOGIN_REQUIRED` without bot-check marker, `UNPLAYABLE`, age gate, geographic restriction, or private video).
  - `bot_check_required`: YouTube returned an explicit bot-check gate (e.g. "Sign in to confirm you’re not a bot").
  - `no_supported_formats`: Response playability is OK but no direct formats are supported by goyt, preserving diagnostic obstacle list (`signature_challenge`, `n_challenge`, `sabr`, `drm`).
  - `signature_challenge_required`: All potential formats require JavaScript signature deciphering.
  - `n_challenge_required`: Formats or HLS manifest URLs require JavaScript N-parameter transformation.
  - `manifest_unavailable`: Requested HLS stream does not expose a usable manifest URL.
  - `invalid_player_response`: Watch page response is missing embedded player metadata or has invalid JSON.
  - `extraction_request_failed`: HTTP transport or network communication failed.
  - `context_canceled`: Operation canceled by context.
  - `timeout`: Operation timed out.
- Context cancellation and deadline error wrapping preserving `errors.Is(err, context.Canceled)` and `errors.Is(err, context.DeadlineExceeded)`.
- Diagnostic safety policy sanitizing signed media/manifest URLs, query parameters, cookies, authorization headers, and visitor identifiers from public CLI and JSON error messages without leaking secrets or raw API responses.
- Replaceable JavaScript challenge-solver interface `ChallengeSolver` in `extractor/youtube` with structured models for signature deciphering (`SignatureChallenge`), n-parameter transformation (`NChallenge`), player script context (`PlayerScript`), and batch results (`ChallengeBatchResult`).
- Player-script discovery (`DiscoverPlayerScriptURL`) discovering player JavaScript URLs from watch page metadata (`PLAYER_JS_URL`, `jsUrl`, `<script src="...">`), resolving relative paths, validating HTTPS, enforcing strict explicit YouTube host allowlist (`www.youtube.com`, `youtube.com`, `m.youtube.com`, `music.youtube.com`), rejecting explicit ports, credentials, and lookalike domains, validating redirects on every hop, and performing bounded downloads (up to 10MB).
- Precision challenge parsing for `signatureCipher`/`cipher` query payloads, extracting `s`, `sp` (with authoritative fallback `sp=sig`), and nested media URLs with structural query validation: rejecting duplicate critical parameters (`url`, `s`, `sp`, `n`) even when values match, and rejecting conflicting simultaneous `signatureCipher` and `cipher` fields on the same format.
- Robust solver result validation rejecting unknown, duplicate, missing, or mismatched result IDs, enforcing batch size (`MaxChallengeBatchSize = 100`) and value length (`MaxTransformedValueLength = 2048`) bounds, and ensuring formats requiring both signature and n transformations remain unusable if either operation fails.
- Player-script identity model (`PlayerScript.Identity()`: `URL#sha256=HEX`) combining the canonical script URL and full SHA-256 digest of the script source to prevent cross-player script conflation or stale transformation caching across different script contents sharing the same URL path.
- Thread-safe extractor solver configuration via `sync.RWMutex` with atomic extraction-scoped solver snapshots preventing race conditions during concurrent extractions.
- Integrated challenge solving into the direct format extraction pipeline: deduplicating challenges across candidates within an extraction run, preserving partial solver successes, rejecting unverified or incomplete candidate transformations, and retaining unchallenged formats without solver dependencies.
- Added functional options `WithChallengeSolver` and `WithHTTPClient` on `youtube.New`, allowing clean dependency injection without breaking existing constructors or public APIs.
- Extended typed extractor diagnostics with `player_script_unavailable` and `challenge_solver_failed` error codes with safe public messages that never expose challenge tokens, script contents, or signed media URLs.
- Implemented real YouTube signature and n challenge solver in `extractor/youtube/jssolver` implementing the `youtube.ChallengeSolver` interface, powered by an embedded AST-based engine bundling `yt-dlp-ejs` 0.8.0 (Unlicense), `meriyah` 6.1.4 (ISC), and `astring` 1.9.0 (MIT).
- Support for external JavaScript runtimes (`node`, `deno`, `bun`, `qjs`, `auto`, or custom binary path) with runtime detection and actionable diagnostic guidance.
- Strict process boundaries and execution sandboxing: direct argument passing (no shell), versioned JSON protocol over stdin/stdout, bounded stdio (10MB stdout with immediate process kill on overflow, 64KB stderr drain), 15s timeout with process termination on context cancellation, and Deno capability sandboxing (`--no-prompt`, `--allow-read` for the bundle script only, with network, file writing, environment, and child process access disallowed).
- In-memory thread-safe LRU challenge cache (`BoundedChallengeCache`, default 1,000 capacity) keyed by structured composite keys (`bundleVersion|scriptIdentity|challengeKind|inputVal`), with per-entry key and value length bounds, avoiding redundant process executions without persisting sensitive challenge values or URLs to disk.
- Added explicit CLI configuration flag `-js-runtime none|node|deno|bun|qjs|auto|PATH` (default `none`) across `goyt download` and `goyt inspect`, ensuring default no-solver backward compatibility, propagating the configured solver across extraction and URL refresh, and validating solver configuration during inspection without downloading media segments or declaring unresolved formats as verified.
- Comprehensive offline test suite using real recorded player script fixtures (`player_7460dd14_en_GB.js`, `player_7460dd14_en_US.js`) with documented provenance, testing signature transforms, n transforms, combined challenges, player identity isolation, process timeouts/cancellation, stdout overflow termination, stderr draining, Deno behavioral sandbox access rejection, temporary bundle creation/permission/cleanup lifecycle, and sensitive secret redaction.
- Opt-in live test suite (`-tags=livetest`) verifying end-to-end extraction against live YouTube `web` client streams, confirming solver execution on genuine challenges, and validating HTTP 206 Partial Content binary media retrieval.
- Sanitized per-format challenge-resolution diagnostics tracking actual resolution provenance (`not_required`, `runtime`, `cache`, `unknown`, `failed`) for signature deciphering and n-parameter transformation across candidate formats.
- Result-scoped diagnostics via `DownloadableResult` and `ExtractDownloadableResult`, isolating diagnostics per extraction call and completely preventing cross-extraction interference and state pollution during concurrent extractions on a shared `Extractor` instance.
- Audio track-specific diagnostic attribution using composite diagnostic keys (`FormatDiagnosticKey(itag, audioTrackID)`), correctly attributing distinct challenge states and provenance to multiple audio tracks sharing the same itag (e.g., `Format 140 (en.4)` vs `Format 140 (es.4)`).
- Safe transient attribution across URL refresh and persistent-job resume, generating fresh result-scoped provenance during re-extraction without persisting transient diagnostic terms to job manifests.
- Transient CLI diagnostics on standard error for selected download formats (e.g. `Format 18: signature=not_required, n=not_required` or `Format 140 (en.4): signature=resolved(runtime), n=not_required`), ensuring solver execution is never inferred from `-js-runtime` flag configuration alone, preserving stdout/JSON inspection outputs, and omitting transient diagnostics from persistent job manifests.
- Maintained bundle generation and reproducibility check script (`scripts/generate_bundle.sh --check`), dependency lockfile (`scripts/bundle_generator/package-lock.json`), and automated digest integrity test (`TestSolverBundle_EmbeddedDigestVerification`) verifying byte-for-byte reproducibility of embedded solver bundle (`bundle.js`, SHA-256 `ca150f7905cca3c13275b1e15e5073ef50278e96ed153372e5826973574ba483`).
- Consolidated third-party licensing and copyright notices file (`THIRD_PARTY_NOTICES.md`) included in release archives alongside binaries, with separate player fixture provenance documentation and redistribution assessments.
- Enhanced runtime resolution in `ResolveRuntime` prioritizing Deno capability sandboxing in `auto` discovery order, supporting explicit type prefixes (e.g. `deno:/custom/path`), and strictly rejecting ambiguous or unrecognized executable types without silent fallbacks.
- Provisioned pinned Node.js (`20.18.0`) and Deno (`v2.0.6`) runtimes across CI and release workflows, enforcing automated behavioral sandbox isolation tests for Deno and explicit execution tests for Node.js backends.
- Comprehensive offline test suite for challenge diagnostics covering unchallenged formats with solver configured, runtime resolution, cache resolution, mixed runtime/cache resolution, signature-only and n-only challenges, deduplication propagation across dependent formats, failed/invalid results remaining unresolved, generic solver unknown provenance, multi-track audio tracks with identical itags, concurrent extractions with result isolation (30 concurrent routines), URL refresh fresh provenance, CLI stderr diagnostics matching selected representations, and non-disclosure of sensitive query tokens, deciphered values, signed URLs, and player script sources.

### Fixed

- Handled Oracle Ubuntu and cloud host bot-check responses descriptively, explaining that the selected client received a bot-check challenge without claiming to bypass or fix that response, promising automatic fallback, or advising infinite retries.

## [0.1.0-rc.7] - 2026-09-27

### Added

- Explicit video codec selection (`-video-codec h264|vp9|av1`, default `h264`) and output container selection (`-container mp4|webm|mkv`, default `mp4`) in `goyt download`.
- Centralized codec and container compatibility policy shared across CLI validation, format planning, FFmpeg processing, and output verification:
  - `mp4`: H.264 or AV1 video with AAC audio.
  - `webm`: VP9 or AV1 video with Opus audio.
  - `mkv`: H.264, VP9, or AV1 video with Opus or AAC audio.
- Strict codec selection without silent fallback: requests for unavailable video codecs fail with clear, actionable errors.
- Automatic container inference from output file extension (`-out`) when `-container` is omitted, with strict validation requiring `-container` and `-out` extensions to agree when both are explicit.
- Multi-container output support (`mp4` and `mkv`) for supported H.264/AAC HLS video downloads, with early rejection of VP9/AV1 and WebM HLS requests clarifying current HLS implementation scope.
- Stream copy remuxing (`-c copy`) exclusively for all video downloads without video transcoding.
- Deterministic audio selection for MKV prioritizing original and default audio tracks, ranking Opus over AAC, and restricting bitrate comparisons to identical audio codecs.
- Generalized video verification (`VerifyVideo` / `VideoVerificationSpec`) supporting all 10 valid combinations with container alias matching, codec family verification, stream counts, dimensions, and duration tolerances.
- Backward-compatible persistent job support for WebM and MKV containers, preserving selected video codec, output container, and exact stream identities across resumes.
- Completed-job recognition supporting MP4, WebM, and MKV outputs.
- Comprehensive unit and integration test suites covering all supported codec/container combinations, container inference, invalid/conflicting flag rejections, MKV audio ranking, full decode checks, and persistent resume for WebM and legacy manifests.

### Fixed

- Fixed codec normalization in generalized video verification (`VerifyVideo`, `isMatchingAudioCodec`, and `isMatchingVideoCodec`) to normalize both expected source codec identifiers (e.g. `mp4a.40.2`, `av01.0.05m.08`, `avc1.640028`, `vp09.00.41.08`) and observed `ffprobe` codec names to their canonical families (`aac`, `av1`, `h264`, `vp9`) before comparison, resolving verification errors on AV1/H.264/VP9 downloads with source audio identifiers while preserving exact codec strings for pinned representation matching and URL refresh.

- Eliminated duplicate initial extraction on new persistent jobs (`-job-dir`) for both HLS and HTTP transports by passing the freshly resolved in-memory presentation directly into initial job execution, preventing redundant network extraction requests and avoiding immediate spurious generation bumps to `gen-2` before downloading begins.
- Fixed persistent job resume instructions to print directly runnable shell commands formatting the resolved executable binary path (via `os.Executable()`, symlink resolution, and argument quoting) and absolute job directory path safely across POSIX and Windows shells, including paths containing spaces and quotes.
- Clarified HLS presentation restart diagnostics on standard error to explicitly report that previously completed segments cannot be reused because remote byte compatibility could not be established across changed signed URLs.
- Documented persistent HLS job behavior and limitations: in-memory reuse on initial execution, reuse within an unchanged generation when compatibility checks pass, safe full restart when signed URLs or playlist structure change, offline processing when all inputs are completed, and verified completed-output recognition.

## [0.1.0-rc.6] - 2026-09-27

### Added

- Persistent download jobs in `goyt download` extended to supported YouTube HLS downloads for video and audio-only modes (`-transport hls -job-dir DIR`).
- Backward-compatible schema extension to version-1 job manifests storing HLS variant identities, audio renditions, track roles, target durations, media sequence numbers, and ordered segment states with SHA-256 URL fingerprints, local paths, sizes, and file hashes.
- Non-secret segment URL fingerprinting (`sha256(resolvedURL)`) proving exact URL identity against saved checkpointed playlists without persisting signed URLs, request headers, cookies, or tokens.
- Conservative HLS segment reuse requiring matching pinned variant/audio rendition identities, matching ordered playlist structure, matching segment URL fingerprints, and passing local size/SHA-256 verification.
- Presentation restart in an isolated new generation (`gen-1` -> `gen-2`) upon refreshed signed URL or playlist changes, preventing mixing of segment generations and reporting restart reasons to standard error.
- Offline local processing and verification for completed HLS inputs: when all segments across all tracks are verified, local `.m3u8` playlists are generated from metadata and processed without network extraction.
- Atomic segment completion checkpointing with bounded manifest updates and deterministic job-owned paths separated by generation and track.
- Intermediate cleanup of `hls/` segment directories upon successful destination commit.
- Integration tests with real FFmpeg/ffprobe covering interrupted combined MPEG-TS HLS jobs, separate video plus packed AAC audio HLS jobs, audio-only HLS jobs, generation restarts on changed tokens, tampered segment detection, and offline execution.
- Persistent HTTP download jobs in `goyt download` for video and audio-only downloads via `-job-dir DIR` (fails if directory exists).
- Persistent job resumption in `goyt download` via `-resume-job DIR`, restoring source URL, selection, audio settings, decode check choices, and output paths from the job manifest.
- Rejection of source, selection, audio, and output flag overrides on `-resume-job` (`-url`, `-out`, `-height`, `-transport`, `-decode-check`, `-audio-only`, `-audio-format`, `-audio-quality`, `-audio-bitrate`, `-audio-language`) with clear errors.
- Support for operational parameter overrides on `-resume-job` (`-timeout`, `-stall-timeout`, `-url-refreshes`), granting fresh overall deadlines and fresh URL refresh budgets per invocation.
- Versioned JSON job manifest (`schema_version: 1`) with atomic file writes and restricted file permissions (`0600`), persisting job ID, canonical source URL, video ID, absolute destination, transport mode, stream identities, audio settings, stages, relative file paths, byte sizes, and SHA-256 hashes.
- OS-backed kernel-held non-blocking exclusive file lock (`flock` on Unix, `LockFileEx` on Windows) on `job.lock` held for the duration of the job invocation and automatically released on process exit or crash.
- Local SHA-256 integrity verification and reuse of completed input streams without network access; fully completed inputs proceed to merge/remux/conversion and verification offline without extraction requests.
- Pinned representation re-extraction for incomplete streams (`MatchRefreshedFormat`) matching original format itag, container, codecs, dimensions, and audio track metadata without re-running format selection heuristics.
- Safe partial stream resumption via strong ETag / Content-Range checks, safely restarting from byte 0 when media URLs refresh.
- Pre-commit state recording (final SHA-256 and byte size), destination-filesystem staging, and crash-window recovery across destination renames.
- Automatic cleanup of intermediate input and partial files upon successful destination commit, retaining a lightweight manifest for idempotent resume.
- Idempotent resumption of completed jobs verifying final output identity (SHA-256 and size) without network requests, returning `ErrCompletedOutputMismatch` if the output was removed or modified.
- Untrusted state validation (`ValidateJobSubpath`) rejecting directory traversal, absolute paths, and symlinks in job manifests.
- Exported typed errors: `ErrJobLocked`, `ErrJobExists`, `ErrJobNotFound`, `ErrInvalidManifest`, `ErrUnsupportedManifestVersion`, `ErrSelectionUnavailable`, `ErrInputIntegrityMismatch`, `ErrCompletedOutputMismatch`, and `ErrInvalidJobPath`.
- Recovery diagnostics for retry and restart events reporting whether a transfer resumes or restarts, the failure reason, prior downloaded byte offset, and action rationale without logging sensitive URLs or secrets.
- An interrupted HTTP transfer may restart the affected stream from zero when safe byte-range resumption cannot be established, such as when a strong ETag is unavailable or the server ignores the range request. This can increase download time and bandwidth usage. Recovery diagnostics report whether a transfer resumes or restarts.

## [0.1.0-rc.5] - 2026-09-27

### Added

- Configurable overall job timeout in `goyt download` and `goyt hls` via `-timeout` (default `30m`, `0` disables).
- Network inactivity (stall) detection for media transfers in `goyt download` and `goyt hls` via `-stall-timeout` (default `60s`, `0` disables).
- Additive `StallTimeout` duration option in `DownloadOptions` for library callers (`0` disables, negative values rejected).
- Stable exported error `ErrDownloadStalled` compatible with `errors.Is`.
- Request-scoped stall watchdog interrupting stalled response headers or stalled body byte transfers (`n > 0` resets inactivity accounting; slow progressive transfers are not treated as stalled).
- Inactivity retries bounded within `MaxRetries` budget reusing cancellation-aware backoff and strong-ETag partial resumption (or clean restarts on changed/ignored ranges).
- Strict priority for parent cancellation (`context.Canceled`) and parent deadlines (`context.DeadlineExceeded`) over stall classification.
- Machine-readable JSON output for `goyt inspect` via the `-json` flag (`goyt inspect -url URL [-client web|visionos|all] -json`).
- Versioned inspect output schema (`schema_version: 1`) with explicit snake_case DTOs for safe, stable downstream consumption.
- Normalized duration in seconds (`duration_seconds`), sorted unique video heights (`available_video_heights`), and honest original-audio heuristics (`original_hint`).
- Strict allowlist filtering ensuring signed media/manifest URLs, cookies, authorization headers, visitor identifiers, and raw API responses are excluded from JSON output.
- Structured per-client error reporting with safe error codes (`context_canceled`, `timeout`, `player_response_missing`, `extraction_failed`), preserving partial successful results under `-client all`.
- Bounded URL refresh in `goyt download` via `-url-refreshes` (default `1`, `0` disables, negative values rejected) across direct HTTP and HLS transports for video and audio-only downloads.
- Single job-scoped refresh budget across all streams, segments, and formats; every re-extraction attempt (including failures) charges the budget.
- Recovery probe triggers on known expiration (`ErrResourceExpired` / `Resource.ExpiresAt` passed) and media access failures (HTTP 403 Forbidden / HTTP 410 Gone) with stderr notices.
- Retained `HTTPStatusError` across all HLS master/media playlist requests so HTTP 403/410 failures qualify for bounded recovery.
- Strict stream and variant identity preservation across refreshes: matches video ID, format itag, audio track ID, language, original/default markers, container, codecs, and dimensions for direct HTTP; preserves and matches `FRAME-RATE` and `VIDEO-RANGE` for HLS variants, rejecting missing metadata and unresolved ambiguity without using bandwidth to select across different frame rates or dynamic ranges.
- Scoped HLS audio rendition matching: binds refreshed audio renditions strictly within the audio group linked to the matched refreshed variant, permitting manifest group ID changes while rejecting global generic "Default" matches.
- Strict cancellation and deadline precedence (`context.Canceled`, `context.DeadlineExceeded`) over refresh triggers, exiting immediately without re-extraction.
- Safe restart isolation: HTTP transfers restart from byte 0 without appending old partial bytes when URLs change; HLS transfers restart in fresh attempt directories without mixing segment generations.
- Staged output verification and destination preservation ensuring existing files are untouched until verification and decode checks succeed.

## [0.1.0-rc.4] - 2026-09-27

### Added

- Individual YouTube Music track URLs (`https://music.youtube.com/watch?v=VIDEO_ID`) in `goyt download` and `goyt inspect`.
- Tracking parameters are ignored; watch links with playlist parameters process only the specified video.
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

### Changed

- Consolidated `goyt`, `goyt-inspect`, and `goyt-hls` binaries into a single unified `goyt` executable.
- Moved CLI command handlers and flag parsing into `internal/cli`, keeping `cmd/goyt/main.go` as a thin entry point.
- Updated build targets, CI workflow, release packaging, and documentation for the unified binary.
- Updated release archives to contain strictly `goyt` (`goyt.exe` on Windows), `README.md`, and `LICENSE`.
- Allowed optional subtitle references during audio/video-only HLS selection.

### Scope / Known Limitations

- YouTube support covers a subset of public, non-live videos.
- YouTube Music album, playlist-only, and browse URLs remain unsupported (individual track watch URLs only).
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
- Stalled-transfer recovery and HTTP range resumes operate within a single job execution up to the retry limit; failed jobs retain intermediate work directories but cannot automatically resume as whole jobs across CLI restarts without restarting the command.
- Successful decoding does not verify language or perceptual synchronization.