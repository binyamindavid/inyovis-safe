#!/usr/bin/env python3
"""Integration regression: COMMIT succeeds but the psql acknowledgement is lost.

Run with TEST_PROVISION_DATABASE_URL pointing at a migrated disposable database.
The test rotates cluster-level PostgreSQL roles; never point it at production.
"""
import os
from pathlib import Path
import shlex
import shutil
import stat
import subprocess
import tempfile
from urllib.parse import urlsplit

owner_url = os.environ["TEST_PROVISION_DATABASE_URL"]
parsed = urlsplit(owner_url)
real_psql = shutil.which("psql")
assert real_psql is not None
script = Path(__file__).with_name("provision-railway-runtime.sh")
with tempfile.TemporaryDirectory() as temporary:
    directory = Path(temporary)
    shim = directory / "psql"
    shim.write_text(
        "#!/bin/sh\n"
        + shlex.quote(real_psql) + ' "$@"\n'
        + "status=$?\n"
        + 'if [ "$status" -ne 0 ]; then exit "$status"; fi\n'
        + "exit 47\n", encoding="utf-8"
    )
    shim.chmod(0o700)
    output = directory / "railway-api.env"
    env = os.environ.copy()
    env["PATH"] = str(directory) + os.pathsep + env["PATH"]
    result = subprocess.run(
        [str(script), owner_url, parsed.hostname or "127.0.0.1", str(parsed.port or 5432), str(output), "--rotate"],
        env=env, capture_output=True, text=True,
    )
    assert result.returncode != 0, "simulated lost acknowledgement should fail the command"
    assert not output.exists() and not Path(str(output) + ".worker").exists()
    recovery = sorted(directory.glob("*.recovery-*"))
    assert len(recovery) == 2, "both generated credential files must survive an uncertain COMMIT"
    assert all(stat.S_IMODE(path.stat().st_mode) == 0o600 for path in recovery)
    values = {}
    for path in recovery:
        values.update(line.split("=", 1) for line in path.read_text().splitlines())
    for key, role in (
        ("ARCHIVE_DATABASE_URL", "invoyis_archive_runtime"),
        ("UPLOAD_RECONCILER_DATABASE_URL", "invoyis_upload_reconciler"),
        ("DOCUMENT_WORKER_DATABASE_URL", "invoyis_document_worker"),
    ):
        connected = subprocess.run(
            [real_psql, values[key], "-X", "-Atqc", "SELECT current_user"],
            capture_output=True, text=True,
        )
        assert connected.returncode == 0 and connected.stdout.strip() == role, key
    assert "Provisioning outcome is uncertain" in result.stderr
    print("uncertain COMMIT retained both owner-only recovery files; all rotated roles connected")
