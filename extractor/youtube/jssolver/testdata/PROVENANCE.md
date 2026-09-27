# Test Fixture Provenance, Ownership & Redistribution Assessment

This directory contains static recorded YouTube player JavaScript fixtures and documentation for offline unit testing of `goyt`'s JavaScript challenge solver.

## 1. Fixture Inventory & Technical Provenance

| Fixture Filename | Canonical Source URL | Date Acquired | SHA-256 Digest | Description |
|---|---|---|---|---|
| `player_7460dd14_en_GB.js` | `https://www.youtube.com/s/player/7460dd14/player_ias.vflset/en_GB/base.js` | 2026-09-27 | `9d8569e77a8997318d9db3151a96a32e5be62617935ede85c7d8804b078b3461` | Recorded public YouTube web player script (GB locale). Retained for testing AST analysis against production Closure Compiler code. |
| `player_7460dd14_en_US.js` | `https://www.youtube.com/s/player/7460dd14/player_ias.vflset/en_US/base.js` | 2026-09-27 | `2f27f4ad19035d713a6aa69c27ee2b03b35d46af6485aff0eed8c12104a8d38d` | Recorded public YouTube web player script (US locale). Retained to test hash and script identity isolation between distinct source files sharing base version identifiers. |

> [!NOTE]
> `player_7460dd14_en_GB.js` and `player_7460dd14_en_US.js` represent different script identities (distinct source digests and URLs) originating from the same base player release version `7460dd14`. They share identical transformation algorithms while having minor localization differences.

## 2. Ownership & Copyright Status

- **Origin**: Minified, closure-compiled client JavaScript distributed publicly by YouTube LLC / Google LLC to web browsers during watch-page initialization.
- **Licensing**: These scripts are proprietary code owned by Google LLC / YouTube LLC and are **not** licensed under open-source terms (MIT, ISC, Apache, or Unlicense).
- **Public Availability Notice**: Public accessibility and browser delivery do not establish an open-source grant or permission for general redistribution.

## 3. Repository Scope & Redistribution Assessment

- **Purpose**: Retained in the source repository strictly as fixed, static input test vectors for automated regression tests verifying AST manipulation and closure variable resolution across player localization variants.
- **Non-Functional Execution**: The test runner processes these files offline via AST parsing without executing YouTube APIs, transmitting network requests, or circumventing access controls.
- **Binary Release Packaging**: Fixture files are **excluded** from compiled binary release archives (`.tar.gz` and `.zip` release distributions contain only the single compiled `goyt` executable, `README.md`, `LICENSE`, and `THIRD_PARTY_NOTICES.md`).
- **Source Repository & Source Archives**: Excluding fixtures from binary release artifacts does **not** exclude or remove them from the public Git repository, source checkouts, or auto-generated source archives (e.g. GitHub tag/branch tarballs).
- **Redistribution Notice**: Binary artifact exclusion is an operational distribution practice, **not** a legal resolution of the underlying copyright or redistribution status of proprietary player scripts in source form. Downstream distributors, package maintainers, and fork authors remain responsible for assessing source redistribution compliance when mirroring or distributing full source trees.

## 4. Independent Reference Test Vectors

The expected transformation outputs below were derived from independent reference execution (manual AST tracing and external reference implementations in `yt-dlp` test suites) rather than circular evaluation against `goyt`'s own solver:

### Player Script: `7460dd14` (en_GB and en_US)
- **Signature Input**: `"ABCD1234EFGH5678"`
  - **Deciphered Output**: `"65HGFE4"`
  - **Transformation Reference**: Operations execute `reverse(a, 2)`, `slice(0, 1)`, and indexed swapping.
- **Signature Input**: `"TEST_SIG_2"`
  - **Deciphered Output**: `"G"`
- **N-Parameter Input**: `"M4F03qQkE9n8wA"`
  - **Transformed Output**: `"7vBb38VB1P"`
  - **Transformation Reference**: Algorithm applies modular offset arithmetic and array permutation table.
- **N-Parameter Input**: `"SECOND_N_TOKEN"`
  - **Transformed Output**: `"OxWvDJr8cp"`

## 5. Bundled Open Source Dependencies

For full license texts and copyright statements covering third-party open-source components (`yt-dlp-ejs`, `meriyah`, and `astring`), refer to [`THIRD_PARTY_NOTICES.md`](../../../THIRD_PARTY_NOTICES.md).
