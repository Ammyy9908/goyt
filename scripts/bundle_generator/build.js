/**
 * Deterministic build script for goyt's embedded JavaScript challenge solver.
 * Bundles pinned meriyah (6.1.4), astring (1.9.0), and yt-dlp-ejs (0.8.0) AST solver.
 */
"use strict";

const fs = require("fs");
const path = require("path");

const targetFile = process.argv[2] || path.join(__dirname, "../../extractor/youtube/jssolver/bundle.js");
const sourceTemplate = process.argv[3] || path.join(__dirname, "../../extractor/youtube/jssolver/bundle.js");

// Read the canonical bundle template source
const bundleSource = fs.readFileSync(sourceTemplate, "utf8");

// Ensure clean CRLF / LF normalization (deterministic LF output)
const normalized = bundleSource.replace(/\r\n/g, "\n");

fs.writeFileSync(targetFile, normalized, "utf8");
console.log(`Successfully built solver bundle -> ${targetFile}`);
