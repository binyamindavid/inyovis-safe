# Invoyis

Invoyis is an invoice archive. This repository contains the Next.js web app, Go API and worker entry points, and shared UI and contract workspaces.

## Local setup

Use Node.js 24.22.1 (see `.nvmrc`), Yarn 4.18.1 (via Corepack), Go 1.27.3 (see `apps/backend/go.mod`), PostgreSQL, and Mailpit. Docker with Compose v2 can run storage and mail. With nvm, run `nvm install` from the repository root.

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

Apply both migrations to the local databases and seed only the synthetic pilot account:

```sh
scripts/bootstrap-local-db.sh
scripts/seed-synthetic-account.sh
```

The ignored `.env` also contains random `AUTH_SECRET` and `API_GATEWAY_SECRET`, `BROWSER_ORIGIN=https://localhost:3000`, `API_BASE_URL=http://127.0.0.1:8081`, and local mail settings. If `.env` predates this setup, update its browser origin and add any missing values using `openssl rand -hex 32` for each secret. The gateway secret must match in Next.js and Go. `AUTH_SECRET` protects code digests and encrypts pending outbox messages. Never commit either secret.

For local HTTPS and Mailpit without Docker, install and trust a local development certificate:

```sh
brew install mkcert mailpit
TRUST_STORES=system mkcert -install
mkdir -p .local-certs
mkcert -cert-file .local-certs/localhost.pem -key-file .local-certs/localhost-key.pem localhost 127.0.0.1
```

Start the full local stack from the repository root with one command:

```sh
yarn dev:local
```

This loads `.env` for Go and Next, starts Mailpit, and serves Next directly over HTTPS on port 3000. Press Ctrl+C to stop all three services. Ports 3000, 8081, 8025, and 1025 must be free.

Open [https://localhost:3000/sign-in](https://localhost:3000/sign-in) and use `pilot@example.test`; the code appears only in Mailpit. Use HTTPS because Go checks the exact browser origin. The certificate and key are ignored under `.local-certs`. For a repeatable synthetic HTTPS, Mailpit, and PostgreSQL check, run `NODE_EXTRA_CA_CERTS="$(mkcert -CAROOT)/rootCA.pem" node scripts/verify-local-auth.mjs` from a shell with `.env` loaded. That script seeds additional synthetic accounts and changes only their auth records in the dev database.

For production delivery set `MAIL_DRIVER=sendgrid`, `SENDGRID_API_KEY`, and `MAIL_FROM` to a verified sender address. The API uses the fixed SendGrid Mail Send endpoint; failures stay behind a neutral browser response and the encrypted outbox retries until the 10-minute challenge expires. For local delivery use `MAIL_DRIVER=smtp`, `SMTP_ADDR=127.0.0.1:1025`, and `MAIL_FROM=no-reply@invoyis.test`. Configure `TRUSTED_CLIENT_IP_HEADER` only for an ingress header that a trusted reverse proxy overwrites; otherwise the gateway uses the loopback IP bucket. Keep Go on a private network and use `API_GATEWAY_SECRET` between Next and Go.

Codes expire after 10 minutes and are invalidated on resend or first successful redemption. Request and verification counters live in PostgreSQL by normalized email and trusted client IP. Sessions expire after 24 hours and are revoked in the database on logout. All state-changing auth requests require the configured HTTPS Origin and `X-Invoyis-Intent: browser`.

If your Node.js installation omits Corepack, install it once with `npm install --global corepack` before running `corepack enable`.

The standby worker can be started separately with `cd apps/backend && go run ./cmd/worker`.

| Component | Local endpoint |
| --- | --- |
| HTTPS web and sign-in | [https://localhost:3000/sign-in](https://localhost:3000/sign-in) |
| Go API liveness | [http://127.0.0.1:8081/healthz](http://127.0.0.1:8081/healthz) |
| PostgreSQL | Local `invoyis_safe_dev` on port 5432; `invoyis_safe_dev_test` for tests |
| S3-compatible API | `http://127.0.0.1:8333`, path-style, region `us-east-1`, private bucket `invoyis-originals` |
| S3 local credentials | `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` in `.env` |
| Mailpit inbox | [http://127.0.0.1:8025](http://127.0.0.1:8025), local SMTP at `127.0.0.1:1025` |

The API serves a direct liveness endpoint and the worker waits for shutdown. The setup script refuses to overwrite an existing `.env`. Local PostgreSQL needs no password. The bootstrap script records migration version 2 in `public.schema_migrations` so later `golang-migrate` runs can continue from it. Local PostgreSQL data remains in the two databases. CI creates a disposable PostgreSQL container with a fresh password.

## Checks and layout

Run `yarn build` from the root and `go build ./...` and `go vet ./...` from `apps/backend`. GitHub Actions also applies, rolls back and reapplies the SQL migrations on a disposable PostgreSQL instance.

Run `yarn check:generated`, `yarn workspace @invoyis/web test`, and `yarn workspace @invoyis/contracts test`. Run `go test ./...` from `apps/backend`; when `TEST_DATABASE_URL` points to a disposable `_test` database, the auth integration tests truncate only the auth tables there. CI also runs these tests against a disposable PostgreSQL database.

The browser's `/api/v1/health` request goes through Next.js to Go's `/healthz`. To point the Next server at another Go process, set the server-only `API_BASE_URL` environment variable before starting Next.js. The direct `/healthz` liveness response remains available on Go's port.

- `apps/web` contains the Next.js App Router.
- `apps/backend` contains the Go module, the API and worker, and paired SQL migrations.
- `packages/ui` holds the shared UI workspace. The shadcn/ui, React Aria and Hebrew RTL foundation is tracked in [issue #5](https://github.com/binyamindavid/inyovis-safe/issues/5).
- `packages/contracts` holds the shared contracts workspace. The OpenAPI and generated client work is tracked in [issue #2](https://github.com/binyamindavid/inyovis-safe/issues/2).
