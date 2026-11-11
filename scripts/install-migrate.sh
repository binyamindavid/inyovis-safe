#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p .local-bin
GOBIN="$(pwd)/.local-bin" go install -tags postgres -ldflags '-X main.Version=4.19.1' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1
test "$(.local-bin/migrate -version 2>&1)" = 4.19.1
