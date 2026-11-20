#!/bin/sh
set -eu

: "${MIGRATION_DATABASE_URL:?Set the migration-owner PostgreSQL URL for Railway pre-deploy}"
MIGRATIONS_PATH=/usr/local/share/invoyis/migrations \
  /usr/local/bin/invoyis-migrate
