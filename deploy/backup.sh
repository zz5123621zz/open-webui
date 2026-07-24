#!/bin/sh
set -eu

DEPLOY_ROOT=${DEPLOY_ROOT:-/opt/owui-personal-slim}
BACKUP_AGE_RECIPIENT=${BACKUP_AGE_RECIPIENT:?Set BACKUP_AGE_RECIPIENT}
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
PLAIN_DB="backups/app-${STAMP}.db"
ENCRYPTED_DIR="${DEPLOY_ROOT}/encrypted-backups"
ENCRYPTED_FILE="${ENCRYPTED_DIR}/chat-${STAMP}.tar.gz.age"

command -v age >/dev/null 2>&1 || {
  echo "age is required" >&2
  exit 1
}

cd "$DEPLOY_ROOT"
install -d -m 0700 "$ENCRYPTED_DIR"
docker compose exec -T app /app/server backup --output "/data/${PLAIN_DB}"

set -- "$PLAIN_DB"
[ ! -d "${DEPLOY_ROOT}/data/uploads" ] || set -- "$@" uploads
[ ! -d "${DEPLOY_ROOT}/data/generated" ] || set -- "$@" generated

tar -C "${DEPLOY_ROOT}/data" -czf - "$@" |
  age -r "$BACKUP_AGE_RECIPIENT" -o "$ENCRYPTED_FILE"

# The plaintext database snapshot is transient. Conversation attachments in
# data remain the live copy; only encrypted archives leave this host.
rm -f "${DEPLOY_ROOT}/data/${PLAIN_DB}"
chmod 0600 "$ENCRYPTED_FILE"
find "$ENCRYPTED_DIR" -type f -name 'chat-*.tar.gz.age' -mtime +7 -delete

if [ -n "${RCLONE_REMOTE:-}" ]; then
  rclone copy "$ENCRYPTED_FILE" "$RCLONE_REMOTE"
fi

echo "Encrypted backup created: $ENCRYPTED_FILE"
