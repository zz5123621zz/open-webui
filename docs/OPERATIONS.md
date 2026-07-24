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

图片生成路由由以下环境变量控制：

- `AI_RESPONSE_IMAGE_MODELS`：使用 Responses `image_generation` 工具的聊天
  模型列表；留空时只启用 `AI_DEFAULT_MODEL`。
- `AI_DEDICATED_IMAGE_MODELS_JSON`：聊天模型到
  `/v1/images/generations` 专用模型的映射。CPA 模式在变量缺失或为空时
  默认使用 `{"grok-4.5":"grok-imagine-image-quality"}`。

应用只发送模型、提示词、`n=1` 和 `response_format=b64_json`；不会覆盖
`quality`、`size`、`compression` 或 `partial_images`，因此图片质量保持
CPA 服务端 `auto` 默认值。

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

交互式创建两名用户：

```bash
sudo docker compose exec app /app/server user add \
  --username USERNAME --display-name 'DISPLAY NAME'
```

不提供注册接口。重复执行只会尝试创建另一个管理员指定的用户。账户维护
同样使用交互式命令，密码不会出现在命令行或 Compose 配置中：

```bash
sudo docker compose exec app /app/server user password --username USERNAME
sudo docker compose exec app /app/server user disable --username USERNAME
sudo docker compose exec app /app/server user enable --username USERNAME
```

重置密码和停用账户都会撤销该用户的所有现有 Session。登录后的用户也可
在“账户与安全”中自行改密或注销全部设备。

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
