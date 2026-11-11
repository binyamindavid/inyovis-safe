#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -z "${DATABASE_URL:-}" ] || [ -z "${TEST_DATABASE_URL:-}" ]; then
  set -a
  . ./.env
  set +a
fi
: "${DATABASE_URL:?Set DATABASE_URL in .env}"
: "${TEST_DATABASE_URL:?Set TEST_DATABASE_URL in .env}"

MIGRATION_DATABASE_URL="$DATABASE_URL" scripts/run-migrations.sh
MIGRATION_DATABASE_URL="$TEST_DATABASE_URL" scripts/run-migrations.sh
if [ -n "${ARCHIVE_TEST_DATABASE_URL:-}" ]; then
  MIGRATION_DATABASE_URL="$ARCHIVE_TEST_DATABASE_URL" scripts/run-migrations.sh
fi
