# 生产验证报告

验证日期：2026-07-24（UTC）

生产入口：`https://chat.la4rain.com`
生产镜像：
`ghcr.io/zz5123621zz/owui-personal-slim@sha256:77b4e2ad51d0f62d5b17b845b92f8b5ff5bbcb9833a6e2f02df44b4ddf5ba21d`

本报告只把已经由 CI、当前 VPS 配置或真实 CPA 请求证明的项目记为通过。

## CI 与镜像

GitHub Actions 在 PR 和 `main` 上执行，VPS 没有运行 Go 测试、Go 编译或
前端构建：

| 范围 | 结果 |
|---|---|
| `npm ci`、audit、类型检查、生产构建 | 通过 |
| Playwright Chromium 桌面与手机 E2E | 通过 |
| `go test ./...`、`go vet ./...` | 通过 |
| `go test -race ./...` | 通过 |
| `govulncheck v1.6.0 ./...` | 0 个可达漏洞 |
| 多阶段 Docker 构建与 GHCR 推送 | 通过 |
| Trivy HIGH/CRITICAL 扫描和 SARIF 上传 | 通过 |

初始实现由 PR #1 合并。CPA 图片完成状态兼容修复由 PR #2 合并；其
功能验收镜像为
`sha256:e1b6c062006eba8fb2efb5f9e410fce2d4353d07c578fbf7b9a069bca0702683`。
备份运维和本报告由 PR #3 合并，最终 `main` 发布运行 `30106660479`
全部成功并部署为上述生产 digest。PR #3 没有修改运行时 Go 或前端源码。
第三方 Actions 均固定到 commit。

## VPS 部署

| 检查 | 生产结果 |
|---|---|
| Compose 预检 | 全部通过 |
| 容器健康 | `healthy` |
| `/readyz` | HTTP 200 |
| 公网首页 | HTTP/2 200 |
| 镜像固定 | 上述不可变 digest |
| 监听地址 | 仅 `127.0.0.1:3001` |
| 运行用户 | `65532:65532` |
| 根文件系统 | read-only |
| Linux capabilities | 全部删除 |
| PID 上限 | 100 |
| 内存硬上限 | 335,544,320 bytes（320 MiB） |
| Docker 日志轮转 | 10 MiB × 3 |
| Nginx / Xray TLS fallback | 公网 HTTPS 和 SSE 实测通过 |
| 证书续期 | Certbot dry-run 与 Nginx reload hook 通过 |

公网响应已观察到 CSP、`X-Frame-Options: DENY`、
`X-Content-Type-Options: nosniff`、Referrer Policy 和 Permissions Policy。

## 双用户、模型与 CPA

生产数据库只创建了管理员提供的固定账户 `owner` 与 `partner`，没有注册
接口。两人的随机初始密码保存在 VPS root-only 文件中，登录后可自行修改。

真实生产验收结果：

| 场景 | 结果 |
|---|---|
| CPA 增强模型目录 | 5 个可选模型 |
| 默认模型 | `gpt-5.6-sol` 可选 |
| 默认推理强度 | `high` 受默认模型支持 |
| owner 会话出现在 owner 列表 | 1 |
| 同一会话出现在 partner 列表 | 0 |
| partner 直接读取 owner 会话 | HTTP 404 |
| partner 直接读取 owner 上传图片 | HTTP 404 |
| partner 直接读取 owner 生成图片 | HTTP 404 |

Provider Credential 只由 Go 后端从 Docker secret 读取，模型目录和浏览器
API 均不返回该值。

## 聊天、图片与工具

所有以下请求都经过公网 Cloudflare、Xray、Nginx 和应用容器，而不是直接
访问容器端口：

| 场景 | 生产结果 |
|---|---|
| 文本 Responses SSE | 3 秒完成；收到 started、text delta、completed |
| 推理摘要 | 真实视觉请求保存 `reasoning` part |
| PNG 上传与视觉输入 | 上传、CPA 视觉理解和消息持久化通过 |
| Web Search | 收到 tool 事件；保存 tool、text、citations |
| 图像生成 | 57 秒完成；收到 tool、reasoning、image、completed |
| 最终生成图片 | 有效 `image/png`，993,359 bytes |
| 图像工具状态 | `completed` |
| 测试数据清理 | 探针对话及附件均通过拥有者 API 删除 |

应用没有发送 `partial_images`，也没有覆盖 `quality`、`size`、`background`、
`output_format` 或压缩参数；图片使用 Provider 的 `auto` 默认值。CPA 会在
最终 `response.output_item.done` 中保留过渡态 `generating`，适配器现以
done 生命周期为准并保存同一条目中的最终 Base64 result。

## 内存

真实容器 cgroup `memory.peak`：

| 生产场景 | 峰值 |
|---|---:|
| 登录、动态模型、文本 SSE 与隔离探针 | 140.11 MiB |
| 登录、默认质量图像生成、Base64 解码、落盘与跨用户检查 | 162.65 MiB |

两个结果均包含 Argon2id 登录的瞬时内存，且明显低于 320 MiB 硬上限。
图像生成完成后的 `memory.current` 为 162.15 MiB。正常验收没有发生 OOM、
容器重启、数据库锁死或 SSE 中断。

按产品约束，本次没有用四路最大图片构造极端并发，也没有降低图片质量。

## 加密备份与恢复

`owui-personal-slim-backup.timer` 已启用，每天 UTC 03:17 执行并带最多
30 分钟随机延迟。首次 service 运行成功：

- SQLite Online Backup API 创建一致性快照；
- 数据库、uploads 和 generated 进入 age 加密归档；
- 明文数据库快照随后删除；
- 密文权限为 0600，本机只保留最近 7 天；
- age identity 为 root-only 0400；
- systemd 环境文件为 root-only 0600。

恢复演练在 `/var/tmp` 独立目录和临时容器中完成：密文成功解密，快照作为
`app.db` 启动同一固定镜像，`/readyz` 返回 200，恢复后的 `owner` 登录成功，
并读取到 5 个 CPA 模型。临时容器和明文恢复目录随后删除，生产容器保持
`healthy`。

仍需由管理员把 age 私钥复制到 VPS 之外的安全位置。当前没有配置
`RCLONE_REMOTE`，因此密文尚未自动上传到异地；配置 rclone remote 后重跑
`deploy/setup-backup-root.sh` 即可启用。

## 后续观察

生产功能和正常负载验收已完成。上线后的 7 天稳定性观察属于持续运维：
关注 OOM、容器重启、SQLite 锁、磁盘增长、证书续期、备份 timer 和 CPA
错误率；它不改变当前固定 digest 和回滚 digest 的可用性。
