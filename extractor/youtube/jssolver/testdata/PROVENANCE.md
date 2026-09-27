# Test Fixture Provenance & Licenses

This directory contains static recorded YouTube player JavaScript fixtures and documentation for offline unit testing of `goyt`'s JavaScript challenge solver.

## 1. Fixture Inventory & Provenance

| Fixture Filename | Canonical Source URL | Date Recorded | SHA-256 Digest | Description & Redistribution Basis |
|---|---|---|---|---|
| `player_7460dd14_en_GB.js` | `https://www.youtube.com/s/player/7460dd14/player_ias.vflset/en_GB/base.js` | 2026-09-27 | `9d8569e77a8997318d9db3151a96a32e5be62617935ede85c7d8804b078b3461` | Recorded public YouTube web player script (GB locale). Retained for testing AST analysis against production Closure Compiler code. |
| `player_7460dd14_en_US.js` | `https://www.youtube.com/s/player/7460dd14/player_ias.vflset/en_US/base.js` | 2026-09-27 | `2f27f4ad19035d713a6aa69c27ee2b03b35d46af6485aff0eed8c12104a8d38d` | Recorded public YouTube web player script (US locale). Retained to test hash and script identity isolation between distinct source files sharing base version identifiers. |

> [!NOTE]
> `player_7460dd14_en_GB.js` and `player_7460dd14_en_US.js` represent different script identities (distinct source digests and URLs) originating from the same base player release version `7460dd14`. They share identical transformation algorithms while having minor localization differences.

## 2. Independent Reference Test Vectors

The following known expected transformation outputs were verified using independent reference execution (manual tracing and reference AST evaluation):

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

## 3. Bundled Dependencies & License Notices

The embedded solver bundle in `extractor/youtube/jssolver/bundle.js` integrates the following open-source libraries:

### `yt-dlp-ejs` (Version 0.8.0)
- **Upstream Repository**: https://github.com/yt-dlp/ejs
- **Pinned Release**: `0.8.0` (commit `3b6b19a`)
- **License**: The Unlicense (Public Domain)
```text
This is free and unencumbered software released into the public domain.

Anyone is free to copy, modify, publish, use, compile, sell, or
distribute this software, either in source code form or as a compiled
binary, for any purpose, commercial or non-commercial, and by any
means.
```

### `meriyah` (Version 6.1.4)
- **Upstream Repository**: https://github.com/meriyah/meriyah
- **Pinned Release**: `v6.1.4` (commit `69e9e8c`)
- **License**: ISC License
```text
ISC License

Copyright (c) Kenny F.

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.
```

### `astring` (Version 1.9.0)
- **Upstream Repository**: https://github.com/davidbonnet/astring
- **Pinned Release**: `v1.9.0` (commit `fca3508`)
- **License**: MIT License
```text
MIT License

Copyright (c) 2016 David Bonnet

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.
```

## 4. Bundle Reproduction Procedure

To regenerate or verify `bundle.js` from upstream pinned sources:
1. Bundle core parser (`meriyah` 6.1.4) and code generator (`astring` 1.9.0) into a single CommonJS module using standard esbuild / rollup without minification of solver interfaces.
2. Integrate `yt-dlp-ejs` solver AST walker (`solveSignature` and `solveNcode`).
3. Add versioned JSON IPC dispatcher over `process.stdin` / `process.stdout`.
4. Embed output directly into `extractor/youtube/jssolver/bundle.js`.
