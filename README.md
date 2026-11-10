# Invoyis

Invoyis is an invoice archive. This repository contains the Next.js web app, Go API and worker entry points, and shared UI and contract workspaces.

## Local setup

Use Node.js 24.22.1 (see `.nvmrc`), Yarn 4.18.1 (via Corepack), Go 1.27.3 (see `apps/backend/go.mod`), and Docker with Compose v2. With nvm, run `nvm install` from the repository root.

```sh
corepack enable
yarn install --immutable
scripts/create-local-env.sh
```

Set `DATABASE_URL` and `TEST_DATABASE_URL` in the ignored `.env`. For the local databases `invoyis_safe_dev` and `invoyis_safe_dev_test`, use the passwordless `benjamindavid` account:

```dotenv
DATABASE_URL=postgres://benjamindavid@127.0.0.1:5432/invoyis_safe_dev?sslmode=disable
TEST_DATABASE_URL=postgres://benjamindavid@127.0.0.1:5432/invoyis_safe_dev_test?sslmode=disable
```

Apply the bootstrap migration to both databases with the local PostgreSQL client, then start storage and mail:

```sh
scripts/bootstrap-local-db.sh
docker compose up -d --wait storage mail
```

If your Node.js installation omits Corepack, install it once with `npm install --global corepack` before running `corepack enable`.

Start the three application processes from the repository root in separate terminals.

```sh
yarn dev
```

```sh
cd apps/backend
go run ./cmd/api
```

```sh
cd apps/backend
go run ./cmd/worker
```

| Component | Local endpoint |
| --- | --- |
| Web | [http://127.0.0.1:3000](http://127.0.0.1:3000) |
| Go API liveness | [http://127.0.0.1:8080/healthz](http://127.0.0.1:8080/healthz) |
| PostgreSQL | Local `invoyis_safe_dev` on port 5432; `invoyis_safe_dev_test` for tests |
| S3-compatible API | `http://127.0.0.1:8333`, path-style, region `us-east-1`, private bucket `invoyis-originals` |
| S3 local credentials | `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` in `.env` |
| Mailpit inbox | [http://127.0.0.1:8025](http://127.0.0.1:8025), local SMTP at `127.0.0.1:1025` |

The API serves a liveness endpoint and the worker waits for shutdown. The setup script generates random S3 credentials in the ignored `.env`; it refuses to overwrite an existing file. Local PostgreSQL needs no password. The bootstrap script records migration version 1 in `public.schema_migrations` so later `golang-migrate` runs can continue from it. Local PostgreSQL data remains in the two databases. Stop storage and mail with `docker compose down`; the SeaweedFS volume persists. CI creates a disposable PostgreSQL container with a fresh password.

## Checks and layout

Run `yarn build` from the root and `go build ./...` and `go vet ./...` from `apps/backend`. GitHub Actions also applies, rolls back and reapplies the SQL migrations on a disposable PostgreSQL instance.

- `apps/web` contains the Next.js App Router.
- `apps/backend` contains the Go module, the API and worker, and paired SQL migrations.
- `packages/ui` holds the shared UI workspace. The shadcn/ui, React Aria and Hebrew RTL foundation is tracked in [issue #5](https://github.com/binyamindavid/inyovis-safe/issues/5).
- `packages/contracts` holds the shared contracts workspace. The OpenAPI and generated client work is tracked in [issue #2](https://github.com/binyamindavid/inyovis-safe/issues/2).
