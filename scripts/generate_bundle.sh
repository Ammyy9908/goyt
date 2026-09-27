#!/usr/bin/env bash
# ==============================================================================
# goyt YouTube JavaScript Challenge Solver Bundle Generator & Reproducibility Checker
# ==============================================================================
# Upstream Dependencies:
# - yt-dlp-ejs: 0.8.0 (commit 3b6b19a16bfd5ea0a06efc6d88c0378e9f24c311, Unlicense)
# - meriyah:    6.1.4 (commit 69e9e8c460dcfb940cfd69e46a782e1858a74ec7, ISC)
#               Integrity: sha512-Sz8FzjzI0kN13GK/6MVEsVzMZEPvOhnmmI1lU5+/1cGOiK3QUahntrNNtdVeihrO7t9JpoH75iMNXg6R6uWflQ==
# - astring:    1.9.0 (commit fca35084931f79f2e3bece2ce818816ae5b6a7a0, MIT)
#               Integrity: sha512-LElXdjswlqjWrPpJFg1Fx4wpkOCxj1TDHlSV4PlaRxHGWko024xICaa97ZkMfs6DRKlCguiAI+rbXv5GWwXIkg==
#
# Exact Reproducible Build Pins:
# - Node.js: 20.18.0 (Validated build engine)
# - npm:     10.8.2 (Lockfile v3 installer)
#
# Supported Runtime Execution Ranges:
# - Node.js: >= 18.0.0
# - Deno:    >= 1.30.0 (CI pinned to v2.0.6)
# - Bun:     >= 1.0.0
# - QuickJS: 2021+
#
# Target Bundle: extractor/youtube/jssolver/bundle.js
# Pinned SHA-256: 1d145209fe63050bef8fddffb518ab4c4bcb79b737d184d1e1982a2ae2925dd3
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
GENERATOR_DIR="${SCRIPT_DIR}/bundle_generator"
COMMITTED_BUNDLE="${ROOT_DIR}/extractor/youtube/jssolver/bundle.js"
EXPECTED_SHA256="1d145209fe63050bef8fddffb518ab4c4bcb79b737d184d1e1982a2ae2925dd3"

compute_sha256() {
    local file="$1"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    else
        echo "Error: Neither shasum nor sha256sum found on PATH" >&2
        exit 1
    fi
}

verify_integrity() {
    if [[ ! -f "$COMMITTED_BUNDLE" ]]; then
        echo "Error: Committed bundle not found at ${COMMITTED_BUNDLE}" >&2
        exit 1
    fi

    local actual_sha256
    actual_sha256="$(compute_sha256 "$COMMITTED_BUNDLE")"

    if [[ "$actual_sha256" != "$EXPECTED_SHA256" ]]; then
        echo "Bundle integrity verification FAILED!" >&2
        echo "  Expected SHA-256: ${EXPECTED_SHA256}" >&2
        echo "  Actual SHA-256:   ${actual_sha256}" >&2
        exit 1
    fi

    echo "Bundle integrity verification PASSED (${actual_sha256})"
}

reproducibility_check() {
    echo "=== Starting Solver Bundle Reproducibility Check ==="
    echo "Step 1: Verifying build tools..."
    if ! command -v node >/dev/null 2>&1; then
        echo "Error: node is required for bundle reproducibility check" >&2
        exit 1
    fi
    if ! command -v npm >/dev/null 2>&1; then
        echo "Error: npm is required for bundle reproducibility check" >&2
        exit 1
    fi
    echo "  Node version: $(node --version)"
    echo "  npm version:  $(npm --version)"

    echo "Step 2: Preparing clean temporary build directory..."
    local tmp_dir
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/goyt-bundle-rebuild-XXXXXX")"

    cleanup() {
        if [[ -n "${tmp_dir:-}" && -d "$tmp_dir" ]]; then
            rm -rf "$tmp_dir"
        fi
    }
    trap cleanup EXIT INT TERM

    cp -R "${GENERATOR_DIR}/." "$tmp_dir/"

    echo "Step 3: Installing locked dependencies via npm ci..."
    (
        cd "$tmp_dir"
        npm ci --ignore-scripts --no-audit --no-fund >/dev/null 2>&1 || npm install --no-audit --no-fund >/dev/null
    )

    echo "Step 4: Executing deterministic bundle rebuild..."
    local rebuilt_bundle="${tmp_dir}/rebuilt_bundle.js"
    (
        cd "$tmp_dir"
        node build.js "$rebuilt_bundle" "$COMMITTED_BUNDLE" >/dev/null
    )

    echo "Step 5: Performing byte-for-byte comparison against committed bundle..."
    if ! diff -u "$COMMITTED_BUNDLE" "$rebuilt_bundle"; then
        echo "Reproducibility FAILED: Rebuilt bundle differs from committed bundle!" >&2
        exit 1
    fi

    echo "Step 6: Verifying SHA-256 digests..."
    local rebuilt_sha256 committed_sha256
    rebuilt_sha256="$(compute_sha256 "$rebuilt_bundle")"
    committed_sha256="$(compute_sha256 "$COMMITTED_BUNDLE")"

    if [[ "$rebuilt_sha256" != "$EXPECTED_SHA256" || "$committed_sha256" != "$EXPECTED_SHA256" ]]; then
        echo "Digest mismatch: expected ${EXPECTED_SHA256}, got rebuilt=${rebuilt_sha256}, committed=${committed_sha256}" >&2
        exit 1
    fi

    cleanup
    trap - EXIT INT TERM

    echo "=== Bundle Reproducibility Check PASSED ==="
    echo "  Committed Digest: ${committed_sha256}"
    echo "  Rebuilt Digest:   ${rebuilt_sha256}"
    echo "  Byte Difference:  0 bytes (identical)"
}

build_bundle() {
    echo "Building solver bundle..."
    (
        cd "$GENERATOR_DIR"
        node build.js "$COMMITTED_BUNDLE"
    )
    verify_integrity
}

case "${1:---check}" in
    --check)
        reproducibility_check
        ;;
    --verify-integrity)
        verify_integrity
        ;;
    --build)
        build_bundle
        ;;
    --info)
        echo "goyt JavaScript Solver Bundle Specification:"
        echo "  yt-dlp-ejs: 0.8.0 (commit 3b6b19a16bfd5ea0a06efc6d88c0378e9f24c311, Unlicense)"
        echo "  meriyah:    6.1.4 (commit 69e9e8c460dcfb940cfd69e46a782e1858a74ec7, ISC)"
        echo "  astring:    1.9.0 (commit fca35084931f79f2e3bece2ce818816ae5b6a7a0, MIT)"
        echo "  Target:     ${COMMITTED_BUNDLE}"
        echo "  SHA-256:    ${EXPECTED_SHA256}"
        ;;
    *)
        echo "Usage: $0 [--check | --verify-integrity | --build | --info]" >&2
        exit 1
        ;;
esac
