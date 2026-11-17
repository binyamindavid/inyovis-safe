#!/bin/sh
set -eu

# Fetch official signatures before the worker validates the database.
freshclam --datadir="$CLAMAV_DATABASE" --stdout
# Keep the signatures fresh for the lifetime of the container.
freshclam --datadir="$CLAMAV_DATABASE" --stdout --daemon --pid=/tmp/freshclam.pid
exec document-worker
