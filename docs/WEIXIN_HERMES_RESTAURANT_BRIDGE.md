# 微信 + Hermes 餐饮问答桥接实施与验收方案

| 项目 | 内容 |
|---|---|
| 状态 | 实施合同；完成情况以第 14 节测试记录为准 |
| 日期 | 2026-07-28 |
| 最后验收记录 | 2026-07-29 |
| Hermes 基线 | 0.19.0，构建提交 `8a71feb84ca20d92908ab95a45f7fb39fd376b26` |
| slim 基线 | 本仓库提交 `b15edece2d89eb580781ea901a179571d39fb4c5` |
| 本轮范围 | 开发与完整测试，不部署 |

关联记录：

- [领域语言](../CONTEXT.md)
- [ADR 0015：微信餐饮业务状态由 slim 统一持有](adr/0015-keep-weixin-business-state-in-slim.md)
- [现有餐饮澄清方案](RESTAURANT_WORKFLOW_PLAN.md)
- [现有 TTS 后端合同](TTS_BACKEND.md)

## 1. 已确认目标

微信私聊中的餐饮用户获得以下流程：

1. 用户发送普通文字，例如“为我设计 20 道菜品”。
2. Hermes 立即维持微信内置的“对方正在输入”状态。
3. slim 使用现有餐饮工作台和 CPA 模型判断是否需要澄清。
4. 需要澄清时，一轮必须输出恰好三道纯文字问题；每题选项独立使用
   `A`～`D` 编号。
5. 用户可以回复 `ABC`、`1A 2B 3C`、三个逗号分隔的自然语言答案，
   或普通自然语言。用户可随时回复“直接生成”停止澄清。
6. 最终答案完成后，先发送完整文字，再自动发送一份或多份可播放 WAV
   音频附件。

本方案所称“对方正在输入”是 Agent 一侧向微信发送的 typing 状态，不是读取
用户是否正在输入。

## 2. 明确不承诺

- 不承诺微信客户端自动播放音频。当前腾讯 iLink 消息载荷和 Hermes Weixin
  适配器没有发送端 `autoplay` 控制字段。
- 不把 WAV 文件宣称为微信原生语音气泡。Hermes 0.19.0 的
  `WeixinAdapter.send_voice` 明确降级为文件附件，因为上游尚未证明原生
  Weixin voice bubble 可靠。
- 不提供按钮、单选框或小程序卡片。当前个人微信链路是
  `WeixinAdapter → 腾讯 iLink Bot API`，不是微信小程序。
- 不显示模型思考、检索或工具进度。本通道只使用临时 typing 状态。
- 本轮不修改正在运行的容器、生产配置、Nginx、数据库或 Hermes Profile。

上述边界以 Hermes 0.19.0 源码为准：

- typing：
  <https://github.com/NousResearch/hermes-agent/blob/8a71feb84ca20d92908ab95a45f7fb39fd376b26/gateway/platforms/weixin.py#L1908-L1979>
- voice 附件降级：
  <https://github.com/NousResearch/hermes-agent/blob/8a71feb84ca20d92908ab95a45f7fb39fd376b26/gateway/platforms/weixin.py#L2060-L2085>
- Weixin 不支持消息编辑：
  <https://github.com/NousResearch/hermes-agent/blob/8a71feb84ca20d92908ab95a45f7fb39fd376b26/gateway/run.py#L21330-L21337>

## 3. 架构与责任边界

```text
微信私聊
  → Hermes WeixinAdapter
  → restaurant Profile 中的桥接插件
      ├─ 检查 Hermes 已授权用户
      ├─ 开始/刷新“对方正在输入”
      └─ 调用 slim Bridge API
          → 桥接凭据绑定唯一 slim User
          → Hermes session_id 映射唯一 slim Conversation
          → 现有餐饮澄清状态机 + CPA
          → 最终答案文字
          → 现有 Speech Provider 生成一个或多个 WAV
  ← Hermes 先发文字，再依次发 WAV 附件
```

slim 是以下数据的唯一事实来源：

- 用户及餐饮工作台；
- 餐厅档案；
- 会话和消息历史；
- 三轮澄清上限；
- Provider 模型与推理强度；
- 配额、审计及响应生命周期；
- TTS Provider、音色和语速；
- 临时音频文件及其到期时间。

Hermes 只负责：

- 微信鉴权/配对；
- 按 `chat_id` 路由到隔离 Profile；
- typing；
- 调用 Bridge API；
- 原样发送 slim 返回的文字；
- 下载并顺序发送 slim 返回的音频。

Hermes 不直接读写 slim SQLite。

## 4. 多用户隔离

一个 Hermes Profile 使用一个独立 Bridge credential；一个 credential 只绑定
一个 slim User。请求不能携带或覆盖 `user_id`。即使调用方知道其他用户 ID，
服务端也只能访问凭据绑定的用户。

推荐路由：

```yaml
gateway:
  multiplex_profiles: true
  profile_routes:
    - name: father-restaurant
      platform: weixin
      chat_id: "<父亲的 chat_id>"
      profile: father-restaurant
```

`father-restaurant/.env` 只保存该 Profile 的
`SLIM_RESTAURANT_BRIDGE_TOKEN`。VPS 运维用户使用另一个 Profile、提示词、
Skills、Tools 和 credential。

## 5. 服务端 API

### 5.1 执行一轮

`POST /api/v1/integrations/hermes/restaurant/turn`

请求头：

```http
Authorization: Bearer hbr_<一次性显示的随机密钥>
Content-Type: application/json
```

请求：

```json
{
  "requestId": "Hermes 插件为本条微信消息生成的稳定 ID",
  "sessionId": "Hermes 当前 session_id",
  "text": "用户原始微信文字"
}
```

限制：

- `requestId` 和 `sessionId` 必填、UTF-8、最长 128 字节；
- `text` 去除首尾空白后必填，最大 64 KiB；
- 仅接受 JSON 中已声明字段；
- 同一 credential 同时只运行一轮；
- 微信事件有 `message_id` 时，插件用 Profile 身份摘要、`chat_id` 和
  `message_id` 生成稳定的 SHA-256 `requestId`；同一消息的网络重试复用该
  ID。只有上游事件缺少 `message_id` 时才生成随机 UUID；
- 服务端在 credential 作用域内把 `requestId` 与 `sessionId`、去除首尾
  空白后的 `text` 指纹绑定。完全相同的重试恢复已有结果，不产生第二次
  Provider 回答；同一 `requestId` 若改换 session 或正文，返回
  `409 request_id_reused`。

成功响应：

```json
{
  "requestId": "...",
  "kind": "clarification | task_brief | answer",
  "text": "应原样发给微信的完整文字",
  "audio": {
    "status": "not_applicable | ready | unavailable",
    "code": "",
    "files": [
      {
        "id": "...",
        "fileName": "answer-01-of-02.wav",
        "contentType": "audio/wav",
        "byteSize": 123456,
        "downloadPath": "/api/v1/integrations/hermes/restaurant/audio/..."
      }
    ]
  }
}
```

`clarification` 和 `task_brief` 不生成语音。只有 `answer` 自动生成语音。

### 5.2 下载音频

`GET /api/v1/integrations/hermes/restaurant/audio/{id}`

- 使用与 turn 相同的 Bearer credential；
- 只能下载该 credential 所绑定用户自己的未过期文件；
- 返回 `Content-Type: audio/wav`、`Content-Length` 和
  `Content-Disposition: attachment`；
- 文件默认保留 24 小时，由维护任务清理；
- 不提供公开 URL。

## 6. 桥接凭据

通过服务端 CLI 签发，不复用用户密码、浏览器 Cookie 或全局应用密钥：

```bash
/app/server integration hermes-token issue \
  --username laochen \
  --label father-restaurant \
  --model gpt-5.6-sol \
  --reasoning-effort high
```

原始 token 只显示一次；SQLite 只保存 SHA-256 摘要。CLI 还必须支持：

```bash
/app/server integration hermes-token list
/app/server integration hermes-token revoke --id <credential-id>
```

被禁用的 slim User、被撤销的 credential、非餐饮工作台用户均不能调用 Bridge
API。

## 7. 三题一轮的文字协议

微信专用工具合同把问题数组限制为 `minItems=3, maxItems=3`，并在服务端再次
校验。网页端继续允许每轮 2～3 题。

格式示例：

```text
需求澄清 · 第 1/3 轮

1. 您想设计什么类型的菜品？
   A. 中式复古
   B. 西餐
   C. 家常菜
   D. 你帮我决定

2. 单道菜希望控制在什么价格？
   A. 20 元以内
   B. 20～30 元
   C. 30～50 元
   D. 你帮我决定

3. 这批菜主要用于什么场景？
   A. 日常散客
   B. 家庭聚餐
   C. 商务宴请
   D. 你帮我决定

请一次回复三题，例如：ABC、1A 2B 3C，
或“复古，30 元左右，家常菜”。
想继续完善就直接回答；信息已够时可回复“直接生成”。
```

解析规则：

1. `ABC`：依次映射第 1、2、3 题的 A、B、C。
2. `1A 2B 3C`：按显式题号映射；多选题可写 `2AC`。
3. 三段逗号、分号或换行分隔文本：依次作为三题答案；与选项标签完全匹配时
   保存选项，否则保存为“其他”文本。
4. 不能无歧义解析时，保留用户原文交给现有对话模型理解，不伪造结构化选择。
5. 文本包含“直接生成”“按当前信息生成”或“不再追问”时，本轮强制进入最终
   回答；已有可解析答案仍保留。
6. `task_brief` 后回复“确认生成”视为仅用于本次任务，不静默保存长期餐厅档案。

## 8. typing 合同

插件只在满足以下全部条件时接管消息：

- 平台是 `weixin`；
- 是私聊；
- 当前 Hermes Profile 配置了 Bridge token；
- `gateway._is_user_authorized(source)` 已确认用户通过 Hermes 配对/允许列表；
- 消息不是 Hermes slash command。

插件创建后台任务后立即让原消息分发返回 `skip`，并每两秒刷新 typing。无论
成功、错误、取消或超时，都必须在 `finally` 中停止 typing。第二条并发消息不
进入同一 slim 会话并发生成，而是收到“上一条仍在处理中”的明确提示。

## 9. 最终文字

- 只拼接最终 assistant message 的 `text` part；
- 不朗读、不发送 reasoning summary、工具状态、内部 JSON 或隐藏提示词；
- `clarification` 和 `task_brief` 使用服务端确定性渲染，不让 Hermes 模型改写；
- Hermes 插件必须原样发送 `text`，不得二次总结。

## 10. TTS 与音频

1. 使用 slim 当前启用的 Speech Provider 和该用户的有效音色、语速。
2. 微信“自动附带音频”独立于网页端的 manual/auto 播放偏好；浏览器偏好不
   能阻止 Bridge 在最终答案后生成附件。
3. Markdown 先转换成 `Spoken answer text`：保留可读标签，去除 URL、邮箱、
   HTML、代码块内容和 Markdown 符号。
4. 在自然句子边界拆为多个有序文本段；每个文件使用独立 Provider session。
5. Provider 返回的 24 kHz、16-bit、mono PCM 封装成标准 RIFF/WAVE 文件。
   若 Provider 返回不受支持的格式，拒绝伪造文件。
6. 文件先写入应用数据目录中的临时文件，完整写入并 `fsync` 后原子改名。
7. 音频全部生成成功才返回 `ready`。任意分段失败时删除本轮已生成文件并返回
   `unavailable`，不能发送不完整语音冒充完整语音。
8. 服务端硬限制为最多 32 段、单段最多 25 MiB、整轮最多 100 MiB；超过限制
   时保留完整文字并把语音降级为 `unavailable`。Hermes 插件使用相同默认
   限制，并同时校验响应元数据和实际下载字节数。

### 10.1 故障降级

最终文字是主结果。以下情况返回 HTTP 200、`kind=answer`、完整 `text`，
但 `audio.status=unavailable`：

- 管理员未启用语音；
- Provider 未配置或鉴权失败；
- 音色与模型不匹配；
- TTS 并发已满；
- PCM/WAV 格式异常；
- 文件写入失败。

Hermes 先发送完整文字，再发送一条简短的“语音暂不可用”提示。不得因为 TTS
故障重新调用 CPA，也不得丢弃或重复最终文字。

## 11. Hermes 插件

仓库提供可复制到 Profile 插件目录的 Python 插件。它只使用 Python 标准库和
Hermes 0.19.0 的公开插件注册入口，加上本版本已有的 gateway adapter 方法。

插件处理顺序：

1. 同步 hook 做平台、私聊、授权、slash command 和配置检查；
2. 创建异步处理任务并返回 `skip`；
3. 取得或创建 Hermes session，使用其 `session_id` 调用 slim；
4. 维持 typing；
5. 原样发送 `text`；
6. 用相同 Bearer token 下载所有 WAV 到当前 Profile 的 media 目录；
7. 按响应顺序调用 Weixin `send_voice`；
8. 删除本地临时 WAV；
9. 停止 typing。

HTTP 对瞬时网络错误最多重试两次，始终复用同一 `requestId`。服务端
`turn_in_progress` 可按 `Retry-After` 轮询，但 4xx 鉴权或校验错误不重试。

本机当前 Hermes 容器使用 host 网络，因此部署时 Bridge URL 是
`http://127.0.0.1:3001`。插件在 `plugins.enabled` 中的启用名必须使用
manifest 的 `slim-restaurant-bridge`，不是 Python 目录名。这里只记录部署
合同；本轮不复制、启用或重启插件。

## 12. 安全不变量

- 日志不得记录 Bearer token、用户原文全文或音频内容。
- credential 与 User 是服务端绑定关系，请求体没有 `userId`。
- 音频下载必须同时验证 credential、User、文件记录和到期时间。
- 下载路径来自数据库受控相对路径，服务端必须验证最终路径仍位于专用音频目录。
- Hermes 下载文件名由响应 `id` 和固定 `.wav` 组成，不使用服务端提供的任意
  路径。
- Bridge API 不接受浏览器 Cookie 代替 Bearer token。
- Bridge 不绕过现有 Provider 并发、会话上限、餐饮工作台或指导轮次限制。

## 13. 测试矩阵

本节的 `[x]` 表示对应测试曾实际通过。2026-07-29 起，项目根目录
[AGENTS.md](../AGENTS.md) 永久禁止在这台约 1 GiB VPS 直接或间接执行
`go test`、Go 编译或浏览器 E2E；GitHub-hosted runner 是资源密集型完整
验证的唯一例外。任何其他本地编译器、测试运行器或容器启动前仍须评估内存、
swap 和峰值余量，无法确认安全余量时必须不运行。

### 13.1 Go 单元测试

- [x] 微信固定三题：2 题和 4 题均拒绝，3 题接受。
- [x] 三题文字渲染：题号、A～D、回复示例、直接生成指引完整。
- [x] `ABC`、小写、空格、`1A 2B 3C`、多选 `2AC`。
- [x] 三段中文逗号、英文逗号、分号、换行自然语言。
- [x] 无效字母、缺题、重复题号、超出选项时不伪造答案。
- [x] “直接生成”单独出现及与答案同时出现。
- [x] task brief 的确认、补充和档案仅本次规则。
- [x] token 随机性、只存摘要、鉴权、撤销、禁用用户、跨用户拒绝。
- [x] Hermes session 到 Conversation 的稳定映射及新 session 隔离。
- [x] 同一 requestId 的幂等恢复及不同输入复用时拒绝。
- [x] Markdown→spoken text 清洗。
- [x] 句子边界分段、UTF-8 和超长单句。
- [x] PCM→WAV 头、长度、采样率、声道、位深。
- [x] TTS 多段顺序、任一段失败时整轮清理。
- [x] 音频到期、越权下载、路径逃逸及清理。

### 13.2 Go HTTP 集成测试

- [x] 无 Bearer、错误 Bearer、撤销 Bearer。
- [x] 非餐饮工作台与功能开关关闭。
- [x] 首问→恰好三题 clarification。
- [x] `ABC`→下一轮或 task brief。
- [x] 自然语言三答案→结构化历史。
- [x] 任意时刻“直接生成”→完整 answer。
- [x] answer→一份 WAV。
- [x] 长 answer→多份有序 WAV。
- [x] TTS 关闭/失败→完整文字 + unavailable，不再调用 CPA。
- [x] 同 credential 并发拒绝及 `Retry-After`。
- [x] 同 requestId 网络重试不产生第二次 CPA 请求。
- [x] 音频下载响应头、内容、摘要完整性及跨 credential 404。
- [x] Provider 失败和服务停止错误不泄漏内部信息。

### 13.3 Hermes 插件测试

- [x] 仅接管已授权 Weixin 私聊。
- [x] 未授权用户继续走 Hermes pairing，不被插件绕过。
- [x] slash command、群聊、其他平台、缺 token 均不接管。
- [x] 接管后立即返回 `skip`，异步任务开始。
- [x] typing 周期刷新且所有退出路径停止。
- [x] 先文字、后一个或多个音频，顺序不变。
- [x] 下载复用 Bearer，限制大小，拒绝非 WAV/错误响应。
- [x] 临时文件在成功和失败路径均删除。
- [x] HTTP 重试复用 requestId；普通 4xx 不盲目重试。
- [x] `turn_in_progress` 按 `Retry-After` 使用同一请求轮询。
- [x] 同一聊天并发消息给出明确 busy 提示。
- [x] audio unavailable 时仍发送完整文字。

### 13.4 全量回归

- [x] `go test ./... -count=1`：在用户颁布禁令前通过；只作为历史证据，
  禁止在 VPS 再次运行，后续由 GitHub-hosted CI 执行。
- [ ] `go test -race ./...`：未完成。首次长时间停留在 modernc SQLite
  编译，后一次按用户要求终止；不得在这台 VPS 上重跑，改由 GitHub-hosted
  CI 验证。
- [ ] `go vet ./...`：两次执行均被会话续接中断，没有可报告结果；它会触发
  Go 编译链，不在 VPS 再启动，改由 GitHub-hosted CI 验证。
- [x] `npm --prefix web run check`：0 错误、0 警告。
- [x] `npm --prefix web run build`：通过，仅有既有 chunk 大小提示。
- [ ] `npm --prefix web run test:e2e`：只完成部分项目，详见第 14.2 节；
  受 1 GiB 内存安全规则限制，不在本机继续启动 Chromium。
- [x] Hermes 插件 `python -m unittest`：Hermes 0.19.0 镜像内原 20 项通过；
  修复真实 slim 错误 envelope 后，宿主机标准库测试 21 项再次通过。
- [x] slim 与 Hermes 的 `docker compose config --quiet`：均通过，只解析
  配置，未启动或部署。
- [x] 工作树审计：Git 候选文件中没有 Bridge token、私钥、数据库、生成 WAV
  或 Playwright trace；详见第 14.2 节。

## 14. 完成记录

### 14.1 基线与范围

- slim 基线提交：
  `b15edece2d89eb580781ea901a179571d39fb4c5`；本方案目前是该基线上的
  未提交工作树。
- Hermes 源码基线：0.19.0，
  `8a71feb84ca20d92908ab95a45f7fb39fd376b26`。
- 实施内容仍未部署：未复制插件到 Hermes Profile、未签发生产 token、未改
  `plugins.enabled`、未修改生产数据库，也未重启现有容器。

### 14.2 实际验证记录

| 命令或验证 | 结果 |
|---|---|
| 新增 guidance、speech、store、HTTP 和 CLI 的定向 Go 测试 | 在禁令前通过；其覆盖点列于 13.1、13.2 |
| `go test ./... -count=1` | 在禁令前全量通过；今后禁止再次执行 |
| `go test -race ./...` | 未完成；modernc SQLite 编译开销过高，后续执行已终止 |
| `go vet ./...` | 两次被会话续接中断，无最终结果；未宣称通过 |
| `npm --prefix web run check` | 通过，0 错误、0 警告 |
| `npm --prefix web run build` | 通过；只有既有 chunk 大小提示 |
| `npm --prefix web audit --audit-level=moderate` | 通过，0 vulnerabilities |
| Hermes 0.19.0 镜像内 `python -m unittest` | 原 20 项全部通过 |
| `python3 -m unittest integrations.hermes.slim_restaurant_bridge.test_bridge` | 修复错误 envelope 与 WAV 边界后，21 项通过 |
| Hermes 插件真实 import/register 验证 | 通过 |
| slim、Hermes 各自的 `docker compose config --quiet` | 均通过 |
| 新增 Go 文件的 `gofmt -d` | 无输出 |
| `git diff --check` | 通过 |
| Git 候选文件的凭据与运行时产物扫描 | 未发现真实 `hbr_` token、常见云/GitHub token、私钥、数据库、生成 WAV 或 trace |

仓库原有的 `static/audio/greeting.mp3` 和 `notification.mp3` 是受版本控制的
产品静态提示音，不是本轮 TTS 产物。`web/test-results/` 的失败 trace 和插件
`__pycache__/` 存在于本地但均被 `.gitignore` 明确排除；用户原有、未跟踪的
`docs/PROJECT_HANDOFF_2026-07-28.md` 保持原样。

Playwright 的完整情况不能压缩成“通过”：

1. 宿主机首次运行缺少 Chromium；浏览器安装到用户缓存后，宿主机仍缺
   `libnspr4` 等系统库，因此没有修改系统包，改在已有 Hermes 镜像的一次性
   容器中验证。
2. 第一次容器运行中，桌面端大型首项执行到最后的滚动断言后触发 30 秒总超时；
   其余 4 项桌面测试通过，移动端首项也通过。此时容器约占 323 MiB，swap
   仅余约 27 MiB，按资源安全规则终止。
3. 因大型首项同时覆盖登录、设置、模型选择、流式内容、富文本、语音和滚动，
   已为该项单独设置 60 秒超时。该改动合理但尚未在本机安全重验。
4. 受限重验给容器设置 350 MiB 上限；容器达到约 347.6 MiB，系统可用内存
   约 129 MiB。任务因严重资源节流无法完成页面初始加载，并再次按规则终止。
   这既不能记为产品失败，也不能记为修复验证成功。
5. 当前 1 GiB VPS 不再运行浏览器 E2E。完整重验需要在内存充足的隔离 CI 或
   测试机执行。

### 14.3 验证中修复的问题

- 长请求每两秒轮询幂等结果原会被 30 次/分钟限流误伤；Bridge 路由限额调整为
  120 次/分钟。
- slim 错误响应使用嵌套的 `error.code`，插件原先只读取顶层 `code`，会把真实
  `409 turn_in_progress` 当成普通冲突；现已解析真实 envelope，并由单元测试
  验证会保留 `Retry-After`。
- 服务关闭时 TTS 原未完整继承关闭 context；现会响应取消并停止后续音频工作。
- 音频记录和下载路径现强制位于专用 `hermes-restaurant-audio` 目录，并验证
  摘要、所有权、有效期和最终解析路径。
- 服务端与插件的音频合同统一为最多 32 段、单段 25 MiB、总计 100 MiB。
- 插件与服务端现一致拒绝只有 44 字节头、没有 PCM 载荷的空 WAV。
- Hermes host 网络的正确 URL 和 manifest 插件启用名已在运维文档中固定。
- Playwright 大型首项的独立超时由 30 秒调整为 60 秒；受资源规则限制，尚待
  外部测试机重验。

### 14.4 当前结论

已安全执行的测试范围内没有遗留失败。`race`、`vet` 和完整浏览器 E2E 没有
最终通过证据，原因和后续验证环境已如实记录，不能宣称它们通过。微信侧最终
收到的是完整文字和一个或多个可播放 WAV 文件附件；腾讯 iLink/Hermes 当前
没有可由发送端保证的原生语音气泡或自动播放字段。

**未部署，等待用户指令。**
