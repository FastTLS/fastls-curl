fcurl v0.2.0 expands single-request scripting on the FastTLS HTTP CLI.

- Query and form encoding with -G/--get and --data-urlencode; literal --data-raw.
- Header files (-H @file/@-), Basic/Bearer authentication and Referer.
- Netscape Cookie import and atomic Cookie jar export, including domain/path, Secure, HttpOnly and expiry rules.
- Strict stdin ownership, explicit authorization precedence and cross-origin credential stripping.
- Five native platforms: Linux amd64/arm64, macOS amd64/arm64 and Windows amd64.

Download your platform archive, verify it with SHA256SUMS, unpack and run `fcurl --version`. Source installation: `go install github.com/FastTLS/fastls-curl/cmd/fcurl@v0.2.0` (Go 1.26+).

Scope: one URL per invocation, request bodies buffered up to 32 MiB, streaming downloads. HTTP/3, multipart, streaming uploads, resume, retries, batches and metadata interfaces remain planned. HTTP proxies require CONNECT even for HTTP targets. See COMPATIBILITY.md and DEVELOPMENT_V0.2.md for exact behavior and limits.

Feature validation: CI run 41279793202 at b2414e052371c362afd58366603d2140094cfb06 passed five-platform tests, Linux race and a five-minute Cookie import/receive/atomic-save resource check. Version/source-only preparation reuses this evidence after input comparison; the exact release candidates receive five-platform identity and installation checks. No full throughput or hour-long stability certification is claimed.

Release archives are promoted from the exact installed CI candidates. BUILD.json, RELEASE.json and SOURCE_VERIFICATION.json link source, toolchain, dependency digests and runtime-validation evidence.
