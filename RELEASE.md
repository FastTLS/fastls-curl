# 发布流程

`v0.2.1` 发布五个平台：Linux amd64/arm64、macOS amd64/arm64、Windows amd64。正式发布入口为 `.github/workflows/release.yml`，支持稳定标签推送和指定既有标签的手动续跑。

## 候选与验收

1. 固定 VERSION、CLI 默认版本、go.mod/go.sum 和工具链，提交到 main。
2. `fcurl checks` 对该精确提交生成候选。五个平台各构建一次，压缩包解包后核对 binary SHA-256、版本，并执行真实本地 HTTP 请求。
3. 若生产输入或编译配方改变，必须完成五平台、Linux race 和 5 分钟资源验收，并将新的成功记录固定到 RELEASE_VALIDATION.json。验证器要求固定仓库、CI 工作流、提交和全部指定任务成功。
4. 若只改版本、文档或发布元数据，验证器逐文件比较 cmd/internal 的生产输入、模块图和编译配方；只规范化唯一的版本字符串。只有上游完整验收仍可验证时，才能采用 identity 档，保留五平台构建身份和安装检查，不重复跑资源认证。

RELEASE_VALIDATION.json 当前固定 v0.2.1 修复验收 run 41317439501 / head c7b83c0cb828c540519d93ff3ac7ca2d729872b4，覆盖五平台、Linux race 和 5 分钟 Cookie 文件资源检查。版本准备复用前核实生产、测试与编译输入。历史报告保留；不能把资源检查描述为正式版本新字节的完整性能认证。

## 标签与发布

候选 CI 成功后，为同一 main 提交创建 `vVERSION` 标签，不移动或覆盖已存在的标签。标签触发发布流程：

- 验证标签精确指向当前 checkout，且提交属于 main；核对 VERSION、历史运行验收和此提交成功的候选 CI。
- 下载原始五平台候选，核对源码提交/树、正式版本、工具链、平台、压缩包及 binary 摘要，不重新编译。
- 生成 SHA256SUMS、RELEASE.json、SOURCE_VERIFICATION.json，附五个压缩包与各自 BUILD.json。
- 先创建 draft Release，上传缺失文件，核对远端完整资产集合和 SHA-256 后公开发布。

已有同名资产只有摘要相同才复用；内容不同立即停止，不使用 clobber、不删除失败记录。已公开 Release 缺少或出现不同资产时停止，不静默修改正式资产。GitHub 同一运行的 rerun 可从 draft 继续；新的手动运行如产生不同的发布记录，需先核对既有 draft 身份，不直接覆盖。

## 安装与核验

用户从 GitHub Releases 下载对应平台压缩包及 SHA256SUMS，核对摘要、解包、运行 `fcurl --version`。Linux/macOS 将 `fcurl` 放入 PATH；Windows 将包含 `fcurl.exe` 的目录加入 PATH。

也可用 Go 1.26+ 安装固定源码标签：

```sh
go install github.com/FastTLS/fastls-curl/cmd/fcurl@v0.2.1
fcurl --version
```

Go 安装是用户自行构建，不等同于五个平台 Release 中的原始不可变二进制。当前只发布 GitHub Release 和源码标签，不创建 npm 或 PyPI 包。
