# Third-party software / 第三方软件

This is an unofficial, community-developed portability wrapper. It is not endorsed by DeepSeek. Product names and marks belong to their respective owners.

## Included

- **7-Zip 26.00 x64** — `tools/7z.exe`, `tools/7z.dll`. Copyright Igor Pavlov. LGPL with the unRAR restriction and other notices as detailed in `tools/License.txt`; original `tools/readme.txt` retained. Binaries extracted without modification from https://www.7-zip.org/a/7z2600-x64.exe . Corresponding source: https://www.7-zip.org/a/7z2600-src.7z ; project: https://www.7-zip.org/ . This launcher only uses 7z/NSIS extraction, not RAR compression.
- **Go 1.26.1 standard library/runtime** — compiled into the launcher; BSD-style license, `docs/licenses/Go-LICENSE.txt`. Source: https://go.dev/dl/go1.26.1.src.tar.gz .
- **golang.org/x/sys v0.42.0** — BSD-style, `docs/licenses/x-sys-BSD.txt`.
- **golang.org/x/net v0.51.0** — BSD-style, `docs/licenses/x-net-BSD.txt`.
- **golang.org/x/text v0.34.0** — BSD-style, `docs/licenses/x-text-BSD.txt`.
- **gopkg.in/yaml.v3 v3.0.1** — MIT and Apache notices, `docs/licenses/yaml-v3-LICENSE.txt`.

Go module versions and checksums are recorded in `source/go.mod` and `source/go.sum`. This package includes the launcher's full source; upstream dependency sources are available through their published modules/repositories.

Resource generation uses `github.com/tc-hib/go-winres v0.3.3` as a build tool. Generated icon artwork in this wrapper is community artwork under MIT, not the official DeepSeek logo. Its resource configuration and PNG sources are included.

## Downloaded on first run, not bundled in this ZIP

- **DeepSeek Harness Desktop** — https://github.com/deepseek-ai/deepseek-harness ; MIT project with its own third-party notices.
- Its bundled **Electron / Chromium / Node.js / plugins** retain the licenses shipped inside the extracted official application. Preserve those files when copying or redistributing a prepared runtime.

The launcher verifies the downloaded original payload against the SHA-512 obtained from the same official HTTPS update feed. This detects corruption and mismatching files; it is not independent publisher attestation against compromise of that feed. It then modifies the ASAR entry metadata, adds portability modules, and applies bounded Desktop update and Host proxy-entry adapters. The resulting runtime is a modified community package, not a byte-identical official release.

Version 1.0.3 additionally limits wrapper-owned Node fetch transports to HTTP/1.1 to avoid the reproduced Undici 8 legacy-dispatcher HTTP/2 header mismatch. It does not modify Undici source or disable TLS verification. Explicit caller-provided dispatchers remain untouched.

Version 1.0.4 can migrate known 1.0.2/1.0.3 portable runtimes locally when the official version and source-payload SHA-512 match the update feed. It copies and validates the existing runtime, replaces portability modules in the staged ASAR, preserves the old runtime, and publishes a new runtime pointer. Retained local integrity records are corruption checks, not independent publisher authentication. Byte-exact legacy wrapper modules under `source/legacy/` are community MIT code used for compatibility validation, not executable fallback behavior.
