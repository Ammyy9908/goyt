# Phased development plan

Only Phase 0 is scaffolded. Later phases are plans, not implemented features.

## Phase 0 — Project foundation

- [x] Go module and root package.
- [x] CLI help and development version.
- [x] Build, format, vet, and test commands.
- [x] Project documentation and ignore rules.
- [ ] Run Go validation on a machine with Go installed.

No media behavior belongs in this phase.

## Phase 1 — Models and extraction boundary

Define Media, Format, Resource, and extraction error types. Distinguish unknown
metadata from zero. Define an Extractor interface with context cancellation and
an explicit registry. Add one synthetic extractor fixture to validate routing.

Done when a synthetic URL resolves into typed metadata, unknown URLs return a
structured error, and canceled operations return promptly. No actual downloads.

## Phase 2 — Direct HTTP transfer

Add a downloader with an injected HTTP client, streamed file writes, progress,
temporary files, cancellation, and bounded retries. Implement resume only with
range and resource-identity validation; safely restart if the server ignores a
range request. Keep credentials scoped to the appropriate destination.

Done when local HTTP fixtures cover successful transfer, interruption, valid
resume, ignored range requests, and changed resources without corrupting output.

## Phase 3 — Format selection and planning

Add typed preferences for resolution, codecs, language, and container. Produce an
inspectable plan before executing it. Separate video/audio selection from merging
and transcoding. Avoid a custom selection expression language initially.

Done when deterministic fixtures demonstrate selection, fallback, and explicit
failure for incompatible requirements.

## Phase 4 — HLS VOD and merging

Parse a deliberately documented subset of HLS, download segments with bounded
concurrency, preserve order, and handle initialization segments. Reject unsupported
features explicitly. Wrap FFmpeg as an optional dependency for stream merging,
using argument arrays and context cancellation.

Done when controlled fixtures produce playable output and missing segments or
missing FFmpeg produce useful errors. Live streaming and DRM remain outside scope.

## Phase 5 — YouTube adapter

Prototype public video metadata and format extraction. Keep player requests,
JavaScript challenge solving, token providers, and session state replaceable.
Add resource refresh for expired URLs. Define a narrow supported capability set
before expanding coverage. If YouTube is required for launch, run a feasibility
spike alongside Phase 1 rather than waiting until this phase.

Done when the declared public-video capability set passes recorded-response tests
and opt-in live smoke checks. Do not promise universal YouTube support.

## Phase 6 — Expansion and release

Add DASH VOD, lazy playlists, subtitles, and additional sites incrementally.
Document the public API, concurrency guarantees, errors, and dependency setup.
Add CI and release builds once the repository location is known. Decide on a
license before publication and review licenses of any code or assets reused.

Done when offline regression checks pass and the supported feature matrix is
documented. Keep live checks separate from deterministic unit tests.

## Architectural direction

The intended flow is Extract → Plan → Download. Introduce public signatures when
their phase is implemented; the scaffold deliberately does not freeze speculative
interfaces. The library returns data and errors; the CLI owns terminal output.
Use context.Context, injected dependencies, bounded resources, and structured
errors throughout implemented operations.
