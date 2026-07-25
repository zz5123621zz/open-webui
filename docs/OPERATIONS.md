# 部署与运维

## 1. 前置条件

- `chat.la4rain.com` 的 DNS 已指向 VPS。
- Xray 会把该 SNI 的普通 TLS 流量回落到 Nginx
  `127.0.0.1:8443`。
- fork 的 GitHub Actions 已发布 GHCR 镜像。
- CPA `/v1/models`、`/v1/responses` 和 `/v1/images/generations` 可通过
  同一个密钥访问，且没有关闭 Web Search 或 Image Generation。

## 2. 创建部署目录

推荐用仓库提供的一次性安装器完成目录、权限、配置和交互式 secret 写入。
CPA 密钥输入不会出现在命令行或 shell history：

```bash
cd /home/vpsadmin/owui-personal-slim
sudo APP_IMAGE='ghcr.io/OWNER/owui-personal-slim@sha256:DIGEST' \
  AI_DEFAULT_MODEL='EXACT_CPA_CHAT_MODEL_ID' \
  ./deploy/install-root.sh
```

脚本默认只安装并运行只读预检，不启动容器。修复预检报告的 DNS、`age`
或其他宿主机问题后，用同样的固定镜像和模型启动：

```bash
sudo DEPLOY_START=1 \
  APP_IMAGE='ghcr.io/OWNER/owui-personal-slim@sha256:DIGEST' \
  AI_DEFAULT_MODEL='EXACT_CPA_CHAT_MODEL_ID' \
  ./deploy/install-root.sh
```

现有 `.env` 和非空 secret 不会被覆盖。也可按下面步骤手工安装。

下面的 UID/GID `65532` 与 distroless `nonroot` 一致：

```bash
sudo install -d -m 0750 /opt/owui-personal-slim
sudo install -d -m 0700 -o 65532 -g 65532 /opt/owui-personal-slim/data
sudo install -d -m 0700 /opt/owui-personal-slim/secrets
sudo install -d -m 0700 /opt/owui-personal-slim/encrypted-backups
sudo install -m 0400 -o 65532 -g 65532 /dev/null \
  /opt/owui-personal-slim/secrets/app_secret
sudo install -m 0400 -o 65532 -g 65532 /dev/null \
  /opt/owui-personal-slim/secrets/provider_api_key
```

生成应用密钥：

```bash
openssl rand -base64 48 |
  sudo tee /opt/owui-personal-slim/secrets/app_secret >/dev/null
```

交互式写入 CPA 密钥，密钥值不会进入 shell history：

```bash
read -rsp 'CPA API key: ' CPA_SECRET_INPUT; echo
printf '%s' "$CPA_SECRET_INPUT" |
  sudo tee /opt/owui-personal-slim/secrets/provider_api_key >/dev/null
unset CPA_SECRET_INPUT
```

再次固定权限：

```bash
sudo chown 65532:65532 /opt/owui-personal-slim/secrets/*
sudo chmod 0400 /opt/owui-personal-slim/secrets/*
```

复制 `compose.yaml`、`.env.example`，把 `.env.example` 改名为 `.env`。
`APP_IMAGE` 应在第一次验证后换成 GHCR 返回的不可变 digest。
`AI_MODEL_ALLOWLIST` 固定为 `gpt-5.6-luna,gpt-5.6-terra,gpt-5.6-sol`，
对应前端的“快速 / 均衡 / 专家”三种模式；默认模型仍为
`gpt-5.6-sol`。

图片生成路由由以下环境变量控制：

- `AI_RESPONSE_IMAGE_MODELS`：使用 Responses `image_generation` 工具的聊天
  模型列表；留空时只启用 `AI_DEFAULT_MODEL`。
- `AI_DEDICATED_IMAGE_MODELS_JSON`：聊天模型到
  `/v1/images/generations` 专用模型的映射。CPA 模式在变量缺失或为空时
  默认使用 `{"grok-4.5":"grok-imagine-image-quality"}`。
- `AI_IMAGE_PROMPT_MAX_BYTES`：专用图片接口的提示词 UTF-8 字节上限。
  CPA Imagine 当前限制为 `8000`。应用会先移除仅用于排版的空白；压缩后
  仍超限时会在本地返回明确错误，不会把无效请求发送给 CPA。

应用只发送模型、提示词、`n=1` 和 `response_format=b64_json`；不会覆盖
`quality`、`size`、`compression` 或 `partial_images`，因此图片质量保持
CPA 服务端 `auto` 默认值。

### 渐进式推理摘要

渐进式摘要依赖单独维护的最小 CPA 兼容镜像。该镜像只对白名单字段
`stream_options.reasoning_summary_delivery = "sequential_cutoff"` 放行，
生产部署必须固定
`ghcr.io/zz5123621zz/cliproxyapi-la4rain@sha256:DIGEST`，并同时记录替换前
CPA 的版本、二进制或镜像 digest 作为回滚目标。替换 CPA 时不得更改现有
密钥、账号目录、模型配置或额度来源。

日常开关位于管理员头像菜单的“推理摘要设置”：

- `自动`：下一次符合条件的正常聊天探测或使用渐进摘要；
- `关闭`：之后开始的新回答使用普通 CPA 流；
- `重新检测`：只清除进程内兼容状态，下一次正常聊天才探测，不会发送隐藏
  请求或消耗额外额度。

设置只影响之后开始的新回答，不取消正在运行的回答，也不需要重启应用或
CPA。`AI_PROGRESSIVE_SUMMARY_HARD_DISABLED=true` 是部署级紧急上限，优先于
管理员设置；只在未知上游行为或管理员页面不可用时启用。

首次发布顺序固定为：CPA 兼容镜像 CI 与 digest → 替换 CPA 并验证普通
Responses → 应用镜像 CI 与 digest → 数据库备份 → 应用以 `off` 启动 →
健康检查和普通聊天 → 管理员切换 `auto` → 真实摘要、降级、并发和内存验收。

启动前的只读检查：

```bash
cd /opt/owui-personal-slim
sudo ./deploy/preflight.sh
```

## 3. 启动与创建用户

```bash
cd /opt/owui-personal-slim
sudo docker compose pull
sudo docker compose up -d
sudo docker compose ps
curl --fail --silent http://127.0.0.1:3001/readyz
```

交互式创建普通用户或管理员（密码至少 6 位）：

```bash
sudo docker compose exec app /app/server user add \
  --username USERNAME --display-name 'DISPLAY NAME' --role user
sudo docker compose exec app /app/server user add \
  --username ADMIN --display-name 'Administrator' --role admin
sudo docker compose exec app /app/server user list
```

不提供注册接口。重复执行只会尝试创建另一个管理员指定的用户。账户维护
同样使用交互式命令，密码不会出现在命令行或 Compose 配置中：

```bash
sudo docker compose exec app /app/server user password --username USERNAME
sudo docker compose exec app /app/server user disable --username USERNAME
sudo docker compose exec app /app/server user enable --username USERNAME
```

如果明确需要重置全部用户和工作区，应先完成备份，再执行：

```bash
sudo docker compose exec app /app/server user purge-all --confirm
```

重置密码和停用账户都会撤销该用户的所有现有 Session。登录后的用户也可
在“账户与安全”中自行改密或注销全部设备。

默认生命周期边界由 Compose 配置：每用户 3 GB 活跃附件、30 个活跃会话、
10 个置顶会话以及 7 天临时留档。临时留档附件不计入 3 GB，但仍占用 VPS
物理磁盘，并由每小时维护任务在到期后永久清理。

## 4. Nginx

首次签发证书时，先安装只包含 HTTP/ACME 的 bootstrap 配置：

```bash
sudo install -d -m 0755 /var/www/acme/.well-known/acme-challenge
sudo install -m 0644 deploy/nginx/chat.la4rain.com.bootstrap.conf \
  /etc/nginx/sites-available/chat.la4rain.com
sudo ln -s /etc/nginx/sites-available/chat.la4rain.com \
  /etc/nginx/sites-enabled/chat.la4rain.com
sudo nginx -t
sudo systemctl reload nginx
sudo certbot certonly --webroot -w /var/www/acme \
  -d chat.la4rain.com --non-interactive --agree-tos
```

签发成功后原子替换为完整 HTTPS 反代，并安装续期 reload hook：

```bash
sudo install -m 0644 deploy/nginx/chat.la4rain.com.conf \
  /etc/nginx/sites-available/chat.la4rain.com
sudo install -m 0755 deploy/certbot-reload-nginx.sh \
  /etc/letsencrypt/renewal-hooks/deploy/reload-nginx
sudo nginx -t
sudo systemctl reload nginx
```

浏览器上传虚拟主机保持 `13m`；CPA 虚拟主机保持 `50m`。SSE location
必须关闭响应和请求缓冲。应用在活动回答期间每 15 秒发送 SSE 注释心跳，
用于跨过反代和浏览器的长时间无输出阶段。

SSE 连接现在只是回答订阅，不再拥有 CPA 请求。关闭页面、断网、刷新或退出
登录不会停止回答；用户必须点击“停止”发送显式取消。运行中安全证据至多每秒
写入一次 SQLite，重新打开会话时前端每秒读取单条 assistant 记录。详细状态、
重启边界和验收步骤见 [后台回答](BACKGROUND_RESPONSES.md)。

应用停止或更新时应保留 Compose 的正常 `SIGTERM` 流程，不要直接发送
`SIGKILL`。正常关机会取消 Provider 请求并把状态保存为
`service_interrupted`；即使被强制终止，下次应用服务启动也会修复遗留
`streaming` 状态。任何重启场景都不会自动重发 CPA 请求。

## 5. 发布

```bash
cd /opt/owui-personal-slim
sudo ./deploy/backup.sh
sudo docker compose pull
sudo docker compose up -d
sudo docker compose ps
curl --fail --silent http://127.0.0.1:3001/readyz
```

记录每次部署前后的镜像 digest：

```bash
sudo docker inspect --format '{{.Image}}' owui-personal-slim
```

## 6. 备份与恢复

`deploy/backup.sh` 要求宿主机安装 `age`，并在仅 root 可读的部署 `.env`
之外提供 `BACKUP_AGE_RECIPIENT`。脚本会：

1. 通过 SQLite Online Backup API 创建一致性快照；
2. 连同 `uploads/` 和 `generated/` 压缩；
3. 在离开 VPS 前用 age 加密；
4. 只保留最近 7 份本机密文；
5. 若设置 `RCLONE_REMOTE`，上传密文到异地。

首次部署可用安装脚本生成 root-only age identity、写入
`/etc/owui-personal-slim-backup.env`，并启用每日 systemd timer：

```bash
cd /home/vpsadmin/owui-personal-slim
sudo ./deploy/setup-backup-root.sh
sudo systemctl status owui-personal-slim-backup.timer
sudo systemctl status owui-personal-slim-backup.service
```

默认每天 UTC 03:17 执行，并加入最多 30 分钟随机延迟。私钥位于
`/root/.config/owui-personal-slim/backup-age-key.txt`；必须另行保存到
VPS 之外的安全位置，否则 VPS 整机丢失后无法解密备份。需要异地上传时，
先配置 rclone，再以 `RCLONE_REMOTE='remote:path'` 重跑安装脚本。

恢复演练必须在单独目录中进行。停止应用后解密归档，恢复 `app.db`、
`uploads/` 与 `generated/`，确认属主为 `65532:65532`、目录 `0700`、
文件 `0600`，再启动固定的旧镜像 digest。

## 7. 验收观察

```bash
sudo docker stats owui-personal-slim
sudo docker compose logs --tail=200 app
free -h
```

常规聊天建议低于 `180 MiB`，容器硬上限 `320 MiB`。健康检查不依赖
CPA，避免 Provider 临时故障引发重启风暴。
