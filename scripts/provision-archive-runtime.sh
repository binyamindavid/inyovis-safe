#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
if [ -z "${ARCHIVE_PROVISION_DATABASE_URL:-}" ]; then
  set -a
  . ./.env
  set +a
  : "${DATABASE_URL:?Set DATABASE_URL in .env}"
  : "${TEST_DATABASE_URL:?Set TEST_DATABASE_URL in .env}"
fi

umask 077
if [ -z "${ARCHIVE_RUNTIME_PASSWORD:-}" ]; then
  ARCHIVE_RUNTIME_PASSWORD=$(openssl rand -hex 24)
fi

if [ -z "${UPLOAD_RECONCILER_PASSWORD:-}" ]; then
  UPLOAD_RECONCILER_PASSWORD=$(openssl rand -hex 24)
fi
if [ -z "${DOCUMENT_WORKER_PASSWORD:-}" ]; then
  DOCUMENT_WORKER_PASSWORD=$(openssl rand -hex 24)
fi

if [ -n "${ARCHIVE_PROVISION_DATABASE_URL:-}" ]; then
  database_urls="$ARCHIVE_PROVISION_DATABASE_URL"
else
  database_urls="$DATABASE_URL $TEST_DATABASE_URL ${ARCHIVE_TEST_DATABASE_URL:-}"
fi
for database_url in $database_urls; do
  psql "$database_url" -X -q -v ON_ERROR_STOP=1 -v runtime_password="$ARCHIVE_RUNTIME_PASSWORD" -v reconciler_password="$UPLOAD_RECONCILER_PASSWORD" -v worker_password="$DOCUMENT_WORKER_PASSWORD" <<'SQL'
SELECT 'CREATE ROLE invoyis_archive_runtime LOGIN' WHERE NOT EXISTS (
    SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime'
) \gexec
ALTER ROLE invoyis_archive_runtime WITH LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'runtime_password';
GRANT USAGE ON SCHEMA invoyis TO invoyis_archive_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    invoyis.archives, invoyis.archive_memberships, invoyis.archive_invitations, invoyis.archive_audit
    TO invoyis_archive_runtime;
REVOKE ALL ON invoyis.upload_sessions, invoyis.stored_objects, invoyis.submissions,
    invoyis.documents, invoyis.processing_jobs, invoyis.upload_receipts FROM invoyis_archive_runtime;
GRANT SELECT, INSERT, UPDATE ON invoyis.upload_sessions TO invoyis_archive_runtime;
GRANT SELECT, INSERT ON invoyis.stored_objects, invoyis.submissions, invoyis.documents,
    invoyis.processing_jobs, invoyis.upload_receipts TO invoyis_archive_runtime;
GRANT SELECT ON invoyis.extraction_runs TO invoyis_archive_runtime;
GRANT INSERT ON invoyis.archive_invitation_outbox TO invoyis_archive_runtime;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA invoyis TO invoyis_archive_runtime;
REVOKE EXECUTE ON FUNCTION invoyis.stalled_upload_ids(integer) FROM invoyis_archive_runtime;
GRANT EXECUTE ON FUNCTION invoyis.upload_uploader(uuid,uuid) TO invoyis_archive_runtime;
SELECT 'CREATE ROLE invoyis_upload_reconciler LOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_upload_reconciler') \gexec
ALTER ROLE invoyis_upload_reconciler WITH LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'reconciler_password';
GRANT USAGE ON SCHEMA invoyis TO invoyis_upload_reconciler;
GRANT EXECUTE ON FUNCTION invoyis.stalled_upload_ids(integer) TO invoyis_upload_reconciler;
GRANT EXECUTE ON FUNCTION invoyis.expire_abandoned_uploads() TO invoyis_upload_reconciler;
GRANT EXECUTE ON FUNCTION invoyis.flag_upload_orphan(uuid,uuid) TO invoyis_upload_reconciler;
GRANT EXECUTE ON FUNCTION invoyis.defer_upload_reconcile(uuid,uuid) TO invoyis_upload_reconciler;
REVOKE EXECUTE ON FUNCTION invoyis.expire_abandoned_uploads() FROM invoyis_archive_runtime;
REVOKE EXECUTE ON FUNCTION invoyis.flag_upload_orphan(uuid,uuid) FROM invoyis_archive_runtime;
REVOKE EXECUTE ON FUNCTION invoyis.defer_upload_reconcile(uuid,uuid) FROM invoyis_archive_runtime;
SELECT 'CREATE ROLE invoyis_document_worker LOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_document_worker') \gexec
ALTER ROLE invoyis_document_worker WITH LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'worker_password';
GRANT USAGE ON SCHEMA invoyis TO invoyis_document_worker;
REVOKE ALL ON ALL TABLES IN SCHEMA invoyis FROM invoyis_document_worker;
GRANT EXECUTE ON FUNCTION invoyis.worker_reap(integer,integer),invoyis.worker_claim(integer,integer),
 invoyis.worker_heartbeat(uuid,uuid,integer),invoyis.worker_start_parser(uuid,uuid,integer),
 invoyis.worker_retry(uuid,uuid,text,integer,integer),invoyis.worker_fail(uuid,uuid,text),
 invoyis.worker_complete(uuid,uuid,text,text,text,jsonb,jsonb,jsonb),invoyis.worker_metrics()
 TO invoyis_document_worker;
REVOKE EXECUTE ON FUNCTION invoyis.worker_reap(integer,integer),invoyis.worker_claim(integer,integer),
 invoyis.worker_heartbeat(uuid,uuid,integer),invoyis.worker_start_parser(uuid,uuid,integer),
 invoyis.worker_retry(uuid,uuid,text,integer,integer),invoyis.worker_fail(uuid,uuid,text),
 invoyis.worker_complete(uuid,uuid,text,text,text,jsonb,jsonb,jsonb),invoyis.worker_metrics()
 FROM invoyis_archive_runtime,invoyis_upload_reconciler;
ALTER DEFAULT PRIVILEGES IN SCHEMA invoyis GRANT EXECUTE ON FUNCTIONS TO invoyis_archive_runtime;
SQL
done

if [ -n "${ARCHIVE_PROVISION_DATABASE_URL:-}" ]; then
  echo 'Provisioned restricted archive runtime role in requested database'
  exit 0
fi

ARCHIVE_RUNTIME_PASSWORD="$ARCHIVE_RUNTIME_PASSWORD" UPLOAD_RECONCILER_PASSWORD="$UPLOAD_RECONCILER_PASSWORD" DOCUMENT_WORKER_PASSWORD="$DOCUMENT_WORKER_PASSWORD" python3 - <<'PY'
from pathlib import Path
from urllib.parse import quote, urlsplit, urlunsplit
import os

path = Path('.env')
lines = path.read_text().splitlines()
settings = dict(line.split('=', 1) for line in lines if '=' in line and not line.startswith('#'))
password = os.environ['ARCHIVE_RUNTIME_PASSWORD']

def runtime_url(value, role="invoyis_archive_runtime", password=None):
    password = password or os.environ["ARCHIVE_RUNTIME_PASSWORD"]
    parsed = urlsplit(value)
    host = parsed.hostname or '127.0.0.1'
    if ':' in host:
        host = f'[{host}]'
    port = f':{parsed.port}' if parsed.port else ''
    auth = f'{role}:{quote(password, safe="")}@{host}{port}'
    return urlunsplit((parsed.scheme, auth, parsed.path, parsed.query, parsed.fragment))

updates = {
    'ARCHIVE_RUNTIME_PASSWORD': password,
    'UPLOAD_RECONCILER_PASSWORD': os.environ['UPLOAD_RECONCILER_PASSWORD'],
    'DOCUMENT_WORKER_PASSWORD': os.environ['DOCUMENT_WORKER_PASSWORD'],
    'DOCUMENT_WORKER_DATABASE_URL': runtime_url(settings['DATABASE_URL'], 'invoyis_document_worker', os.environ['DOCUMENT_WORKER_PASSWORD']),
    'UPLOAD_RECONCILER_DATABASE_URL': runtime_url(settings['DATABASE_URL'], 'invoyis_upload_reconciler', os.environ['UPLOAD_RECONCILER_PASSWORD']),
    'ARCHIVE_DATABASE_URL': runtime_url(settings['DATABASE_URL']),
    'TEST_ARCHIVE_DATABASE_URL': runtime_url(settings.get('ARCHIVE_TEST_DATABASE_URL', settings['TEST_DATABASE_URL'])),
}
lines = [line for line in lines if line.split('=', 1)[0] not in updates]
path.write_text('\n'.join(lines + [f'{key}={value}' for key, value in updates.items()]) + '\n')
PY
chmod 600 .env
echo 'Provisioned restricted archive runtime role in dev and test databases; connection URLs are in ignored .env'
