// Package goyt provides media extraction, format planning, download management,
// and post-processing tools for online video resources.
//
// Key components include:
//   - Client and Extractor registry for discovering media representations.
//   - Planner and Selection rules for choosing compatible audio/video streams.
//   - Downloader for resilient HTTP transfers with retry and strong-ETag resumption.
//   - Executor for orchestrating downloads and invoking stream merge/remux operations.
//   - HLSDownloader for downloading completed media playlists and master variants,
//     supporting both MPEG-TS and ID3-prefixed packed AAC audio segments.
//   - FFmpeg and Verifier for stream processing and ffprobe metadata verification.
package goyt
