# fastls-curl

基于 [FastTLS/fastls](https://github.com/FastTLS/fastls) 的 HTTP 命令行工具，命令名 **`fcurl`**。支持常用 curl 参数、TLS/HTTP 指纹、代理与流式下载。当前正式版本为 `v0.2.0`；可下载的正式产物以 [GitHub Releases](https://github.com/FastTLS/fastls-curl/releases) 为准。

## 安装与构建

需要 Go 1.26+；仓库固定工具链为 1.26.10，不需要 C 编译器或后台服务。

```sh
go build -trimpath -o dist/fcurl ./cmd/fcurl
./dist/fcurl --version
```

Windows 将输出文件改为 `dist/fcurl.exe`。也可从 GitHub Releases 下载对应平台压缩包与 SHA256SUMS，核对摘要后解包使用。安装位置、源码标签安装和 Actions 发布流程见 [RELEASE.md](RELEASE.md)。

v0.2.0 新增查询编码、原始数据、头文件、Basic/Bearer、Referer 与 Cookie 文件。示例和限制见 [v0.2.0 功能说明](DEVELOPMENT_V0.2.md)。源码安装：`go install github.com/FastTLS/fastls-curl/cmd/fcurl@v0.2.0`。

## 使用

```sh
fcurl https://example.com
fcurl --profile chrome/156/windows https://example.com
fcurl --profile chrome/156/windows --json '{"hello":"world"}' https://example.com/api
fcurl -H 'X-Tag: first' -H 'X-Tag: second' https://example.com
fcurl -x socks5h://127.0.0.1:1080 -L -o result.html https://example.com
fcurl --cacert private-ca.pem https://internal.example
fcurl -sS --fail-with-body --max-time 30 https://example.com
fcurl --list-profiles
fcurl --help
```

示例使用 POSIX shell 引号；Windows PowerShell 可执行相同的单行命令，复杂 JSON 也可用 `--json @request.json`。

默认验证证书、不跟随跳转、不读取环境代理、不隐式选择浏览器指纹。显式 `--profile` 复用固定版本 imitate 的本地生成器，不访问远程配置服务。生成身份不等于完整真实浏览器指纹认证。

正文默认写 stdout，错误与简要 `-v` 诊断写 stderr。下载使用有界块复制；请求数据最多缓存 32 MiB。`--compressed` 显式启用响应解码，仍受核心解码上限保护；超大文件建议保持原始编码下载。

## 范围与边界

本项目提供 HTTP/HTTPS 命令行兼容子集，不提供 libcurl ABI。正式 v0.2.0 支持 HTTP/1.1 和 HTTP/2，不支持 HTTP/3、FTP、WebSocket、多个 URL、并行、multipart 或环境代理。未知参数直接报错，不转交系统 curl。

完整参数、退出码和行为差异见 [COMPATIBILITY.md](COMPATIBILITY.md)。包括 `--max-time 0` 在内的 CLI 超时设置仍受上游建连保护限制，详见该文档。

## 开发与验收

```sh
go test ./...
go vet ./...
python scripts/check_docs.py
python scripts/package.py
```

测试使用本地 HTTP/TLS、HTTP CONNECT、HTTPS CONNECT 与 SOCKS5 靶场，不依赖公网探针。CI 按改动选择档位：文档只检查文档；普通补丁跑 Linux/Windows；TLS、代理、头语义、流式传输、依赖和打包改动至少跑五平台及 Linux race、5 分钟资源检查。`scripts/test_plan.py` 生成可追溯计划。

五个平台：Linux amd64/arm64、macOS amd64/arm64、Windows amd64。每个平台仅构建一份候选，并对打包后的原始字节执行解包、身份与本地请求烟测；压缩包附 SHA-256 和构建清单。首次验收中的 5 分钟资源检查不等于完整性能认证；尚无性能结论。

后续路线与当前交付边界见 [ROADMAP.md](ROADMAP.md)。测试记录见 [VALIDATION.md](VALIDATION.md)。

完整调用示例、脚本/语言接入、请求文件、批处理和公共接口演进见 [调用方式规划](INVOCATION_PLAN.md)，其中明确区分当前支持与拟议接口。

## 许可证

项目自有代码采用 [Apache-2.0](LICENSE)，见 [NOTICE](NOTICE)。依赖保持各自许可证；平台包收集依赖的 LICENSE、NOTICE、COPYING、AUTHORS 与第三方声明文件，随附模块清单。
