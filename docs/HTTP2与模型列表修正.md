> 运行时适配/历史说明：本次 1.0.5 沿用 1.0.4 的运行时逻辑。启动器已移除托盘；网络设置改用 `Network-Settings.cmd`，退出用应用自己的退出入口。当前界面/生命周期与测试状态以 `测试与验收.md` 和根目录使用说明为准。

> 本文为 1.0.3 历史说明。1.0.4 已改为优先本地迁移，下面“升级需重新下载”的旧流程不再适用于通过复用检查的 1.0.2 / 1.0.3 运行时。请以 [本地运行时复用与升级](本地运行时复用与升级.md) 为准。

# 1.0.3：HTTP/2 响应头兼容问题与模型列表修正

日期：2026-09-26。社区工程试用版，目标 Windows 10/11 x64。

## 用户现场证据

用户在 Windows PowerShell 本机运行独立 .NET 对照脚本，针对同一 Agnes 模型列表地址、同一个正确 Key：

| 请求 | 状态 | Content-Type | Content-Encoding | 原始长度 | 解码后长度 | PowerShell 解析 | 模型数 |
| --- | --- | --- | --- | ---: | ---: | --- | ---: |
| identity | 200 | application/json; charset=utf-8 | 无 | 1523 | 1523 | 成功 | 12 |
| gzip | 200 | application/json; charset=utf-8 | gzip | 211 | 1523 | 成功 | 12 |

这证明该账号/接口在这两次本地 .NET 请求中能返回可解析的模型列表，gzip 解压后长度也一致。它不证明 DSH 请求的代理出口、协议协商、请求头和返回字节完全相同，也不是 JavaScript JSON.parse 的严格等价测试。

用户此前还报告：DSH 中故意填错 Key 返回 401；填正确 Key 返回 `did not answer with JSON`。

## 已经复现的具体缺陷

使用从 SHA-512 核对后的官方 `0.1.7-rc.2` 安装包提取的 Undici **8.11.0**，配合 Node **24.21.0** 内置 fetch 和便携版 1.0.2 原始网络适配层，在有认证的本地 CONNECT 代理后提供受控 TLS 模型接口。

本地模拟模型列表是测试数据，不是用户的真实模型列表或 Key。

| 服务端/响应 | 1.0.2 适配层结果 | 1.0.3 适配层结果 |
| --- | --- | --- |
| HTTP/1.1 + identity | 成功 | 成功 |
| HTTP/1.1 + gzip / deflate / br | 成功 | 成功 |
| HTTP/2 可用 + identity | 协商 h2，JSON 可解析，但响应头缺失 | 协商 HTTP/1.1，响应头和 JSON 均正常 |
| HTTP/2 可用 + gzip / deflate / br | 协商 h2，响应头缺失，压缩字节进入 JSON.parse，解析失败 | 协商 HTTP/1.1，响应头、解压和 JSON 均正常 |
| 错误测试 Key，返回未压缩 401 JSON | 401 仍然可辨认 | 401 仍然可辨认 |

旧版负向测试日志中的 `EXPECTED-JSON-FAILURE-REPRODUCED` 表示**成功复现预期缺陷**，不是旧版已经正常。

### 缺陷机制

官方包中相关代码链：

1. `undici/lib/dispatcher/client-h2.js` 将 HTTP/2 响应头对象交给请求处理器。
2. `undici/lib/core/request.js` 把这个对象设置为 `controller.rawHeaders`。
3. `undici/lib/dispatcher/dispatcher1-wrapper.js` 采用 `controller.rawHeaders ?? toRawHeaders(headers)`。因为对象不是 null，不会走对象到原始头数组的转换。
4. Node 内置 fetch 的旧 dispatcher 接口收到的不是所需的原始头数组。受控实验中 Content-Type、Content-Encoding 等响应头因此不可见。
5. HTTP 状态码仍在，所以错误 Key 的 401 分支能够工作；对成功但经过压缩的正文，fetch 看不到 Content-Encoding，没有执行解压，之后 JSON.parse 失败。

已从实际发布包的 `@deepseek-ai/dsh-llm-pi-ai/lib/index.js` 核对：HTTP 成功检查之后，正文读取与 JSON.parse 分开处理；JSON.parse 失败才生成用户看到的错误。并非简单的模型列表字段格式不匹配。

**修正此前测试覆盖不足：** 1.0.2 的模型发现集成测试使用 HTTP/1.1 TLS 接口、未压缩正文，没有覆盖 HTTP/2 与压缩的组合，因此当时测试通过不代表这个组合正常。

## 本版修正范围

- 仅更改便携适配模块 `source/assets/portable-node-proxy.mjs` 中由便携版创建的 Agent、Pool、ProxyAgent 的协议选项：`allowH2: false`；代理隧道内的 TLS 连接也明确限制为 HTTP/1.1。
- 保留原有的 HTTP(S)/SOCKS 上游选择、系统/PAC/环境变量/手动代理解析及同一个启动器转接端口。
- HTTPS 仍是 HTTPS；证书验证、Node 系统 CA 和 CONNECT 隧道不变。没有解密代理流量，没有关闭证书验证。
- 不删除 Content-Encoding，不手工重复解压，也不全局设置 Accept-Encoding: identity。HTTP/1.1 的 gzip、deflate、br 响应均经过真实回归测试。
- 不读取或输出 API Key、代理口令、响应正文。原有日志仍是脱敏的事件/来源/状态码信息。
- 调用方显式提供的 dispatcher 保持原样，避免破坏专用安全传输或 DNS 绑定。此兼容措施不声称覆盖所有插件自建的 HTTP 客户端。
- 官方模型发现模块和 Undici 源码未被修改；仍采用之前的有边界 Host 入口、桌面更新入口适配。
- 代价是便携版管理的这些 Node fetch 连接不使用 HTTP/2 多路复用。仅支持 h2 而不支持 HTTP/1.1 的端点不在本兼容路径支持范围；本版不会失败后偷偷绕过代理直连。
- 待跨版本 dispatcher 的 HTTP/2 行为经过验证后，才适合恢复 h2，而不是无证据地修改 JSON 解析器。

## 验证结果

1. 原始官方安装包 288,245,480 字节，SHA-512 再次核对通过：

   `AY7f45dYO7BFrfgaLmzXNWP0pavlxkSbsehPo/WF6PXcFdDK3fF1oHUPs/4f2bzROgQvm6wSgawZ/g7UzbPRmw==`

2. 旧版负向复现矩阵通过：HTTP/2 下三种压缩均复现响应头缺失和 JSON 解析失败。
3. 新版八种协议能力/编码组合全部成功；两种服务器的错误 Key 测试仍返回 401。
4. **实际发布的模型发现模块**通过带认证 Go 便携转接层和上游 CONNECT 代理，对支持 HTTP/2 的 gzip 模拟端点成功获取模型。fixture 确认修正版协商 HTTP/1.1。
5. 全套 Go race 测试通过，含真实官方 ASAR 适配、原有代理/编码/生命周期/便携更新测试。
6. 8 项 Node 桌面代理、更新与 Unicode 启动回归测试通过。
7. Windows x64 vet（`-unsafeptr=false`）、GUI EXE 交叉编译和 Windows 测试二进制交叉编译通过。

原始测试输出：

- `http2-compatibility-reproduction.txt`：1.0.2 负向复现。
- `http2-compatibility-tests.txt`：1.0.3 Go race 与真实模块/压缩集成测试。
- `http2-js-regression-tests.txt`：8 项 Node 回归测试。

### 仍未验证

- 没有用户 Key，也没有代用户发出经过鉴权的 Agnes 请求。
- 没有捕获用户失败的 DSH 响应，尚不能严格证明现场那次错误必然由同一个 h2 缺陷引起。
- 没有原生 Windows 10/11 真机端到端验证。测试用 Node 24.21.0 不是实际 Electron 44 中的 Node 24.18.1；跨版本行为仍需用户更新后确认。
- 不把交叉编译、模拟测试或一项缺陷的修正等同于整套桌面应用无闪退、无其他缺陷。

## 用户升级与确认

1. 从托盘正常退出旧版；备份 Data、Workspace、portable.json。
2. 将 ZIP 解压到临时文件夹，**只把其中新版 DSH-Portable.exe 替换到现有目录**。保留自己的 tools、portable.json、Data、Workspace。
3. 启动后会重新下载和准备官方运行时，应用新模块，预计下载约 288 MB；不是删除会话。准备期间不要拔盘。
4. 不改 Agnes Base URL、正确 Key 或原有代理；重新点击“获取可用模型”。
5. 请反馈是否能列出模型。若服务端列表与对照时相同，应对应 12 个；账号实时结果可能变化。

无需继续运行独立 PowerShell 对照，也无需提供 Key。若仍有错误，先保留错误原文，再针对实际 Host 请求补充诊断，不能重新假定是 Key/地址错误。

## 开发者复现

从官方原始 ASAR 提取 `dsh` 目录及其实际依赖后，设置：

```sh
export DSH_TEST_NODE=/absolute/path/to/node24
export DSH_TEST_RUNTIME=/absolute/path/to/extracted/dsh
export DSH_TEST_OFFICIAL_ASAR=/absolute/path/to/original/app.asar
cd source
go test -race -v ./...
"$DSH_TEST_NODE" --test tests/bootstrap-smoke.mjs tests/electron-network.test.mjs tests/desktop-update.test.mjs
```

旧版受控负向复现使用随包保留的测试 fixture，不参与产品构建：

```sh
DSH_TEST_OLD_ADAPTER="$PWD/tests/fixtures/portable-node-proxy-1.0.2.mjs" \
  go test -race -v -run '^TestPortableCompressionMatrix$' .
```

Go 测试按次创建临时自签名测试 CA，只通过 NODE_EXTRA_CA_CERTS 信任这张测试证书，不设置任何跳过证书验证的开关。正常新版回归不要设置 DSH_TEST_OLD_ADAPTER。
