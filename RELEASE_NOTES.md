fcurl v0.2.1 fixes two single-request compatibility issues.

- --data-urlencode now gives '=' precedence over '@': a@b=c is sent as a field instead of opening b=c as a file.
- Netscape Cookie persistence preserves the precedence of equal-path, same-name host/domain cookies. Updates retain creation order; deletion and recreation assign a new position. Files interoperate with curl in both directions, including duplicate/expired records.

Historical creation order already lost by v0.2.0's alphabetical Cookie serialization cannot be reconstructed from those old files.

Download the archive for Linux amd64/arm64, macOS amd64/arm64 or Windows amd64, verify SHA256SUMS, unpack and run `fcurl --version`. Source installation: `go install github.com/FastTLS/fastls-curl/cmd/fcurl@v0.2.1` (Go 1.26+).

Validation: CI run 41317439501 at c7b83c0cb828c540519d93ff3ac7ca2d729872b4 passed five-platform regressions and installed archive checks, Linux race and a five-minute Cookie import/receive/atomic-save resource check (5,797 requests; retained heap 2,171,208 -> 2,268,488 bytes; goroutines 3 -> 3). Release preparation changes only version, documentation and evidence metadata; exact release candidates receive five-platform identity and installation checks. This is not full throughput or hour-long stability certification.

Release archives are promoted from the exact installed CI candidates. BUILD.json, RELEASE.json and SOURCE_VERIFICATION.json link source, toolchain, dependency digests and runtime evidence. Existing request limits and planned features remain documented in COMPATIBILITY.md and DEVELOPMENT_V0.2.md.
