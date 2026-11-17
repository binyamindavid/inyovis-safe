#!/usr/bin/env python3
"""Provision restricted roles and write private Railway connection variables."""
import argparse
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
from urllib.parse import parse_qsl, quote, unquote, urlencode, urlsplit, urlunsplit

ROLES = {
    "ARCHIVE_DATABASE_URL": ("invoyis_archive_runtime", "ARCHIVE_RUNTIME_PASSWORD"),
    "UPLOAD_RECONCILER_DATABASE_URL": ("invoyis_upload_reconciler", "UPLOAD_RECONCILER_PASSWORD"),
    "DOCUMENT_WORKER_DATABASE_URL": ("invoyis_document_worker", "DOCUMENT_WORKER_PASSWORD"),
}
RUNTIME_OPTIONS = {"sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout", "application_name"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("owner_url")
    parser.add_argument("private_host")
    parser.add_argument("private_port", type=int)
    parser.add_argument("output_file", type=Path)
    parser.add_argument("--rotate", action="store_true")
    args = parser.parse_args()
    if not 1 <= args.private_port <= 65535:
        raise ValueError("Private port must be between 1 and 65535")
    host = args.private_host
    if not host or any(c in host for c in "/@?#"):
        raise ValueError("Invalid private host")
    if ":" in host and not (host.startswith("[") and host.endswith("]")):
        host = f"[{host}]"
    parsed = urlsplit(args.owner_url)
    if parsed.scheme not in ("postgres", "postgresql") or not parsed.username or not parsed.path or parsed.path == "/":
        raise ValueError("Owner URL must include a PostgreSQL user and database")
    _ = parsed.port
    options = parse_qsl(parsed.query, keep_blank_values=True)
    overrides = sorted({key for key, _ in options if key in {"host", "hostaddr", "port", "user", "password", "dbname", "service"}})
    if overrides:
        raise ValueError("Owner URL has connection-field overrides: " + ", ".join(overrides))
    excluded = sorted({key for key, _ in options if key not in RUNTIME_OPTIONS})
    if excluded:
        print("Ignoring owner-only or unsupported connection options: " + ", ".join(excluded), file=sys.stderr)
    query = urlencode([(key, value) for key, value in options if key in RUNTIME_OPTIONS])
    dbname = unquote(parsed.path[1:])
    if "/" in dbname:
        raise ValueError("Invalid database name")
    passwords = {}
    for _, env_name in ROLES.values():
        value = secrets.token_hex(24) if args.rotate else os.environ.get(env_name)
        if not value:
            raise ValueError(f"Set existing {env_name} or pass --rotate")
        passwords[env_name] = value

    def url(role, password):
        auth = f"{quote(role, safe='')}:{quote(password, safe='')}@{host}:{args.private_port}"
        return urlunsplit((parsed.scheme, auth, "/" + quote(dbname, safe=""), query, ""))

    api = {
        "DATABASE_URL": url(unquote(parsed.username), unquote(parsed.password or "")),
        "ARCHIVE_DATABASE_URL": url("invoyis_archive_runtime", passwords["ARCHIVE_RUNTIME_PASSWORD"]),
        "UPLOAD_RECONCILER_DATABASE_URL": url("invoyis_upload_reconciler", passwords["UPLOAD_RECONCILER_PASSWORD"]),
    }
    worker_values = {"DOCUMENT_WORKER_DATABASE_URL": url("invoyis_document_worker", passwords["DOCUMENT_WORKER_PASSWORD"])}
    output = args.output_file.absolute()
    worker = Path(str(output) + ".worker")
    for path in (output, worker):
        if path.exists() or path.is_symlink():
            raise ValueError("Output already exists; choose a new path")
        if not path.parent.is_dir():
            raise ValueError("Output directory does not exist")

    # Stage complete files on the destination filesystem before any role changes.
    staged = []
    old_umask = os.umask(0o077)
    try:
        for path, values in ((output, api), (worker, worker_values)):
            fd, name = tempfile.mkstemp(prefix=path.name + ".recovery-", dir=path.parent)
            staged.append(Path(name))
            with os.fdopen(fd, "w", encoding="utf-8") as stream:
                for key, value in values.items():
                    stream.write(f"{key}={value}\n")
                stream.flush()
                os.fsync(stream.fileno())
    except Exception:
        for path in staged:
            path.unlink(missing_ok=True)
        raise
    finally:
        os.umask(old_umask)
    env = os.environ.copy()
    env.update(passwords)
    env["ARCHIVE_PROVISION_DATABASE_URL"] = args.owner_url
    provision = Path(__file__).with_name("provision-archive-runtime.sh")
    try:
        subprocess.run([str(provision)], env=env, check=True)
    except Exception:
        for path in staged:
            path.unlink(missing_ok=True)
        raise
    try:
        for source, target in zip(staged, (output, worker)):
            if target.exists() or target.is_symlink():
                raise ValueError("Output appeared during provisioning")
            os.replace(source, target)
    except Exception:
        print("Provisioning committed. Recover credentials from these owner-only files:", file=sys.stderr)
        for path in (output, worker, *staged):
            if path.exists():
                print(path, file=sys.stderr)
        raise
    print("Wrote separate Railway API and worker variables files (owner-only permissions).")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        if isinstance(exc, subprocess.CalledProcessError):
            print("Provisioning failed; no variables files were published.", file=sys.stderr)
        else:
            print(str(exc), file=sys.stderr)
        sys.exit(1)
