#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env
set +a
: "${DATABASE_URL:?Set DATABASE_URL in .env}"
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -c "INSERT INTO invoyis.accounts (email, eligible) VALUES ('pilot@example.test', true) ON CONFLICT (email) DO UPDATE SET eligible=true"
echo 'Seeded pilot@example.test as a synthetic eligible account'
