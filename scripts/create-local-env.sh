#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
if [ -e .env ]; then
  echo '.env already exists; leaving it unchanged' >&2
  exit 1
fi

umask 077
{
  printf 'S3_ACCESS_KEY_ID=local-%s\n' "$(openssl rand -hex 8)"
  printf 'S3_SECRET_ACCESS_KEY=%s\n' "$(openssl rand -hex 24)"
  printf 'AUTH_SECRET=%s\n' "$(openssl rand -hex 32)"
  printf 'API_GATEWAY_SECRET=%s\n' "$(openssl rand -hex 32)"
  printf 'BROWSER_ORIGIN=https://127.0.0.1:3001\n'
  printf 'API_BASE_URL=http://127.0.0.1:8081\n'
  printf 'MAIL_DRIVER=smtp\nSMTP_ADDR=127.0.0.1:1025\nMAIL_FROM=no-reply@invoyis.test\n'
} > .env
echo 'Created .env with local service credentials'
