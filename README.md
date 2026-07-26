# La4RainGPT

面向少量固定用户的私人 AI 聊天界面。项目从 Open WebUI `v0.10.2`
fork，保留其成熟的聊天产品思路和视觉语言，运行时替换为轻量的
Svelte 静态前端、Go 单进程后端与 SQLite。

当前由两名普通用户和一名管理员共享一个 CPA Provider 账户。普通用户的
会话、消息和图片严格隔离；管理员可只读查看全部会话以便排障。浏览器永远
不会收到 CPA 密钥。

## 已实现

- 管理员创建用户；普通用户/管理员角色；Argon2id 密码与服务端 Session
- 用户自助改密/注销全部设备；管理员可停用、启用或重置账户
- 用户隔离的会话、消息、上传及生成图片
- CPA 动态模型目录、可搜索能力面板与 Conversation 级低/中/高推理强度，
  分别发送 `medium`/`high`/`max`
- OpenAI Responses SSE 文本与推理摘要
- Web Search 和 Image Generation 工具状态；中国本地实体优先使用中文
  查询与本土官方/地图/生活服务来源，并区分官方事实和用户评价
- PNG、JPEG、WebP 上传；生成图原质量落盘
- GPT 默认走 Responses `image_generation`；Grok 4.5 默认路由到 CPA
  `grok-imagine-image-quality` 的 `/v1/images/generations`
- Markdown、代码高亮/复制、KaTeX、引用来源；移动端表格、公式、代码和
  长链接在消息内部独立滚动或换行，不撑破页面
- 停止、失败重试、重新生成、置顶与七天临时留档/恢复
- 最新一条用户消息可编辑并重新发送；旧回答保留在时间线上
- 侧边栏对话搜索（标题与消息全文，附匹配片段；管理员覆盖全部用户）
- 输入框粘贴或拖拽上传图片；未发送草稿按对话自动保存在本浏览器
- 按月/模型聚合的用量统计（管理员含每用户明细）
- 流式语音朗读（阿里云/火山引擎 TTS），支持手动与自动模式
- PWA：manifest 与图标支持添加到主屏幕
- 每用户 3 GB 活跃附件空间、最多 30 个活跃会话和 10 个置顶会话；置顶
  会话不参与自动留档
- 应用管理的 Context Checkpoint
- 中文/English 界面、亮色/暗色/系统主题；首次登录提供可跳过、可重新
  打开的浏览器端新手指南
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
