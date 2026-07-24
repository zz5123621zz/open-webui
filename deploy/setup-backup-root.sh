#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Run this setup as root." >&2
  exit 1
fi

SOURCE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEPLOY_ROOT=${DEPLOY_ROOT:-/opt/owui-personal-slim}
KEY_DIR=${KEY_DIR:-/root/.config/owui-personal-slim}
KEY_FILE=${KEY_FILE:-${KEY_DIR}/backup-age-key.txt}
ENV_FILE=${ENV_FILE:-/etc/owui-personal-slim-backup.env}
RUN_NOW=${RUN_NOW:-1}
RCLONE_REMOTE=${RCLONE_REMOTE:-}

command -v age >/dev/null 2>&1 || {
  echo "age is required." >&2
  exit 1
}
command -v age-keygen >/dev/null 2>&1 || {
  echo "age-keygen is required." >&2
  exit 1
}
command -v systemctl >/dev/null 2>&1 || {
  echo "systemd is required." >&2
  exit 1
}

install -d -m 0700 "$KEY_DIR"
if [ ! -s "$KEY_FILE" ]; then
  umask 077
  age-keygen -o "$KEY_FILE"
fi
chmod 0400 "$KEY_FILE"
recipient=$(age-keygen -y "$KEY_FILE")

env_tmp=$(mktemp "${ENV_FILE}.XXXXXX")
trap 'rm -f "$env_tmp"' EXIT HUP INT TERM
{
  printf 'BACKUP_AGE_RECIPIENT=%s\n' "$recipient"
  if [ -n "$RCLONE_REMOTE" ]; then
    printf 'RCLONE_REMOTE=%s\n' "$RCLONE_REMOTE"
  fi
} >"$env_tmp"
install -m 0600 "$env_tmp" "$ENV_FILE"
rm -f "$env_tmp"
trap - EXIT HUP INT TERM

install -m 0644 \
  "$SOURCE_ROOT/deploy/systemd/owui-personal-slim-backup.service" \
  /etc/systemd/system/owui-personal-slim-backup.service
install -m 0644 \
  "$SOURCE_ROOT/deploy/systemd/owui-personal-slim-backup.timer" \
  /etc/systemd/system/owui-personal-slim-backup.timer

systemctl daemon-reload
systemctl enable --now owui-personal-slim-backup.timer
if [ "$RUN_NOW" = "1" ]; then
  systemctl start owui-personal-slim-backup.service
fi

echo "Backup timer enabled."
echo "Age recipient: $recipient"
echo "Private restore key: $KEY_FILE"
echo "Copy the private restore key to a separate secure location."
