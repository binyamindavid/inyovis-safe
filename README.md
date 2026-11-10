# Invoyis

Invoyis is an invoice archive. This repository contains the Next.js web app, Go API and worker entry points, and shared UI and contract workspaces.

## Local setup

Use Node.js 24.22.1 (see `.nvmrc`), npm 11.9.0, Go 1.27.3 (see `apps/backend/go.mod`), and Docker with Compose v2. With nvm, run `nvm install` from the repository root.

```sh
npm install --global npm@11.9.0
npm ci
docker compose up -d --wait db storage mail
docker compose run --rm migrate up
```

Start the three application processes from the repository root in separate terminals.

```sh
npm run dev
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
| PostgreSQL | `postgres://invoyis:invoyis-local@127.0.0.1:54329/invoyis?sslmode=disable` |
| S3-compatible API | `http://127.0.0.1:8333`, path-style, region `us-east-1`, private bucket `invoyis-originals` |
| S3 local credentials | Access key `invoyis`, secret key `invoyis-storage-local` |
| Mailpit inbox | [http://127.0.0.1:8025](http://127.0.0.1:8025), local SMTP at `127.0.0.1:1025` |

The API serves a liveness endpoint and the worker waits for shutdown. The Compose credentials and mail capture are for local development. Stop the infrastructure with `docker compose down`. The PostgreSQL and SeaweedFS volumes persist between restarts.

## Checks and layout

Run `npm run build` from the root and `go build ./...` and `go vet ./...` from `apps/backend`. GitHub Actions also applies, rolls back and reapplies the SQL migrations on a disposable PostgreSQL instance.

- `apps/web` contains the Next.js App Router.
- `apps/backend` contains the Go module, the API and worker, and paired SQL migrations.
- `packages/ui` holds the shared UI workspace. The shadcn/ui, React Aria and Hebrew RTL foundation is tracked in [issue #5](https://github.com/binyamindavid/inyovis-safe/issues/5).
- `packages/contracts` holds the shared contracts workspace. The OpenAPI and generated client work is tracked in [issue #2](https://github.com/binyamindavid/inyovis-safe/issues/2).
