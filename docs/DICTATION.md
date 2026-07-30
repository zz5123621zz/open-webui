# 网页语音输入

## 目标与交互

La4RainGPT 使用火山引擎豆包流式语音识别 2.0，把浏览器麦克风语音实时转成
输入框文字。它是输入方式，不是自动对话：

- 点击“语音输入”开始，再点击一次停止；
- 也可按住约 0.5 秒说话，松开后停止；
- 识别中的临时文字实时写入输入框，停止后以二遍结果定稿；
- 最终文字由用户检查并手动发送，不会自动提交给 Agent；
- “取消”恢复录音开始前的原草稿；
- 失败时保留最后一次可用的临时转写；
- 单次最长 120 秒。

Safari 与桌面 Edge 为正式支持目标。微信内置浏览器尽力兼容；麦克风授权或
AudioWorklet 不可用时会降级到 ScriptProcessor，仍失败则提示改用 Safari。

## 轻量架构

没有引入 Redis、消息队列、对象存储或新的常驻进程。

```text
浏览器麦克风
  -> Web Audio（16 kHz / 16-bit / 单声道 PCM，约 200 ms/包）
  -> 应用同源 WebSocket /api/v1/dictation/sessions
  -> 火山 WebSocket wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
  -> 临时/最终转写
  -> 浏览器输入框
```

服务端只在内存中转当前音频包，不把原始音频写入 SQLite、文件或日志。识别
Provider 只收到匿名化用户标识。通用工作台不附带对话上下文；餐饮工作台仅可
临时附带录音前草稿与已经由用户确认的餐厅档案，不传聊天历史。

## 火山参数

| 项目 | 值 |
|---|---|
| Endpoint | `wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async` |
| Resource ID | `volc.seedasr.sauc.duration` |
| 音频 | PCM、16 kHz、16-bit、单声道 |
| 分包 | 约 200 ms |
| 二遍识别 | `enable_nonstream=true` |
| 文本规范化 | `enable_itn=true` |
| 标点 | `enable_punc=true` |
| 语种/方言检测 | `enable_lid=true` |
| SSD 2.0 | `ssd_version="200"` |

不固定 `language=zh-CN`，以便识别普通话、英文、上海话（吴语）、闽南语、
粤语、西南官话和中原官话。协议使用 gzip；full request 序列号为 1，音频从
2 递增，最后一包使用负序列号和 `0b0011` flag。

## 凭据与 Compose

ASR 使用独立 secret 名 `asr_volcengine_api_key`。即使它与 TTS 的 API Key
值相同，也保持两个文件，避免未来轮换互相影响。

交互式创建，不把值写入 shell history：

```bash
cd /opt/owui-personal-slim
read -rsp 'Volcengine ASR API key: ' ASR_SECRET_INPUT; echo
printf '%s' "$ASR_SECRET_INPUT" |
  sudo tee ./secrets/asr_volcengine_api_key >/dev/null
unset ASR_SECRET_INPUT
sudo chown 65532:65532 ./secrets/asr_volcengine_api_key
sudo chmod 0400 ./secrets/asr_volcengine_api_key
```

生产解析和启动始终合并三份 Compose：

```bash
sudo docker compose \
  -f compose.yaml \
  -f compose.tts-volcengine.yaml \
  -f compose.asr-volcengine.yaml \
  config --quiet
sudo docker compose \
  -f compose.yaml \
  -f compose.tts-volcengine.yaml \
  -f compose.asr-volcengine.yaml \
  up -d app
```

环境变量：

- `ASR_VOLCENGINE_API_KEY_FILE=/run/secrets/asr_volcengine_api_key`
- `ASR_VOLCENGINE_RESOURCE_ID=volc.seedasr.sauc.duration`
- `ASR_SESSION_TTL_SECONDS=135`

每用户最多 1 路、全应用最多 2 路识别，不排队；这些固定边界独立于 CPA 和
TTS 并发。管理员头像菜单中的“语音输入设置”只控制新会话，关闭不会中止已经
开始的录音。

## 安全与浏览器行为

- `Permissions-Policy` 只允许本站使用麦克风；
- WebSocket 要求已登录、同源 Origin，并沿用服务端 Session；
- 音频帧限制 64 KiB，总音频量和墙钟时间双重限制；
- 页面隐藏、锁屏或离开时立即停止采集；
- 开始录音会停止网页 TTS，录音结束后不会自动恢复旧朗读；
- 录音期间锁定输入、发送、上传、绘图和朗读按钮；
- Provider 错误日志不记录音频或转写全文。

## 验收

CI 必须覆盖协议编码/解码、Provider 握手、并发门、设置审计、鉴权/同源
WebSocket、点击与长按、取消、失败保留临时文字、两分钟停止、TTS 互斥和
管理员开关。

生产冒烟至少检查：

1. `/readyz` 为 ready，数据库 schema 为 V8；
2. 首页响应头包含 `microphone=(self)`；
3. 普通聊天、浏览器 TTS 与微信 Bridge 回归正常；
4. Safari 或桌面 Edge 录制一句普通话，临时文字实时出现，停止后二遍定稿；
5. 录制一句上海话并确认能得到合理转写；
6. 文字不会自动发送，容器和数据库中没有新增音频文件。
