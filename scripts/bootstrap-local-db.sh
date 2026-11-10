#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
set -a
. ./.env
set +a
: "${DATABASE_URL:?Set DATABASE_URL in .env}"
: "${TEST_DATABASE_URL:?Set TEST_DATABASE_URL in .env}"

for database_url in "$DATABASE_URL" "$TEST_DATABASE_URL"; do
  if [ "$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('public.schema_migrations') IS NOT NULL")" = t ]; then
    version=$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT version || ':' || dirty FROM public.schema_migrations LIMIT 1")
    if [ "$version" = '2:false' ]; then
      echo 'Migration version 2 already applied'
      continue
    fi
    if [ "$version" != '1:false' ]; then
      echo "Unexpected migration version: ${version:-empty}" >&2
      exit 1
    fi
  else
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
CREATE TABLE public.schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
\i apps/backend/migrations/000001_create_schema.up.sql
INSERT INTO public.schema_migrations (version, dirty) VALUES (1, false);
COMMIT;
SQL
  fi

  psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000002_auth.up.sql
UPDATE public.schema_migrations SET version = 2;
COMMIT;
SQL
done
