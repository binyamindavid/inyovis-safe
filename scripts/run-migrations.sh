#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${MIGRATION_DATABASE_URL:?Set MIGRATION_DATABASE_URL to the migration-owner PostgreSQL URL}"
cd "$root/apps/backend"
exec go run ./cmd/migrate
