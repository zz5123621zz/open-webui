# 验证报告

验证日期：2026-07-24（UTC）

本报告区分“已在当前 VPS 完成的本地验证”和“需要 Docker/root、正式
CPA Credential 或 DNS 后才能完成的生产验收”。未完成项不会被记作通过。

## 已通过

| 范围 | 命令或方法 | 结果 |
|---|---|---|
| Go 单元/集成测试 | `go test ./...` | 通过 |
| Go 静态检查 | `go vet ./...` | 通过 |
| 数据竞争 | 对 jobs、store、provider、activecontext、httpapi 逐包执行 `go test -race` | 通过 |
| Go 漏洞 | `govulncheck v1.6.0 ./...` | 0 个可达漏洞 |
| 前端类型/可访问性 | `npm run check` | 0 error、0 warning |
| 前端生产构建 | `npm run build` | 通过 |
| npm 漏洞 | `npm audit --audit-level=moderate` | 0 vulnerabilities |
| 浏览器 E2E | Playwright Chromium，桌面和 iPhone 13 | 2/2 通过 |
| XSS | 恶意 `script` 与事件属性 Markdown fixture | DOM 中均被移除 |
| 用户隔离 | 会话、消息、上传和生成图片跨用户请求 | 返回 404 |
| CPA 请求边界 | 编译后 JSON 恰好等于上限、以及超过 1 字节 | 上限通过，超限拒绝 |
| 上下文安全线 | 80% 触发、90% 硬线、45/50 MiB、完整轮次与并发头 | 通过 |
| Compose 解析 | `docker compose config --format json` | 通过 |
| 备份脚本语法 | `sh -n deploy/backup.sh` | 通过 |
| CI 工作流 | `actionlint v1.7.7`；第三方 Action 固定到已核实 commit | 通过 |

E2E 覆盖登录、可搜索模型目录/能力标记/推理强度、中英切换、内部 SSE、
排队事件、带耗时的推理摘要、
Web Search 与 Image Generation 工具卡、生成图片附件加载、参数白名单、
引用、Context Checkpoint、Markdown/
代码/KaTeX、重新生成入口、账户安全、Fork 来源，以及桌面/手机布局。

## 内存实测

测试使用当前 release 方式构建的静态 Go 二进制，配置与 Compose 一致：
`GOMEMLIMIT=220MiB`、`GOGC=75`、每用户 2 路、全局 4 路。Provider 是独立
mock 进程，不计入应用 RSS；生成图是不可压缩像素组成的有效 PNG，应用未
改尺寸、未重编码、未降低质量。

| 场景 | 应用进程 RSS |
|---|---:|
| 空数据库、ready 后空闲 | 14,064 KiB（13.73 MiB） |
| 两用户共 4 路，含一张 7,084,249 字节生成图；冷态并含近期登录内存 | 峰值 147,388 KiB（143.93 MiB） |
| 相同 4 路拓扑热态复测，4/4 完成且生成图完整落盘 | 峰值 53.53 MiB |

冷态首轮中 mock TCP 意外断开过一路已产生正文的流；应用按设计没有自动
重试，而是保存稳定的 `provider_stream_error`。随后同拓扑复测 4/4 完成。
两轮峰值都明显低于 `320 MiB`，但它们是宿主机进程 RSS，不是
`docker stats`。Compose 解析确认硬限制为 `335,544,320` 字节；正式容器
内压力测试仍列在下面的待验收项。

## 当前外部阻塞

- `vpsadmin` 无权访问 `/var/run/docker.sock`，且 `sudo -n` 需要密码；
  因此不能在本轮启动真实容器、执行 `docker stats` 或安装 `/opt` 文件。
  Rootless Docker 的只读前置检查也因系统缺少需 root 安装的 `uidmap` 而失败。
- 仓库只有 `upstream` remote，没有用户 fork/origin；无法触发已写好的
  GitHub Actions、发布 GHCR 镜像或取得不可变 image digest。
- `chat.la4rain.com` 当前没有 DNS 解析，无法完成证书、Xray fallback 和
  Nginx HTTPS 验收。
- 正式 CPA Credential 和一个当前可选默认模型 ID 尚未写入本机 secret。
  无凭证探测确认 `https://cpa.la4rain.com/v1/models` 返回预期的 401，
  但不能替代鉴权后的模型、Web Search 和 Image Generation smoke test。
- GitHub 已连接身份为 `zz5123621zz`，但 App 尚未安装到任何仓库，该账号下
  没有本项目的可访问 fork；因此当前仍没有可推送的 `origin` 或可运行的
  镜像发布工作流。

## 上线前必须完成

1. 在 fork 的默认分支运行 CI，固定通过 Trivy 扫描的 GHCR digest。
2. 由有 root 权限的操作者创建 `/opt/owui-personal-slim`、写入两个 secret，
   启动 Compose 并创建两名正式用户。
3. 用正式 CPA 账户验证增强 `/v1/models`、默认模型、推理等级、
   `web_search` 引用和 `image_generation_call.result`。
4. 在真实容器中执行两用户四路负载，确认 `docker stats` 峰值小于
   `320 MiB`，同时观察 CPA、Nginx、Xray 和整机余量。
5. 配置 DNS、证书和 Xray fallback，执行 `nginx -t` 与外网 SSE/上传测试。
6. 创建 age 加密备份并在隔离目录完成一次恢复演练；记录回滚 digest。
7. 上线后连续观察 7 天，确认没有 OOM、用户隔离或数据一致性问题。
