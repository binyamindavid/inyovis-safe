#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${MIGRATION_DATABASE_URL:?Set MIGRATION_DATABASE_URL to the migration-owner PostgreSQL URL}"
if [ -z "${MIGRATE_BIN:-}" ] && [ -x "$root/.local-bin/migrate" ]; then
  MIGRATE_BIN="$root/.local-bin/migrate"
  export MIGRATE_BIN
fi
cd "$root/apps/backend"
exec go run ./cmd/migrate
