#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
set -a
. ./.env
set +a
: "${DATABASE_URL:?Set DATABASE_URL in .env}"
: "${TEST_DATABASE_URL:?Set TEST_DATABASE_URL in .env}"

umask 077
if [ -z "${ARCHIVE_RUNTIME_PASSWORD:-}" ]; then
  ARCHIVE_RUNTIME_PASSWORD=$(openssl rand -hex 24)
fi

for database_url in "$DATABASE_URL" "$TEST_DATABASE_URL" ${ARCHIVE_TEST_DATABASE_URL:+"$ARCHIVE_TEST_DATABASE_URL"}; do
  psql "$database_url" -X -q -v ON_ERROR_STOP=1 -v runtime_password="$ARCHIVE_RUNTIME_PASSWORD" <<'SQL'
SELECT 'CREATE ROLE invoyis_archive_runtime LOGIN' WHERE NOT EXISTS (
    SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime'
) \gexec
ALTER ROLE invoyis_archive_runtime WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'runtime_password';
GRANT USAGE ON SCHEMA invoyis TO invoyis_archive_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    invoyis.archives, invoyis.archive_memberships, invoyis.archive_invitations, invoyis.archive_audit
    TO invoyis_archive_runtime;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA invoyis TO invoyis_archive_runtime;
SQL
done

ARCHIVE_RUNTIME_PASSWORD="$ARCHIVE_RUNTIME_PASSWORD" python3 - <<'PY'
from pathlib import Path
from urllib.parse import quote, urlsplit, urlunsplit
import os

path = Path('.env')
lines = path.read_text().splitlines()
settings = dict(line.split('=', 1) for line in lines if '=' in line and not line.startswith('#'))
password = os.environ['ARCHIVE_RUNTIME_PASSWORD']

def runtime_url(value):
    parsed = urlsplit(value)
    host = parsed.hostname or '127.0.0.1'
    if ':' in host:
        host = f'[{host}]'
    port = f':{parsed.port}' if parsed.port else ''
    auth = f'invoyis_archive_runtime:{quote(password, safe="")}@{host}{port}'
    return urlunsplit((parsed.scheme, auth, parsed.path, parsed.query, parsed.fragment))

updates = {
    'ARCHIVE_RUNTIME_PASSWORD': password,
    'ARCHIVE_DATABASE_URL': runtime_url(settings['DATABASE_URL']),
    'TEST_ARCHIVE_DATABASE_URL': runtime_url(settings.get('ARCHIVE_TEST_DATABASE_URL', settings['TEST_DATABASE_URL'])),
}
lines = [line for line in lines if line.split('=', 1)[0] not in updates]
path.write_text('\n'.join(lines + [f'{key}={value}' for key, value in updates.items()]) + '\n')
PY
chmod 600 .env
echo 'Provisioned restricted archive runtime role in dev and test databases; connection URLs are in ignored .env'
