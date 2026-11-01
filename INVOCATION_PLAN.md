# fcurl 调用方式与接口演进规划

本文规划更完整的调用体验，不代表新增参数已经实现。当前能力以 [COMPATIBILITY.md](COMPATIBILITY.md) 为准；实现基线为 `8232f7d`。以下“现有”示例可用于当前开发版，“拟议”示例只用于接口评审。

## 1. 调用层次

保留 `fcurl [选项] URL` 作为主要入口，兼容已有脚本习惯。在此之上补齐三个方向：请求文件用于保存和重放复杂请求；批处理用于多个请求和连接复用；语言接入用于程序调用和结构化结果。

Python、Node、Go 目前均可启动 fcurl 子进程。需要长期 Session、高并发或流式 API 的应用优先使用现有 fastls Go 核心或 fastls-ffi SDK；fcurl 不另建一套 TLS 引擎。仓库内 `internal/cli` 当前不能作为外部 Go 公共 API 导入。

所有入口最终归一到相同的请求、会话和结果模型，避免命令行、请求文件和程序接口产生不同默认行为。

## 2. 当前可用：直接调用

### 2.1 GET、HEAD、POST、PUT、PATCH、DELETE

```sh
fcurl 'https://example.com/search?q=fastls&page=1'
fcurl -I https://example.com
fcurl --json @request.json https://example.com/api
fcurl -X PUT --data-binary @payload.bin https://example.com/object
fcurl -X PATCH --json @patch.json https://example.com/object/123
fcurl -X DELETE https://example.com/object/123
```

URL 的查询参数当前由调用方编码。自定义方法按核心规则转为大写。指定请求数据默认 POST，显式 `-X` 覆盖实际方法。

### 2.2 指纹、头与认证

```sh
fcurl --list-profiles
fcurl --profile chrome/156/windows https://example.com
fcurl --profile-file profile.json https://example.com
fcurl -H 'Authorization: Bearer DEMO_TOKEN' https://example.com/api
fcurl -H 'X-Tag: first' -H 'X-Tag: second' https://example.com
fcurl -H 'User-Agent:' -H 'X-Empty;' https://example.com
```

profile ID、Spec 文件、JA3、JA4R 四种来源互斥。重复普通头保留多值；`Name:` 抑制头，`Name;` 发送空值。`-A` 只改实际 UA，不自动重生成 Client Hints 或协议指纹。当前没有 `-u`、Cookie 文件或独立 Bearer 选项，认证可显式提供请求头。

### 2.3 代理、CA 与超时

```sh
fcurl -x http://127.0.0.1:8080 https://example.com
fcurl -x socks5h://127.0.0.1:1080 https://example.com
fcurl -x https://127.0.0.1:8443 --proxy-cacert proxy-ca.pem https://example.com
fcurl --cacert origin-ca.pem --connect-timeout 5 --max-time 30 https://example.com
fcurl -L --max-redirs 5 https://example.com
```

不读取环境代理。HTTP/HTTPS 代理当前都要求 CONNECT，包括 HTTP 目标。`socks5` 本地解析目标，`socks5h` 由代理解析。目标站与 HTTPS 代理使用独立信任配置；`-k` 只作用于目标站。CLI 的零超时不会关闭核心已有的建连保护。

### 2.4 文件、标准输入与输出分离

```sh
fcurl --data-binary @payload.bin https://example.com/upload
fcurl --json @request.json https://example.com/api
printf '%s' '{"hello":"world"}' | fcurl --json @- https://example.com/api
fcurl -L -o result.bin -D response.headers https://example.com/download
fcurl -sS --fail-with-body -o response.json https://example.com/api
```

下载流式输出；当前请求正文最多缓存 32 MiB。`--data @file` 移除 CR/LF/NUL，二进制与 JSON 模式保留字节。`-D` 收集各跳响应头，但响应头经过解析重建。失败可能留下部分输出文件。

PowerShell 推荐用文件传 JSON，避免复杂转义与管道编码差异：

```powershell
$fcurl = 'C:\Tools\fcurl.exe'
& $fcurl --json '@request.json' --max-time 30 -o response.json https://example.com/api
$code = $LASTEXITCODE
if ($code -ne 0) { throw "fcurl failed with exit code $code" }
```

## 3. 当前可用：程序启动子进程

调用时传递参数数组，不拼接 shell 命令。进程退出码描述传输或 fail 策略结果；HTTP 状态与进程成功是不同概念。当前结构化 HTTP 状态需读取 `-D` 文件，正文使用 stdout 或 `-o`。将来由第 5 节的 metadata 接口替代人工解析。

### 3.1 Python

```python
import json
import subprocess

with open("response.json", "wb") as output:
    result = subprocess.run(
        ["fcurl", "--profile", "chrome/156/windows",
         "--json", "@-", "--fail-with-body", "--max-time", "30",
         "https://example.com/api"],
        input=json.dumps({"hello": "world"}).encode("utf-8"),
        stdout=output,
        stderr=subprocess.PIPE,
        timeout=35,
        check=False,
    )
if result.returncode:
    raise RuntimeError(result.stderr.decode("utf-8", errors="replace"))
```

将大响应直接写文件；`capture_output=True` 会在 Python 中缓存正文。调用方还应处理启动失败和 `TimeoutExpired`。父进程超时用于兜底，CLI 总超时用于尽可能完成自身清理。

### 3.2 Node.js

```javascript
import { spawn } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { pipeline } from 'node:stream/promises';

const child = spawn('fcurl', [
  '--json', '@-', '--fail-with-body', '--max-time', '30',
  'https://example.com/api'
], { stdio: ['pipe', 'pipe', 'inherit'] });
const completed = new Promise((resolve, reject) => {
  child.once('error', reject);
  child.once('close', (code, signal) => {
    code === 0 ? resolve() : reject(new Error(`fcurl: ${code ?? signal}`));
  });
});
child.stdin.on('error', () => {}); // 失败由进程结果统一报告
child.stdin.end(JSON.stringify({ hello: 'world' }));
await Promise.all([
  pipeline(child.stdout, createWriteStream('response.json')),
  completed
]);
```

上例展示当前子进程调用，不是新的 Node SDK。正式封装还应在写文件失败时终止子进程，并将输入、输出、进程错误汇总为一个调用结果。

### 3.3 Go

外部程序可用 `exec.CommandContext`，将请求写入 `Stdin`、正文写入文件、诊断写入单独 Writer，并用 `exec.ExitError.ExitCode()` 判断结果。Windows 进程强制终止不等于 CLI 已处理取消。

高频 Go 调用建议直接复用 `fastls.NewSession` 与 `Session.DoContext`，显式关闭 Body 和 Session。该入口属于上游 Go API；未来 fcurl 公共执行包应在它之上增加命令行一致的重定向、输出和错误策略，不能把当前 internal 包直接当 SDK 发布。

## 4. 0.2 规划：把单请求能力补齐

以下参数均为拟议能力，当前未知参数仍直接报错。

### 4.1 查询、表单与请求体

- `-G/--get`、`--data-urlencode`：统一处理 query 编码，保留重复键与空值。
- `--data-raw`：字符串中的 `@` 不解释为文件。
- `-F/--form`、`--form-string`：multipart 字段、文件名、Content-Type、多文件和同名字段。
- `-T/--upload-file`：文件或 `-` 标准输入流式上传，区分可重开文件与不可重放输入。
- `-H @headers.txt`：文件中每行一个头编辑，保留重复字段和抑制语义。

拟议调用：

```sh
fcurl -G --data-urlencode 'q=TLS 指纹' --data-urlencode 'tag=a&b' https://example.com/search
fcurl -F 'title=demo' -F 'file=@report.pdf;type=application/pdf' https://example.com/upload
fcurl -T archive.tar https://example.com/object
```

请求体来源最终必须唯一；原始 body、multipart、upload 不能静默覆盖。文件上传支持已知长度，管道输入允许未知长度。307/308、认证协商或重试要求重放时，不可重放源必须明确失败，不能发送空正文或无限缓存。

### 4.2 认证、Cookie 与会话

- `-u/--user`、`--basic`、`--oauth2-bearer`：首批只实现 Basic 与显式 Bearer。
- `-b/--cookie`、`-c/--cookie-jar`：支持 Cookie 字符串及 Netscape 格式文件，明确定义同名值、会话 Cookie 和过期清理。
- `--netrc-file`：显式读取账号配置；自动发现和更复杂认证后置。
- `--referer`、`--proxy-user`：把业务头、目标站认证和代理认证分开。

拟议调用：

```sh
fcurl --user demo:password --basic https://example.com/api
fcurl -c cookies.txt --json @login.json https://example.com/login
fcurl -b cookies.txt https://example.com/account
```

Session 隔离键至少包括 profile、目标信任设置、客户端证书、代理和 Cookie 会话；不能只按 hostname 合并。显式 Cookie 头与 Jar 的优先级、跨源凭据处理保持可解释且有回归用例。

### 4.3 下载、状态与流

- `-O`、`--output-dir`、`--create-dirs`：明确远端文件名、覆盖及目录规则。
- `-C/--continue-at`、`--range`：先验证 206、Content-Range、ETag/Last-Modified；服务器忽略 Range 返回 200 时禁止直接追加损坏文件。
- `--max-filesize`、`--limit-rate`：分别限制接收量和传输速率，区分线上压缩字节与解码字节。
- `-N/--no-buffer`：低延迟 SSE/分块转发，不把块边界当事件边界。
- `--post301/302/303`：显式正文保留策略；不会因为用了 `-X POST` 就默认启用。

Cookie、Basic、multipart、续传和 POST 跳转规则以 [curl 手册](https://curl.se/docs/manpage.html) 为语义参考；POST 模式与自定义方法的区别参考 [CURLOPT_POSTREDIR](https://curl.se/libcurl/c/CURLOPT_POSTREDIR.html)。逐项选择固定参考版本，不把不同系统自带 curl 当作同一个版本。

## 5. 0.3 规划：稳定的机器调用

### 5.1 保存请求

拟议保留 curl 风格 `-K/--config` 文本配置，同时增加明确独立的 `--request-file` JSON。二者不能共用同一个参数却根据文件内容猜格式。首版请求文件只支持 JSON，避免同时维护 YAML/TOML 解析差异。

```sh
fcurl --request-file request.json --max-time 20
```

拟议文件模型：

```json
{
  "schema_version": 1,
  "id": "create-order",
  "method": "POST",
  "url": "https://example.com/orders",
  "profile": "chrome/156/windows",
  "headers": [
    {"name": "X-Tag", "values": ["first", "second"]}
  ],
  "body": {"kind": "file", "path": "order.json", "content_type": "application/json"},
  "timeouts": {"connect_ms": 5000, "total_ms": 30000},
  "output": {"body_file": "order-response.json", "metadata_file": "order-result.json"}
}
```

字段必须有版本与严格校验。相对输入/输出路径相对请求文件目录；来自 stdin 的请求文件相对工作目录。不隐式执行 shell、展开模板或读取环境凭据。`--config` 的语法遵循选定 curl 基线，请求 JSON 的数组/对象契约独立定义。

覆盖规则：内置默认值 < 显式 config < request-file/批任务配置 < 显式 CLI 覆盖。只用真正出现的 CLI 参数覆盖，不能把解析器默认值当成用户输入。普通单值字段替换；头编辑顺序合并；互斥 body 来源仍拒绝。profile 切换须重新生成默认头，再应用用户编辑，不能继承前一个 profile 的默认值。

### 5.2 正文和元数据分离

拟议增加 `--metadata FILE` 与 `--write-out FORMAT`。保留 stdout 默认正文、stderr 默认诊断；不让状态 JSON、进度或日志混入二进制正文。请求文件的 `metadata_file` 与 CLI `--metadata` 对应。

```sh
fcurl --request-file request.json -o body.bin --metadata result.json
fcurl -o body.bin --write-out '%{json}' https://example.com
```

版本化 metadata 至少包含：request_id、exit_code、error.kind、HTTP status、最终协议/URL、跳转数、尝试次数、收发字节、总耗时、profile 选择及匹配依据、正文输出位置。不存在的阶段测量值用 null，不能用 0 假装已测得。header 用有序多值数组，不降级成单值 map。

数组顺序表示接口输出顺序；底层未提供原始线上顺序时，不把解析重建顺序描述成抓包顺序。

HTTP 404 与网络失败分别表示；是否触发退出 22 仍由 fail 策略决定。计时单位固定，正文不默认塞入结果 JSON。URL 凭据和认证头不进入默认诊断；显式 metadata 需要说明最终 URL 的查询内容会被保存。正文与 metadata 不能写同一个文件；二者同时使用 stdout 时拒绝。

### 5.3 批量与并发

```sh
fcurl --batch requests.jsonl --results results.jsonl
fcurl --batch requests.jsonl --parallel --parallel-max 8 --results results.jsonl
```

以上均为拟议接口。JSONL 每行一个带 id 的请求对象，支持增量读取；批任务正文默认写独立文件。结果每行一个 metadata 对象，以 id 关联。默认按输入顺序返回结果；缓冲需有界，另提供显式完成顺序模式。多个任务不能争用同一输出文件。

定义全局与每 origin 并发上限、排队上限、每任务超时、整批截止时间和 Ctrl+C 行为。默认顺序执行；显式请求并行才提高并发。保留各任务结果，并规定：整批退出 0 代表所有任务满足成功策略；有失败返回按输入顺序首个失败任务的码；批配置无效返回 2；取消返回 130。`--fail-early` 用于停止继续启动任务，已完成结果不丢弃。

stdin 只能有一个消费者：批任务或请求文件已经从 stdin 读取时，正文不能再用 `@-`。`--next` 和多个位置 URL 在同一请求模型成熟后加入，不与原生 JSONL 批任务混为一种格式。

### 5.4 重试

拟议提供 `--retry`、`--retry-delay`、`--retry-max-time` 与明确的重试范围。默认不重试。启用后先覆盖 GET/HEAD 等幂等、可重放请求；POST/PATCH 必须额外显式授权并验证 body 可重放。对已写入 stdout 或输出文件的部分正文，必须定义清理/重开策略，不能盲目叠加。

优先处理明确的暂时性错误和选定 408/429/5xx 状态；支持 Retry-After 和有上限的退避。总预算覆盖所有尝试与等待，单次请求超时另计。结果保留每次尝试、最终原因和是否已收到响应，不能把重试成功描述成只发过一次请求。

## 6. 后续规划：协议、网络与语言 API

### 6.1 显式协议与拨号

拟议加入 `--http1.1`、`--http2`、`--http2-prior-knowledge`、`--resolve`、`--connect-to`、`--ipv4/ipv6`、`--noproxy`、显式环境代理开关，以及 mTLS 的 `--cert/--key`。

先检查核心是否能表达并验证这些能力。协议强制、SNI、证书验证名称、Host、DNS 覆盖、实际拨号地址必须分开；profile 与协议要求冲突时明确报错。`--resolve` 不能意外改变 TLS 校验名称。没有后端能力时不新增“看似接受但实际无效”的选项。

HTTP/3、WebSocket、NTLM/Kerberos、完整 libcurl ABI 单独规划。H3 不能继承 H1/H2 的完整浏览器指纹承诺，代理能力也必须以核心实际支持为准。

### 6.2 公共 Go 执行包与轻量语言封装

若确有复用需求，新增公共包 `github.com/FastTLS/fastls-curl/client`；暂定模型为 Executor、Request、Response、TransferError，API 名称需单独评审。Executor 管理 Session 和资源上限；Request 接收 context、headers 多值数组及可重放 body factory；Response 暴露流式 Body、metadata 和 Close。

CLI 与公共包共享内部执行层，但公共包不解析命令行、不自行退出、不写全局 stdout。构造器必须返回 error，Close 可重复调用，context 取消覆盖读取阶段。复用完整 Session 的用户仍可直接使用上游 fastls。

Python/Node 子进程封装在 metadata 契约固定后提供，负责参数数组、stdin、流式输出、取消和错误对象。不默认给每次请求重新安装/下载二进制。高吞吐场景继续使用已有 fastls-ffi；是否支持一个持久工作进程，需要先证明收益并设计帧、背压和进程恢复协议。

## 7. 交付顺序与完成条件

第一批完成单请求：query/表单/文件流、认证/Cookie、状态与下载；可以按独立能力拆分，但冻结后集中做一次候选验收。

第二批完成自动化：请求文件、metadata、批处理、取消与有限重试。优先让程序可靠调用，再增加并行吞吐。

第三批完成协议和连接控制：复用、DNS/拨号、显式协议、mTLS；公共 Go API 和长期进程根据真实需求单列，不让首版 CLI 被 ABI 承诺拖住。

每项完成需要四类证据：参数和机器字段契约、固定参考版本的行为对照、本地正常/故障/取消场景、与实际风险匹配的平台与安装检查。阶段计时、连接复用、资源预算等能力只能报告已测范围。

规划与说明仅做文档检查；实现 TLS、代理、流式、并发、依赖或打包改动至少小版本范围。只改文档或来源元数据时复用原候选证据，不因推送、合并或标签重跑完整性能认证。
