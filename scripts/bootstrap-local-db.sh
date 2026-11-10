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

for database_url in "$DATABASE_URL" "$TEST_DATABASE_URL" ${ARCHIVE_TEST_DATABASE_URL:-}; do
  if [ "$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('public.schema_migrations') IS NOT NULL")" = t ]; then
    version=$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT version || ':' || dirty FROM public.schema_migrations LIMIT 1")
    if [ "$version" = '8:false' ]; then
      echo 'Migration version 8 already applied'
      continue
    fi
    if [ "$version" != '1:false' ] && [ "$version" != '2:false' ] && [ "$version" != '3:false' ] && [ "$version" != '4:false' ] && [ "$version" != '5:false' ] && [ "$version" != '6:false' ] && [ "$version" != '7:false' ]; then
      echo "Unexpected migration version: ${version:-empty}" >&2
      exit 1
    fi
  else
    if [ "$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('invoyis.archives') IS NOT NULL")" = t ]; then
      if [ "$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('invoyis.extraction_runs') IS NOT NULL")" = t ]; then
        version='8:false'
      elif [ "$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('invoyis.processing_jobs') IS NOT NULL")" = t ]; then
        version='7:false'
      elif [ "$(psql "$database_url" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('invoyis.archive_invitation_outbox') IS NOT NULL")" = t ]; then
        version='6:false'
      else
        echo 'Existing invoyis schema has no migration ledger and cannot be identified safely' >&2
        exit 1
      fi
      psql "$database_url" -X -v ON_ERROR_STOP=1 -c "CREATE TABLE public.schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)" >/dev/null
      psql "$database_url" -X -v ON_ERROR_STOP=1 -c "INSERT INTO public.schema_migrations(version,dirty) VALUES (${version%:*},false)" >/dev/null
    else
      psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
CREATE TABLE public.schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
\i apps/backend/migrations/000001_create_schema.up.sql
INSERT INTO public.schema_migrations (version, dirty) VALUES (1, false);
COMMIT;
SQL
      version='1:false'
    fi
  fi

  if [ "$version" = '8:false' ]; then
    echo 'Migration version 8 already applied'
    continue
  fi

  if [ "$version" = '1:false' ]; then
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000002_auth.up.sql
UPDATE public.schema_migrations SET version = 2;
COMMIT;
SQL
    version='2:false'
  fi

  if [ "$version" = '2:false' ]; then
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000003_auth_outbox_lease.up.sql
UPDATE public.schema_migrations SET version = 3;
COMMIT;
SQL
    version='3:false'
  fi

  if [ "$version" = '3:false' ]; then
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000004_archives.up.sql
UPDATE public.schema_migrations SET version = 4;
COMMIT;
SQL
    version='4:false'
  fi

  if [ "$version" = '4:false' ]; then
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000005_invitation_redemption.up.sql
UPDATE public.schema_migrations SET version = 5;
COMMIT;
SQL
    version='5:false'
  fi

  if [ "$version" = '5:false' ]; then
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000006_invitation_outbox.up.sql
UPDATE public.schema_migrations SET version = 6;
COMMIT;
SQL
    version='6:false'
  fi
  if [ "$version" = '6:false' ]; then
    psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000007_upload_intake.up.sql
UPDATE public.schema_migrations SET version = 7;
COMMIT;
SQL
    version='7:false'
  fi
  psql "$database_url" -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
\i apps/backend/migrations/000008_document_processing.up.sql
UPDATE public.schema_migrations SET version = 8;
COMMIT;
SQL
done
