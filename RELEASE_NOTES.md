fcurl v0.1.0 is the first standalone HTTP CLI powered by FastTLS/fastls.

- Common curl-style HTTP request, header, JSON, output and failure options.
- Explicit browser profiles, offline Spec files and custom JA3/TCP JA4R.
- HTTP/HTTPS CONNECT and SOCKS5/SOCKS5H proxies, separate origin/proxy CA roots.
- Manual HTTP redirects with credential stripping, bounded request buffering and streaming downloads.
- Five native platforms: Linux amd64/arm64, macOS amd64/arm64 and Windows amd64.

Download your platform archive, verify it with SHA256SUMS, unpack and run `fcurl --version`. Source installation: `go install github.com/FastTLS/fastls-curl/cmd/fcurl@v0.1.0` (Go 1.26+).

Scope: one URL per invocation, request bodies buffered up to 32 MiB, no HTTP/3, multipart, cookie files or parallel batches yet. HTTP proxies must allow CONNECT even for HTTP targets. See COMPATIBILITY.md for exact behavior and known curl version differences; INVOCATION_PLAN.md describes future features, not this release.

Release archives are promoted from the exact installed CI candidates. BUILD.json, RELEASE.json and SOURCE_VERIFICATION.json link source, toolchain, dependency digests and runtime-validation evidence. No full throughput or hour-long stability certification is claimed.
