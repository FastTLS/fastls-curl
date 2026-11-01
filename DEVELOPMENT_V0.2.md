# v0.2.0：第一批单请求扩展

本页描述正式 v0.2.0 新增能力；历史 `v0.1.0` 不包含这些参数。基础行为继续遵循 [COMPATIBILITY.md](COMPATIBILITY.md)。安装后用 `fcurl --version` 确认 `0.2.0`。

## 查询与请求正文

```sh
fcurl -G --data-urlencode 'q=TLS 指纹' --data-urlencode 'tag=a&b' https://example.com/search
fcurl --data-urlencode 'name=hello world' --data-urlencode 'note@note.txt' https://example.com/form
fcurl --data-raw '@literal-not-a-file' https://example.com/api
fcurl -G -I --data-urlencode 'key=value' https://example.com
```

`--data-urlencode` 支持 content、=content、name=content、@file、name@file；文件 `-` 表示 stdin。name 按原样保留，value 采用 form 编码，空格变为 `+`，保留重复键和值的出现顺序。file 内容不删除换行；编码后的合计正文仍最多 32 MiB。

`-G/--get` 把数据一次性追加到原始 URL 查询部分，不发送请求正文；重定向不会重新追加。与 `-I` 合用发送 HEAD，显式 `-X` 仍覆盖实际方法。已有查询字节保留；新编码使用大写百分号转义，可能与某些 curl 版本的 URI 转义字母大小写不同，解码语义相同。原始 query 数据包含空白、控制或非 ASCII 字节时明确要求使用 --data-urlencode。`--get` 与 `--json` 互斥。

`--data-raw` 不把 `@` 解释为文件，其余使用已有普通表单数据规则。现有 `--data`、`--data-binary`、`--json` 行为保留。

## 请求头文件与 Referer

```sh
fcurl -H @headers.txt https://example.com
fcurl -e https://ref.example/page https://example.com
```

头文件每行一条头编辑，支持 LF/CRLF、重复字段、Name: 抑制和 Name; 空值；空行忽略。不做递归 include、注释或折叠头解释。文件最大 4 MiB、每行 64 KiB、展开后最多 4096 条文件头。`-H @-` 可从 stdin 读取。

显式 `-H Referer: ...` 优先于 `-e/--referer`。自动 Referer 与 ;auto 后缀尚未支持，不能把该后缀当作已实现的自动模式。头格式与认证格式错误不在诊断中回显值。

## Basic 与 Bearer

```sh
fcurl -u user:password --basic https://example.com/api
fcurl --oauth2-bearer DEMO_TOKEN https://example.com/api
```

Basic 直接发送，不进行挑战协商；只接受显式 user:password，不提供交互式密码提示。密码可含冒号。Basic 与 Bearer 互斥，空 Bearer 拒绝。显式 Authorization 头，包括抑制规则，始终优先于认证选项。跨源跳转移除认证，不会在回跳时恢复。Digest、NTLM、Kerberos、netrc 暂未实现。

## Cookie 文件与连续请求

```sh
fcurl -c cookies.txt --json @login.json https://example.com/login
fcurl -b cookies.txt https://example.com/account
fcurl -b cookies.txt -c cookies.txt https://example.com/refresh
fcurl -b 'a=1' -b 'b=2' https://example.com
```

`-b/--cookie` 可重复：含 `=` 的参数按原始 Cookie 字符串处理，其余作为 Netscape 文件路径；空值只启用已有 Jar，`-` 从 stdin 读取。多个原始字符串以 `;` 连接。原始 Cookie 优先于 Jar 出站值，显式 `-H Cookie` 再优先；所有响应仍更新 Jar。

Cookie 文件严格采用七字段、TAB 分隔的 Netscape 格式，保留 host-only/subdomain、Path、Secure、HttpOnly、会话 Cookie、过期秒数；支持 #HttpOnly_ 前缀。域匹配和公共后缀校验复用 Go 标准 cookiejar。不持久化已过期或已删除的 Cookie。输入文件上限 4 MiB，行上限 64 KiB；无效文件、丢失文件或无效域作用域返回 26，与 curl 忽略某些无效输入的行为不同。

Cookie 文件按创建次序从新到旧保存，导入时恢复该次序，避免同一路径下的同名主机/域 Cookie 在重载后换位。更新保留原次序，删除后重建获得新次序；同一文件重复身份采用最后一条值。v0.2.0 原始发行的文件按字典序保存，缺失的历史创建次序无法从旧文件恢复。

`-c/--cookie-jar` 输出 Netscape 文件，同一次调用允许 `-b/-c` 指向同一个文件。写入同目录临时文件后原子替换，Unix 文件模式为 0600；Windows 使用平台自身的继承权限。Jar 保存与响应正文输出分开，不能和 `-o/-D` 指向同一个文件。暂不支持 `-c -`，避免混入正文 stdout。

HTTP 错误、网络失败或取消后仍保存已收到的有效 Cookie。Jar 写入失败返回 23，并保留已有请求错误作为原因；不会仅告警后返回成功。临时写入失败不截断旧 Jar。Netscape 格式不表示 SameSite/Partitioned 等完整浏览器存储语义，本工具也不宣称实现完整浏览器 Cookie 策略。

## stdin 归属与尚未实现项

头文件、Cookie 文件、普通数据、编码数据中最多只能有一个 stdin 消费者；冲突在读取或发送前返回 2。`--data-raw '@-'` 是普通文本，不消耗 stdin。

这一批未加入 multipart、流式上传、下载续传、重试、批任务、metadata、公用 Go 执行包或 H3。后续仍按 [调用规划](INVOCATION_PLAN.md) 分批推进。

## 验收范围

相关本地回归与 curl 差分覆盖查询、重复值、编码、头文件、认证优先级、跨源凭据删除、Cookie 读写/删除/域/path/Secure/HttpOnly/过期、公共后缀拒绝、同文件替换、保存失败和 stdin 冲突。URI 转义大小写只按等价解析语义比较，正文编码逐字节比较，并另行检查本工具的 query 输出顺序。

涉及认证和 Cookie 状态，按小版本风险档执行五平台、Linux race 和 5 分钟资源检查。冻结功能提交 b2414e0 的 [CI 41279793202](https://github.com/FastTLS/fastls-curl/actions/runs/41279793202) 已通过上述范围；正式版本的身份与安装另由候选 CI 核验。

本批资源检查包含每次请求的 Cookie 接收、文件导入和原子替换，观测热身后保留堆与 goroutine 增长；不把该检查当作吞吐性能认证。

平台参考 curl 的 Cookie 分隔空格与 CRLF 文件行为存在差异。差分比较保持 Cookie 名值与顺序语义，以 LF 表达同一组参考头；fcurl 的 LF/CRLF 输入和原始 Cookie 输出由独立精确断言覆盖。历史失败保留。CI 的 identity 复用还要求 Go 测试输入未改变，新用例不能随生产输入相同而被跳过。
