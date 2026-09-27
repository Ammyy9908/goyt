# Third-Party Notices and Licenses

This file contains open source license and copyright notices for third-party software and components incorporated into `goyt`.

---

## 1. Bundled JavaScript Solver Components

The embedded JavaScript challenge solver (`extractor/youtube/jssolver/bundle.js`) incorporates code from the following third-party projects:

### 1.1 `yt-dlp-ejs` (Version 0.8.0)
- **Project**: https://github.com/yt-dlp/ejs
- **Version**: 0.8.0 (commit `3b6b19a`)
- **License**: The Unlicense (Public Domain)

```text
This is free and unencumbered software released into the public domain.

Anyone is free to copy, modify, publish, use, compile, sell, or
distribute this software, either in source code form or as a compiled
binary, for any purpose, commercial or non-commercial, and by any
means.

In jurisdictions that recognize copyright laws, the author or authors
of this software dedicate any and all copyright interest in the
software to the public domain. We make this dedication for the benefit
of the public at large and to the detriment of our heirs and
successors. We intend this dedication to be an overt act of
relinquishment in perpetuity of all present and future rights to this
software under copyright law.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS BE LIABLE FOR ANY CLAIM, DAMAGES OR
OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.

For more information, please refer to <http://unlicense.org/>
```

---

### 1.2 `meriyah` (Version 6.1.4)
- **Project**: https://github.com/meriyah/meriyah
- **Version**: 6.1.4 (commit `69e9e8c`)
- **License**: ISC License

```text
ISC License

Copyright (c) 2019 and later, KFlash and others.

Permission to use, copy, modify, and/or distribute this software for any purpose
with or without fee is hereby granted, provided that the above copyright notice
and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS
OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
```

---

### 1.3 `astring` (Version 1.9.0)
- **Project**: https://github.com/davidbonnet/astring
- **Version**: 1.9.0 (commit `fca3508`)
- **License**: MIT License

```text
MIT License

Copyright (c) 2015, David Bonnet <david@bonnet.cc>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## 2. Test Fixture Provenance & Redistribution Assessment

Static YouTube player JavaScript files located in `extractor/youtube/jssolver/testdata/` are used exclusively for offline automated testing of the AST challenge solver:

| Fixture File | Canonical Source URL | Date Acquired | SHA-256 Digest |
|---|---|---|---|
| `player_7460dd14_en_GB.js` | `https://www.youtube.com/s/player/7460dd14/player_ias.vflset/en_GB/base.js` | 2026-09-27 | `9d8569e77a8997318d9db3151a96a32e5be62617935ede85c7d8804b078b3461` |
| `player_7460dd14_en_US.js` | `https://www.youtube.com/s/player/7460dd14/player_ias.vflset/en_US/base.js` | 2026-09-27 | `2f27f4ad19035d713a6aa69c27ee2b03b35d46af6485aff0eed8c12104a8d38d` |

### 2.1 Ownership & Copyright Status
- **Origin**: Minified, closure-compiled client JavaScript distributed publicly by YouTube LLC / Google LLC to web browsers during watch-page initialization.
- **Licensing**: These scripts are proprietary code owned by Google LLC / YouTube LLC and are **not** licensed under open-source terms (MIT, ISC, Apache, or Unlicense).
- **Public Availability Notice**: Public transmission to web browsers does not constitute an open-source grant or permission for general redistribution.

### 2.2 Repository Scope & Redistribution Assessment
- **Purpose**: Retained in the source repository strictly as fixed, static input test vectors for automated regression tests verifying AST manipulation and closure variable resolution across player localization variants.
- **Non-Functional Data**: The test runner processes these files offline via AST parsing without executing YouTube APIs, transmitting network requests, or circumventing access controls.
- **Binary Release Packaging**: Fixture files are **excluded** from compiled binary release archives (`.tar.gz` and `.zip` distributions contain only the single compiled `goyt` executable, `README.md`, `LICENSE`, and `THIRD_PARTY_NOTICES.md`).
- **Source Repository & Source Archives**: Excluding fixtures from binary release artifacts does **not** exclude or remove them from the public Git repository, source checkouts, or auto-generated source archives (e.g. GitHub tag/branch tarballs).
- **Redistribution Notice**: Binary artifact exclusion is an operational distribution practice, **not** a legal resolution of the underlying copyright or redistribution status of proprietary player scripts in source form. Downstream distributors, package maintainers, and fork authors remain responsible for assessing source redistribution compliance when mirroring or distributing full source trees.

### 2.3 Independent Expected-Output Verification
- Expected signature deciphering outputs (`ABCD1234EFGH5678` -> `65HGFE4`, `TEST_SIG_2` -> `G`) and n-parameter transformations (`M4F03qQkE9n8wA` -> `7vBb38VB1P`, `SECOND_N_TOKEN` -> `OxWvDJr8cp`) were derived from independent algorithmic tracing and cross-referenced with external reference implementations (`yt-dlp` test suites), ensuring test assertions do not depend on circular verification against `goyt`'s own solver.
