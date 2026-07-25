# 生产验证报告

验证日期：2026-07-25（UTC）

生产入口：`https://chat.la4rain.com`

当前固定镜像：

- La4RainGPT：
  `ghcr.io/zz5123621zz/owui-personal-slim@sha256:a2c5508503d66c7e92bd82c9cc2c75bd1213c663cfe9eefefa4b3c88fdd9d319`
- CPA：
  `ghcr.io/zz5123621zz/cliproxyapi-la4rain@sha256:d9955c99fe69b62479c2c41cd25223a3c018c7ec34fc6097bce80aac4b90560e`

本报告只把 GitHub Actions、当前 VPS 配置或真实 CPA 请求已经证明的项目记为
通过。VPS 没有运行 Go 测试、Go 编译、前端构建或 Playwright。

## 1. CI、PR 与镜像

### La4RainGPT

- 实现由 [PR #10](https://github.com/zz5123621zz/open-webui/pull/10)
  合并，merge commit 为
  `5232fa0e291bdf31bfa1063191de479d6b68b351`。
- PR 最终验证运行
  [30143271074](https://github.com/zz5123621zz/open-webui/actions/runs/30143271074)
  全部通过。
- `main` 发布运行
  [30143424312](https://github.com/zz5123621zz/open-webui/actions/runs/30143424312)
  全部通过并发布上述固定 digest。

| 检查 | 结果 |
|---|---|
| `npm ci`、audit、Svelte 检查与生产构建 | 通过 |
| Playwright Chromium 桌面与 375 px 手机 E2E | 通过 |
| `go test ./...`、`go vet ./...` | 通过 |
| `go test -race ./...` | 通过 |
| `govulncheck v1.6.0 ./...` | 0 个可达漏洞 |
| Docker 多阶段 AMD64 构建与 GHCR 推送 | 通过 |
| Trivy HIGH/CRITICAL 扫描与 SARIF 上传 | 通过 |

Docker Actions 当前有 GitHub Node.js 20 弃用提示，但运行器已强制使用 Node.js
24，未影响本次构建或扫描；后续维护时应升级相应固定 action commit。

### CPA 最小兼容层

- fork 固定在上游 `v7.2.96` commit
  `285322cd97add6b21f60c267debec44fbec74060`。
- 实现 commit 为
  `99f2204f19069df74cdc7539d7828e34a0623071`，由
  [CPA PR #1](https://github.com/zz5123621zz/CLIProxyAPI/pull/1)
  合并到固定基线分支。
- 专用 CI
  [30142810073](https://github.com/zz5123621zz/CLIProxyAPI/actions/runs/30142810073)
  的单元测试、AMD64 构建、GHCR 推送和 Trivy 扫描通过。
- 上游原有 PR build、translator path guard 和 `AGENTS.md` guard 同样通过。

兼容改动只允许原始 OpenAI Responses 请求中的精确值
`stream_options.reasoning_summary_delivery = "sequential_cutoff"`；其他键、
错误值、配置注入值和非 Responses 来源都不会被恢复。Count Tokens 路径仍
删除全部 `stream_options`。

## 2. 分阶段发布与回滚点

发布严格按既定顺序执行：

1. CPA CI 和固定 digest 完成；
2. 保留现有密钥、账号目录、模型配置和额度来源，替换 CPA 镜像；
3. CPA `/v1/models` 返回 HTTP 200；
4. La4RainGPT `main` CI 和固定 digest 完成；
5. 发布前创建 age 加密备份；
6. 应用以部署级硬关闭启动；
7. 通过管理员 API 持久化 `off`；
8. 解除硬关闭并重启一次，确认仍为 `off`；
9. 普通聊天通过后，不重启切换为 `auto`；
10. 真实联网请求完成渐进摘要验收；
11. 删除验收会话、注销验收 Session，再创建发布后加密备份。

发布前备份：
`/opt/owui-personal-slim/encrypted-backups/chat-20260725T041044Z.tar.gz.age`

发布后备份：
`/opt/owui-personal-slim/encrypted-backups/chat-20260725T043302Z.tar.gz.age`

两次备份均由 systemd service 返回 `Result=success`，密文约 3.4 MiB、权限
0600。每日备份 timer 保持 active。

回滚目标：

- 应用旧 digest：
  `sha256:7f0dbc001fc25e186873dc47d8a3119c7719dd3d4f4523b2ead649a02693a481`
- 应用旧配置：
  `/opt/owui-personal-slim/.env.pre-progressive-20260725T0410Z` 和
  `compose.yaml.pre-progressive-20260725T0410Z`
- CPA 官方旧 digest：
  `sha256:65cdeb08c5724a11e82ad8343ab0bfda3494b2201012d2b52d5d839b100c782c`
- CPA 旧 Compose：
  `/root/cpa/docker-compose.yml.pre-la4rain-20260725T0400Z`

最快止损不需要镜像回滚：管理员先把渐进摘要切换为 `off`；管理员页面不可用
时才使用 `AI_PROGRESSIVE_SUMMARY_HARD_DISABLED=true` 并重启应用。

## 3. 当前生产状态

| 检查 | 结果 |
|---|---|
| Compose preflight | 全部通过 |
| 应用容器 | `healthy`，restart count 0 |
| CPA 容器 | running，restart count 0 |
| 应用和 CPA `memory.events` | `oom=0`、`oom_kill=0` |
| 本地 `/readyz` | HTTP 200 |
| Cloudflare 公网首页 | HTTP/2 200，TLS 校验通过 |
| 应用监听 | 仅 `127.0.0.1:3001` |
| 应用硬上限 | 320 MiB |
| CPA 硬上限 | 256 MiB |
| 应用近 20 分钟 ERROR 日志 | 0 |
| CPA 本次部署后 panic/fatal/error 日志 | 0 |
| 加密备份 timer | active |

公网响应包含 CSP、`X-Frame-Options: DENY`、
`X-Content-Type-Options: nosniff`、Referrer Policy 和 Permissions Policy。
Compose 继续使用只读根文件系统、删除全部 Linux capabilities、
`no-new-privileges`、100 PID 上限和 10 MiB × 3 日志轮转。

CPA 启动日志确认：

```text
CLIProxyAPI Version: v7.2.96-la4rain
Commit: 99f2204f19069df74cdc7539d7828e34a0623071
API server started successfully on: 127.0.0.1:8317
```

应用启动日志确认版本
`main-5232fa0e291bdf31bfa1063191de479d6b68b351`。

## 4. 用户、模型与固定边界

当前数据库中的 active 账户：

| 用户名 | 角色 |
|---|---|
| `admin` | administrator |
| `laochen` | user |
| `rainsaa` | user |

管理员只读查看其他用户会话、普通用户隔离和管理员不能修改其他用户会话由
本次全绿的 Go 集成测试覆盖。生产没有开放注册接口。
Provider Credential 仍只由后端从 0400、`65532:65532` 的 Docker secret
读取，不位于浏览器 API 或容器环境变量中。

真实模型目录返回 5 个模型：

- `gpt-5.6-sol`
- `gpt-5.6-terra`
- `gpt-5.6-luna`
- `grok-composer-2.5-fast`
- `grok-4.5`

默认模型为 `gpt-5.6-sol`，默认推理强度为 `high`。运行中配置确认：

- 每用户同时运行最多 2 个 Provider 请求；
- 全应用同时运行最多 4 个；
- 每用户最多排队 2 个；
- CPA 请求体上限 52,428,800 bytes（50 MiB）；
- 每用户 3 GiB 活跃附件、30 个活跃会话、10 个置顶会话；
- 临时留档 168 小时。

本次没有用四路图片或其他高成本请求制造极端并发。并发数值由运行容器环境
确认，调度、排队、公平释放和同会话互斥由云端 Go 测试覆盖。

## 5. 管理员开关与兼容状态

实际状态变化：

| 阶段 | 持久模式 | 硬关闭 | 有效状态 |
|---|---|---|---|
| 首次迁移启动 | `auto` | `true` | `disabled` |
| 管理员写入关闭 | `off` | `true` | `disabled` |
| 解除硬关闭并重启 | `off` | `false` | `disabled` |
| 管理员在线切换 | `auto` | `false` | `unknown` |
| 下一次正常聊天探针后 | `auto` | `false` | `active` |

重启后 `off` 保持不变，证明配置持久化；从 `off` 切到 `auto` 未重启应用或
CPA。最终生产状态为：

```text
mode=auto
hardDisabled=false
effectiveState=active
model=gpt-5.6-sol
modelState=active
```

简单计算探针被 CPA 接受并一次完成，但 Provider 没有为该简单请求生成推理
摘要。这是预期边界：`active` 表示实验字段已被接受，不保证每个问题都会产生
语义摘要。

生产没有已知会明确拒绝该字段的模型，因此没有人为伪造 HTTP 400。精确字段
拒绝、只降级一次、30 分钟冷却、单探针以及成功 HTTP/SSE 后绝不自动重发，
由 PR 和 `main` 的确定性模拟 CPA 测试覆盖。

## 6. 真实渐进摘要、搜索与 SSE

所有请求均经过公网 Cloudflare、Xray、Nginx、La4RainGPT 和真实 CPA。

### 摘要关闭基线

提示明确要求不联网、不调用工具：

| 指标 | 结果 |
|---|---:|
| HTTP | 200 |
| 首个 SSE 字节 | 0.139 秒 |
| 完成 | 2.384 秒 |
| 终止事件 | 唯一 `response.completed` |
| 错误事件 | 0 |
| 数据库状态 | user/assistant 均为 `completed` |

### 自动模式简单探针

| 指标 | 结果 |
|---|---:|
| HTTP | 200 |
| 首个 SSE 字节 | 0.114 秒 |
| 完成 | 5.224 秒 |
| 自动重试 | 0 |
| 推理摘要段 | Provider 本次未生成 |
| 兼容状态 | `unknown` → `active` |

### 自动模式联网验收

提示明确要求搜索 OpenAI 官方资料。首次事件到达时间：

| 事件 | 首次到达 |
|---|---:|
| `response.started` | 0.125 秒 |
| 事实阶段状态 | 0.131 秒 |
| 网页搜索/访问事件 | 3.532 秒 |
| 第一段推理摘要 delta | 19.041 秒 |
| 第一段推理摘要 done | 21.646 秒 |
| 正文 delta | 33.478 秒 |
| `response.completed` | 约 37.85 秒 |

结果证明搜索状态和安全摘要在正文前到达；用户不再需要只看约半分钟的静态
“正在搜索”。完整流包含：

- 25 个工具增量事件，去重后保存为 5 个 completed 搜索/访问阶段；
- 2 个 `response.reasoning.delta` 和 2 个
  `response.reasoning.done`；
- 两段摘要使用不同 Provider item ID，`summaryIndex=0`，分别保存为两张卡；
- 46 个正文 delta；
- 唯一 `response.completed`，无 `response.error`；
- curl、SSE reader 和 JSON 生成管线退出码均为 0。

刷新后从数据库读取到同样的 5 个工具阶段、2 个独立 reasoning part、正文和
引用，证明不是只存在于浏览器内存中的临时动画。每段 reasoning 均标记
`completed=true`，并保存实际持续时间。

## 7. 网页证据清洗

真实联网验收持久化了：

- 2 个搜索词；
- 3 个打开页面动作；
- 2 条引用；
- URL 主机仅为 `openai.com` 和 `platform.openai.com`。

对 5 个持久化 URL 的自动检查结果：

| 检查 | 结果 |
|---|---:|
| 非 HTTP(S) | 0 |
| URL 用户名/密码 | 0 |
| fragment | 0 |
| `utm_*`、`fbclid`、`gclid` | 0 |
| token、API key、signature、session 参数 | 0 |
| Header、Cookie、Authorization 键 | 0 |
| `encrypted_content` | 0 |

搜索词、访问网页域名、推理摘要和引用均在删除验收会话前通过 API 从持久化
记录重新读取。三个验收会话随后全部通过拥有者 API 删除，活跃列表中为 0；
验收 Session 已注销，root 临时 Cookie 和响应文件已删除。

## 8. 内存

真实容器 cgroup / Docker 快照：

| 容器与场景 | `memory.current` | `memory.peak` | 硬上限 |
|---|---:|---:|---:|
| La4RainGPT：文本、搜索、两段摘要后 | 8.42 MiB | 12.36 MiB | 320 MiB |
| La4RainGPT：再执行一次 Argon2 管理员登录 | 约 72.37 MiB | 72.62 MiB | 320 MiB |
| CPA 兼容镜像 | 39.22 MiB（Docker 快照） | 56.45 MiB | 256 MiB |
| CPA Manager Plus | 34.04 MiB（Docker 快照） | 110.57 MiB | 160 MiB |

Manager Plus 的 peak 是其较长容器生命周期峰值，不代表三个峰值同时发生。
La4RainGPT 的代表性峰值 72.62 MiB，明显低于 0.4 GiB 目标和自身 320 MiB
硬上限。验收期间没有 OOM、容器重启、数据库锁或 SSE 中断。

## 9. 未重复的既有功能基线

2026-07-24 的上一生产镜像已真实通过 PNG 上传/视觉输入、Web Search 和默认
`auto` 质量图片生成；生成 PNG 为 993,359 bytes，当时包含图片生成和
Argon2 登录的应用峰值为 162.65 MiB。

本次发布没有再次消耗额度生成图片，也没有构造极端图片并发。当前改动没有
设置 `partial_images`，没有覆盖 `quality`、`size`、`background`、
`output_format` 或压缩参数；图片仍由 CPA 使用 `auto` 默认值。图片、上传、
搜索、上下文压缩和历史恢复回归由本次全绿的 Go 集成测试与 Playwright
覆盖。

## 10. 持续观察

上线后的持续运维仍需关注 OOM、重启、SQLite 锁、磁盘增长、证书续期、
备份 timer、CPA 错误率和 GitHub Actions 的 Node.js action 弃用提示。
这些观察项不改变当前管理员即时关闭、部署级硬关闭以及固定旧 digest 的
回滚能力。

既有备份运维待办仍然有效：age 私钥必须另存到 VPS 之外；在配置
`RCLONE_REMOTE` 前，加密归档不会自动复制到异地。
