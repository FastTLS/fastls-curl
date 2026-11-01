# 实施路线

## 0.1：独立命令行

独立 Go 模块与 `cmd/fcurl`；直接调用 fastls.Session，不经 FFI 或 Fetch 服务。固定 fastls v0.2.1、imitate v0.1.2、profile v0.1.1，记录完整模块摘要。实现范围以 COMPATIBILITY.md 为准。

首版验收包括选项与错误行为、本地 HTTP1/HTTP2、多值头、TLS 验证与 CA、代理信任、SOCKS DNS、跨源重定向、正文重放、压缩、流式下载、取消和输出故障，以及五平台产物身份/安装、Linux race、5 分钟资源检查。完整性能认证单独立项，不把资源检查当作性能结论。

## 0.2：文件传输与脚本

按实际使用需求依次加入 multipart、流式上传、Cookie 文件、write-out 字段、多 URL。每项先定义 curl 对照行为，再实现和测试。Cookie 文件必须覆盖域/path/Secure、权限和导入导出边界；流式上传必须覆盖不可重放源、取消及正文生命周期。

## 后续能力

并行与重试需要并发及资源预算；断点续传需要验证服务器 Range 和本地文件一致性；HTTP/3 必须保留核心的代理与完整指纹限制，并单独验收。libcurl ABI 集成另行设计，不在现有 CLI 中模拟其兼容性。

## 发布方式

首选 GitHub Release 发布五个平台压缩包、SHA-256、模块与源码身份清单、许可证材料。尚未创建正式标签或发布 Release。候选通过后复用同一字节，不因合并或标签变化自动重跑资源/性能认证。

暂不创建 npm 包；若后续需要 npm 分发，遵循工作区 Trusted Publisher / GitHub Actions OIDC 偏好，并单独确认新包和工作流身份，不复用长期 token。
