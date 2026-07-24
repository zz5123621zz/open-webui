# Personal Chat Slim

面向少量固定用户的私人 AI 聊天界面。项目从 Open WebUI `v0.10.2`
fork，保留其成熟的聊天产品思路和视觉语言，运行时替换为轻量的
Svelte 静态前端、Go 单进程后端与 SQLite。

当前目标是两名用户共享一个 CPA Provider 账户，但会话、消息和图片严格
按应用用户隔离。浏览器永远不会收到 CPA 密钥。

## 已实现

- 管理员创建用户；Argon2id 密码与服务端 Session
- 用户自助改密/注销全部设备；管理员可停用、启用或重置账户
- 用户隔离的会话、消息、上传及生成图片
- CPA 动态模型目录、可搜索能力面板与 Conversation 级推理强度；新对话创建前即可选择强度
- OpenAI Responses SSE 文本与推理摘要
- Web Search 和 Image Generation 工具状态；上传图片与生成图片是两个明确模式
- PNG、JPEG、WebP 上传；生成图原质量落盘
- GPT 默认走 Responses `image_generation`；Grok 4.5 默认路由到 CPA
  `grok-imagine-image-quality` 的 `/v1/images/generations`
- Markdown、代码高亮/复制、KaTeX、引用来源
- 停止、失败重试、重新生成、归档/恢复
- 应用管理的 Context Checkpoint
- 中文/English 界面、亮色/暗色/系统主题
- 50 MiB CPA 请求硬边界；大请求通过临时文件编译
- 浏览器 SSE 每 15 秒心跳，并在断流后重新同步服务端消息终态
- 非 root、只读、`320m` 内存上限的 Docker Compose

## 本地检查

```bash
cd web
npm ci
npm run check
npm run build
```

Go 单元测试、竞态检查和 `govulncheck` 由 GitHub Actions 在独立 runner
执行，不在这台 1 GiB 生产 VPS 上运行。

生产部署不在 1 GiB VPS 上执行前端构建。推送 fork 后由
[镜像工作流](.github/workflows/image.yml) 构建并扫描镜像，再在 VPS
上固定版本或 digest 拉取。

部署步骤、密钥创建、Nginx 和备份见 [运维文档](docs/OPERATIONS.md)。
完整的本地/容器验证结果见 [验证报告](docs/VALIDATION.md)。
实现决策与资源验收基线保存在本 VPS 的
`/home/vpsadmin/OPENWEBUI_SLIM_FORK_DESIGN.md`。

## 安全边界

聊天数据不是端到端加密。应用用户互相不可见，但 VPS root 或服务运维者
可以读取 SQLite、图片与本机备份。异地备份必须在离开 VPS 前加密。

## 上游与许可证

本项目基于 [Open WebUI](https://github.com/open-webui/open-webui)。
原项目版权声明和许可证保留在 [LICENSE](LICENSE)。本实例设计为不超过
50 名终端用户，仍应在发布和吸收上游更新时复核许可证要求。
原始通知与许可证历史同时保留在 [LICENSE_NOTICE](LICENSE_NOTICE) 和
[LICENSE_HISTORY](LICENSE_HISTORY)。
