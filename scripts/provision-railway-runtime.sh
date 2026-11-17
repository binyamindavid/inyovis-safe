#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
usage() {
  echo 'Usage: scripts/provision-railway-runtime.sh OWNER_URL PRIVATE_HOST PRIVATE_PORT OUTPUT_FILE [--rotate]' >&2
  exit 2
}
[ "$#" -ge 4 ] && [ "$#" -le 5 ] || usage
owner_url=$1
private_host=$2
private_port=$3
output=$4
rotate=${5:-}
[ -z "$rotate" ] || [ "$rotate" = '--rotate' ] || usage
case "$private_port" in ''|*[!0-9]*) usage ;; esac
[ "$private_port" -ge 1 ] && [ "$private_port" -le 65535 ] || usage
[ -n "$private_host" ] || usage
[ ! -e "$output" ] || { echo 'Output already exists; choose a new path.' >&2; exit 1; }
[ ! -e "$output.worker" ] || { echo 'Worker output already exists; choose a new path.' >&2; exit 1; }

umask 077
if [ "$rotate" = '--rotate' ]; then
  ARCHIVE_RUNTIME_PASSWORD=$(openssl rand -hex 24)
  UPLOAD_RECONCILER_PASSWORD=$(openssl rand -hex 24)
  DOCUMENT_WORKER_PASSWORD=$(openssl rand -hex 24)
else
  : "${ARCHIVE_RUNTIME_PASSWORD:?Set existing ARCHIVE_RUNTIME_PASSWORD or pass --rotate}"
  : "${UPLOAD_RECONCILER_PASSWORD:?Set existing UPLOAD_RECONCILER_PASSWORD or pass --rotate}"
  : "${DOCUMENT_WORKER_PASSWORD:?Set existing DOCUMENT_WORKER_PASSWORD or pass --rotate}"
fi
export ARCHIVE_RUNTIME_PASSWORD UPLOAD_RECONCILER_PASSWORD DOCUMENT_WORKER_PASSWORD
ARCHIVE_PROVISION_DATABASE_URL=$owner_url "$root/scripts/provision-archive-runtime.sh" >&2

OWNER_URL=$owner_url PRIVATE_HOST=$private_host PRIVATE_PORT=$private_port OUTPUT_FILE=$output python3 - <<'PY'
import os
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit, urlunsplit

parsed = urlsplit(os.environ['OWNER_URL'])
if parsed.scheme not in ('postgres', 'postgresql') or not parsed.path or parsed.path == '/':
    raise SystemExit('Owner URL must include a PostgreSQL database name')
if parsed.username is None:
    raise SystemExit('Owner URL must include a username')
host = os.environ['PRIVATE_HOST']
if any(c in host for c in '/@?#'):
    raise SystemExit('Invalid private host')
if ':' in host and not (host.startswith('[') and host.endswith(']')):
    host = f'[{host}]'
port = os.environ['PRIVATE_PORT']

def url(role, password):
    authority = f'{quote(role, safe="")}:{quote(password, safe="")}@{host}:{port}'
    return urlunsplit((parsed.scheme, authority, parsed.path, parsed.query, ''))

owner_password = unquote(parsed.password or '')
values = {
    'DATABASE_URL': url(unquote(parsed.username), owner_password),
    'ARCHIVE_DATABASE_URL': url('invoyis_archive_runtime', os.environ['ARCHIVE_RUNTIME_PASSWORD']),
    'UPLOAD_RECONCILER_DATABASE_URL': url('invoyis_upload_reconciler', os.environ['UPLOAD_RECONCILER_PASSWORD']),
}
path = Path(os.environ['OUTPUT_FILE'])
with path.open('x', encoding='utf-8') as stream:
    for key, value in values.items():
        stream.write(f'{key}={value}\n')
path.chmod(0o600)
worker_path = Path(str(path) + '.worker')
with worker_path.open('x', encoding='utf-8') as stream:
    stream.write('DOCUMENT_WORKER_DATABASE_URL=' + url('invoyis_document_worker', os.environ['DOCUMENT_WORKER_PASSWORD']) + '\n')
worker_path.chmod(0o600)
PY
echo 'Wrote separate Railway API and worker variables files (owner-only permissions).'
