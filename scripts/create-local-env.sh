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
} > .env
echo 'Created .env with local service credentials'
