#!/bin/sh
set -eu

DEPLOY_ROOT=${DEPLOY_ROOT:-$(pwd)}
failed=0

pass() {
  printf 'PASS  %s\n' "$1"
}

fail() {
  printf 'FAIL  %s\n' "$1" >&2
  failed=1
}

require_file() {
  if [ -s "$1" ]; then
    pass "$1 exists and is non-empty"
  else
    fail "$1 is missing or empty"
  fi
}

cd "$DEPLOY_ROOT"
require_file .env
require_file compose.yaml
require_file secrets/app_secret
require_file secrets/provider_api_key

if [ -s .env ]; then
  app_image=$(sed -n 's/^APP_IMAGE=//p' .env | tail -n 1)
  default_model=$(sed -n 's/^AI_DEFAULT_MODEL=//p' .env | tail -n 1)
  case "$app_image" in
    ""|*"replace-"*|*:latest) fail "APP_IMAGE is not a fixed tag or digest" ;;
    *) pass "APP_IMAGE is fixed and is not latest" ;;
  esac
  case "$default_model" in
    ""|*"replace-with"*) fail "AI_DEFAULT_MODEL is still a placeholder" ;;
    *) pass "AI_DEFAULT_MODEL is set" ;;
  esac
fi

for secret in secrets/app_secret secrets/provider_api_key; do
  [ -e "$secret" ] || continue
  mode=$(stat -c '%a' "$secret")
  owner=$(stat -c '%u:%g' "$secret")
  if [ "$mode" = "400" ] && [ "$owner" = "65532:65532" ]; then
    pass "$secret has mode 0400 and owner 65532:65532"
  else
    fail "$secret must have mode 0400 and owner 65532:65532 (found $mode $owner)"
  fi
done

if docker info >/dev/null 2>&1; then
  pass "Docker daemon is accessible"
  if docker compose config --quiet; then
    pass "Compose configuration is valid"
  else
    fail "Compose configuration is invalid"
  fi
else
  fail "Docker daemon is not accessible to the current operator"
fi

if getent hosts chat.la4rain.com >/dev/null 2>&1; then
  pass "chat.la4rain.com resolves"
else
  fail "chat.la4rain.com does not resolve"
fi

if command -v age >/dev/null 2>&1; then
  pass "age is installed for encrypted backups"
else
  fail "age is not installed"
fi

if [ "$failed" -ne 0 ]; then
  exit 1
fi
echo "Production preflight passed."
