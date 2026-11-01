# curl 兼容范围

这是首版行为契约；未列入的选项不作兼容承诺。语义参考 [curl 手册](https://curl.se/docs/manpage.html)，不宣称完整替代 curl。

## 已支持参数

- 请求：`-X/--request`、`-H/--header`、`-A/--user-agent`、`-d/--data`、`--data-binary`、`--json`、`--url`。
- 输出：`-i/--include`、`-I/--head`、`-o/--output`、`-D/--dump-header`、`-s/--silent`、`-S/--show-error`、`-v/--verbose`。
- 网络：`-L/--location`、`--max-redirs`、`-x/--proxy`、`--cacert`、`--proxy-cacert`、`-k/--insecure`、`--connect-timeout`、`-m/--max-time`。
- 响应：`--compressed`、`-f/--fail`、`--fail-with-body`。
- 信息：`-h/--help`、`-V/--version`。
- 扩展：`--profile`、`--list-profiles`、`--profile-file`、`--ja3`、`--ja4r`。

支持长选项 `--name=value`、短选项组合 `-sS`、短选项紧接值 `-XPOST`，以及 `--` 结束选项。单值选项后者覆盖前者；重复 `-H`、请求数据按出现顺序处理。仅接受一个带 scheme 的绝对 HTTP(S) URL。

## 请求头与数据

同名普通头保留多值及值顺序；字段间顺序以 profile 和底层传输规则为准，不承诺命令行位置等于线上字段顺序。`-H 'Name:'` 抑制字段，`-H 'Name;'` 发送一个空值。用户显式设置优先于指纹默认值。Host 只接受一个非空值。

重复 `-A` 使用最后的值，显式 `-H 'User-Agent: ...'` 始终优先于 `-A`。跳转移除正文时，自动生成的 Content-Type 一并移除，用户显式指定的 Content-Type 保留。

`Content-Length`、`Transfer-Encoding`、`Trailer` 和 `Proxy-Authorization` 由传输层管理，不接受 `-H` 覆盖。代理认证放在代理 URL 中。请求 URL 中的用户名、密码不支持。自定义方法按核心要求转为大写，这是与 curl 的差异。

请求数据默认使用 POST，显式 `-X` 可覆盖。`--data @file` 和 `--data @-` 移除 CR/LF/NUL；`--data-binary`、`--json` 保留字节。普通数据重复项以 `&` 连接，JSON 重复项直接连接；JSON 与其他数据模式混用不支持。JSON 只设置默认 Content-Type/Accept，不检查 JSON 语法。所有数据合计最多 32 MiB，上传暂不流式。

空数据拼接固定采用 curl 8.12.1 语义：只有已累积正文非空才添加 `&`，因此 `-d '' -d second=2` 发送 `second=2`。curl 8.5.0 发送 `&second=2`；这是已知版本差异，分别保留精确断言。依据为上游 [8.5.0 postfields 存在性检查](https://github.com/curl/curl/blob/curl-8_5_0/src/tool_getparam.c#L1816) 与 [8.12.1 buffer 长度检查](https://github.com/curl/curl/blob/curl-8_12_1/src/tool_getparam.c#L916)。

未指定数据时为 GET，`-I` 为 HEAD 并输出响应头；`-I` 与数据互斥。`-I -X METHOD` 仍仅输出响应头。

## 重定向与会话

默认不跟随重定向。`-L` 跟随 301/302/303/307/308，限 50 次；`--max-redirs` 只接受非负数。协议只允许 HTTP(S)。301/302 对使用请求数据的 POST 模式、303 对非 HEAD 请求移除正文；未指定 `-X` 时方法改为 GET。307/308 重放缓存的原始正文。显式 `-X` 在跳转中保留，但不会阻止 301/302 移除数据，例如 `-X PUT -d x=1 -L` 的后续请求仍为 PUT、正文为空。

跨 scheme、主机或有效端口时，移除显式 Authorization、Cookie、Host。移除后不会在后续回跳恢复。CookieJar 按域、路径、Secure 和公共后缀规则管理服务端 Cookie；跨源跳转后抑制显式 Cookie 不影响合规的 CookieJar Cookie。没有 `--location-trusted`。自定义的其他敏感头由使用者决定是否应随 `-L` 发出。

每一跳使用独立 Session，随响应体关闭释放连接；跳转之间共用 CookieJar。首版未启用跨跳连接复用。中间响应的正文丢弃，`-i/-D` 保留各跳响应头。响应头由解析结果重建、按字段名排序，原始字段大小写和线上顺序不保留。

Go 1.26 按显式 Host 确定 CookieJar 的有效主机；为自定义虚拟主机收到的 Cookie 不会在跳转回实际 IP authority 后当作该 IP 的 Cookie 发送。

## 代理、TLS、压缩与输出

HTTP 和 HTTPS 代理均通过 CONNECT 建隧道，包括 HTTP 目标；这与 curl 对 HTTP 目标默认使用绝对形式代理请求的方式不同，代理必须允许目标端口的 CONNECT。支持 URL 内 Basic 代理认证及 SOCKS5 用户名密码认证。

`socks5` 在本地解析目标 DNS，`socks5h` 交给代理解析。环境中的 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY 不生效。

系统根证书默认启用。`--cacert` 替换目标站根证书，`--proxy-cacert` 单独替换 HTTPS 代理根证书。`-k` 只关闭目标站验证。自定义 CA 文件最大 4 MiB。

秒数允许小数，范围 0..86400，0 表示不增加 CLI 对应计时器。连接时间覆盖每跳连接阶段，总时间覆盖所有请求跳转及正文消费。上游 fastls 自带约 30 秒建连保护，0 不会关闭该保护；不承诺与 curl 的无限等待完全相同。

`--compressed` 发送默认 Accept-Encoding 并启用核心支持的解码；显式 Accept-Encoding 优先。核心默认解码总量上限仍适用，超限返回接收错误。未启用时保留响应编码。

stdout 默认只有响应正文。没有进度条，因此 `-s` 主要控制错误信息；`-sS` 恢复错误显示。`-v` 仅显示方法、scheme/authority、响应协议/状态，不输出头值、URL 路径/查询或代理凭据。`-o/-D -` 表示 stdout。同一个文件不能同时作为头和正文输出。失败可能留下部分文件，不保证原子替换。显式输出路径会在网络请求前创建或截断。

## 指纹

四种指纹来源互斥：生成器规范 ID、单个 profile.Spec JSON、自定义 JA3、自定义 TCP JA4R。profile 文件严格拒绝未知字段、多个对象及超过 4 MiB 的文件。不支持 profile bundle 格式。无显式指纹时使用 Go TLS。

JA3/JA4R 需要原始可解析字符串，不能传摘要。H3 `q` 前缀明确拒绝。自定义 UA 只覆盖实际请求值，不自动重生成 Client Hints 或调整协议指纹。生成器目录身份不代表线上全指纹已经过真实浏览器验证。

## 退出码

- `0`：传输成功；未指定 fail 参数时 HTTP 4xx/5xx 也可成功。
- `1`：不支持的 URL 协议。
- `2`：选项或传输/profile 配置错误。
- `3`：URL 无效，或包含不支持的 URL 凭据。
- `5`：代理主机 DNS 解析失败。
- `6`：目标主机 DNS 解析失败。
- `7`：连接失败，或未进一步分类的传输错误。
- `22`：使用 fail 参数且 HTTP 状态不小于 400。
- `23`：输出打开、写入或关闭失败。
- `26`：请求数据读取失败或超限。
- `28`：超时。
- `35`：可识别的 TLS 记录/握手失败。
- `47`：重定向次数超限。
- `56`：响应读取、解码或其他接收错误。
- `60`：目标站或 HTTPS 代理证书验证失败。
- `77`：CA 文件读取或解析失败。
- `130`：请求上下文取消，包括处理到的 Ctrl+C/SIGTERM。

退出码按已识别错误映射；不承诺所有底层错误与 curl 一一对应。尤其代理认证/隧道拒绝目前归入连接错误。Windows 强制结束进程及操作系统默认信号退出不在此保证中。
