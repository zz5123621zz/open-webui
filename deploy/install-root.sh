#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Run this installer as root." >&2
  exit 1
fi

SOURCE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEPLOY_ROOT=${DEPLOY_ROOT:-/opt/owui-personal-slim}
APP_IMAGE=${APP_IMAGE:-}
AI_DEFAULT_MODEL=${AI_DEFAULT_MODEL:-gpt-5.6-sol}
AI_MODEL_ALLOWLIST=${AI_MODEL_ALLOWLIST:-gpt-5.6-luna,gpt-5.6-terra,gpt-5.6-sol}
APP_BASE_URL=${APP_BASE_URL:-https://chat.la4rain.com}
AI_BASE_URL=${AI_BASE_URL:-https://cpa.la4rain.com/v1}
DEPLOY_START=${DEPLOY_START:-0}

if [ -z "$APP_IMAGE" ]; then
  printf 'Immutable image tag or digest: '
  IFS= read -r APP_IMAGE
fi
if [ -z "$AI_DEFAULT_MODEL" ]; then
  printf 'Selectable CPA chat model ID: '
  IFS= read -r AI_DEFAULT_MODEL
fi

case "$APP_IMAGE" in
  ""|*"replace-"*|*:latest)
    echo "APP_IMAGE must be a fixed version tag or digest, never latest." >&2
    exit 1
    ;;
esac
case "$AI_DEFAULT_MODEL" in
  ""|*"replace-with"*)
    echo "AI_DEFAULT_MODEL must be an exact selectable CPA model ID." >&2
    exit 1
    ;;
esac
case "$APP_BASE_URL" in
  https://*) ;;
  *)
    echo "APP_BASE_URL must use https." >&2
    exit 1
    ;;
esac
case "$AI_BASE_URL" in
  https://*/v1|http://127.0.0.1:*/v1|http://localhost:*/v1) ;;
  *)
    echo "AI_BASE_URL must be an HTTPS /v1 endpoint or a loopback /v1 endpoint." >&2
    exit 1
    ;;
esac

install -d -m 0750 "$DEPLOY_ROOT"
install -d -m 0700 -o 65532 -g 65532 "$DEPLOY_ROOT/data"
install -d -m 0700 -o 65532 -g 65532 "$DEPLOY_ROOT/data/backups"
install -d -m 0700 "$DEPLOY_ROOT/secrets"
install -d -m 0700 "$DEPLOY_ROOT/encrypted-backups"
install -d -m 0750 "$DEPLOY_ROOT/deploy"
install -d -m 0750 "$DEPLOY_ROOT/deploy/systemd"
install -m 0640 "$SOURCE_ROOT/compose.yaml" "$DEPLOY_ROOT/compose.yaml"
install -m 0750 "$SOURCE_ROOT/deploy/backup.sh" "$DEPLOY_ROOT/deploy/backup.sh"
install -m 0750 "$SOURCE_ROOT/deploy/setup-backup-root.sh" "$DEPLOY_ROOT/deploy/setup-backup-root.sh"
install -m 0750 "$SOURCE_ROOT/deploy/preflight.sh" "$DEPLOY_ROOT/deploy/preflight.sh"
install -m 0750 "$SOURCE_ROOT/deploy/certbot-reload-nginx.sh" "$DEPLOY_ROOT/deploy/certbot-reload-nginx.sh"
install -m 0644 \
  "$SOURCE_ROOT/deploy/systemd/owui-personal-slim-backup.service" \
  "$DEPLOY_ROOT/deploy/systemd/owui-personal-slim-backup.service"
install -m 0644 \
  "$SOURCE_ROOT/deploy/systemd/owui-personal-slim-backup.timer" \
  "$DEPLOY_ROOT/deploy/systemd/owui-personal-slim-backup.timer"

if [ ! -e "$DEPLOY_ROOT/.env" ]; then
  umask 077
  {
    printf 'APP_IMAGE=%s\n' "$APP_IMAGE"
    printf 'APP_BASE_URL=%s\n' "$APP_BASE_URL"
    printf 'AI_BASE_URL=%s\n' "$AI_BASE_URL"
    printf 'AI_DEFAULT_MODEL=%s\n' "$AI_DEFAULT_MODEL"
    printf 'AI_DEFAULT_REASONING_EFFORT=high\n'
    printf 'AI_MODEL_ALLOWLIST=%s\n' "$AI_MODEL_ALLOWLIST"
    printf 'AI_MODEL_DENYLIST=\n'
    printf 'AI_UNKNOWN_MODEL_CONTEXT_TOKENS=128000\n'
    printf 'AI_MODEL_CONTEXT_OVERRIDES_JSON={}\n'
    printf 'USER_MAX_STORAGE_BYTES=3221225472\n'
    printf 'USER_MAX_ACTIVE_CONVERSATIONS=30\n'
    printf 'USER_MAX_PINNED_CONVERSATIONS=10\n'
    printf 'CONVERSATION_RETENTION_HOURS=168\n'
    printf 'MAINTENANCE_INTERVAL_MINUTES=60\n'
  } >"$DEPLOY_ROOT/.env"
else
  echo "Keeping existing $DEPLOY_ROOT/.env"
fi

if [ ! -s "$DEPLOY_ROOT/secrets/app_secret" ]; then
  secret_tmp=$(mktemp "$DEPLOY_ROOT/secrets/.app-secret.XXXXXX")
  trap 'rm -f "$secret_tmp"' EXIT HUP INT TERM
  openssl rand -base64 48 >"$secret_tmp"
  chown 65532:65532 "$secret_tmp"
  chmod 0400 "$secret_tmp"
  mv "$secret_tmp" "$DEPLOY_ROOT/secrets/app_secret"
  trap - EXIT HUP INT TERM
else
  echo "Keeping existing app secret."
fi

if [ ! -s "$DEPLOY_ROOT/secrets/provider_api_key" ]; then
  printf 'CPA API key (input hidden): '
  trap 'stty echo 2>/dev/null || true' EXIT HUP INT TERM
  stty -echo
  IFS= read -r provider_secret
  stty echo
  trap - EXIT HUP INT TERM
  printf '\n'
  if [ -z "$provider_secret" ]; then
    echo "CPA API key cannot be empty." >&2
    exit 1
  fi
  provider_tmp=$(mktemp "$DEPLOY_ROOT/secrets/.provider-key.XXXXXX")
  trap 'rm -f "$provider_tmp"' EXIT HUP INT TERM
  printf '%s' "$provider_secret" >"$provider_tmp"
  unset provider_secret
  chown 65532:65532 "$provider_tmp"
  chmod 0400 "$provider_tmp"
  mv "$provider_tmp" "$DEPLOY_ROOT/secrets/provider_api_key"
  trap - EXIT HUP INT TERM
else
  echo "Keeping existing CPA credential."
fi

chown 65532:65532 "$DEPLOY_ROOT/secrets/app_secret" "$DEPLOY_ROOT/secrets/provider_api_key"
chmod 0400 "$DEPLOY_ROOT/secrets/app_secret" "$DEPLOY_ROOT/secrets/provider_api_key"

cd "$DEPLOY_ROOT"
./deploy/preflight.sh

if [ "$DEPLOY_START" = "1" ]; then
  docker compose pull
  docker compose up -d
  docker compose ps
  attempts=0
  until curl --fail --silent http://127.0.0.1:3001/readyz >/dev/null; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 30 ]; then
      echo "Container did not become ready; inspect: docker compose logs app" >&2
      exit 1
    fi
    sleep 1
  done
  echo "Application is ready on http://127.0.0.1:3001"
else
  echo "Install complete. Re-run with DEPLOY_START=1 to pull and start the fixed image."
fi
