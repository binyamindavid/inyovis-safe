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

Apply the migrations to the local databases and seed only the synthetic pilot account:

```sh
scripts/bootstrap-local-db.sh
scripts/provision-archive-runtime.sh
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

For production delivery set `MAIL_DRIVER=sendgrid`, `SENDGRID_API_KEY`, and `MAIL_FROM` to a verified sender address. The API uses the fixed SendGrid Mail Send endpoint; failures stay behind a neutral browser response and the encrypted outbox retries until the 10-minute challenge expires. For local delivery use `MAIL_DRIVER=smtp`, `SMTP_ADDR=127.0.0.1:1025`, and `MAIL_FROM=no-reply@invoyis.test`. The outbox leases up to 16 messages at a time, sends up to eight concurrently outside its claim transaction, and retries failures. The lease is calculated from the batch size, concurrency, send deadline, acknowledgement retries, and a 30-second margin. Undecryptable messages are discarded, since retrying them cannot recover a code after a key change. Delivery is at least once: a provider acceptance followed by a database acknowledgement failure can still produce a duplicate email.

Outside loopback development, `TRUSTED_CLIENT_IP_HEADER` is required for both Next and Go. Configure it only for a single-IP header that a trusted ingress **overwrites** on every request, never a value accepted directly from browsers. If that header is missing or malformed, the gateway returns a structured `503` instead of putting all clients in one rate-limit bucket. Keep Go on a private network and use `API_GATEWAY_SECRET` between Next and Go.

Codes expire after 10 minutes and are invalidated on resend or first successful redemption. Request and verification counters live in PostgreSQL by normalized email and trusted client IP, including a 50-verification-per-email daily cap. Sessions expire after 24 hours and are revoked in the database on logout. Old challenges, rate counters, and sessions are deleted periodically. All state-changing auth requests require the configured HTTPS Origin and `X-Invoyis-Intent: browser`.

If your Node.js installation omits Corepack, install it once with `npm install --global corepack` before running `corepack enable`.

After migration 10 and role provisioning, configure a storage identity that can read originals but cannot write or delete them. Set its credentials as `WORKER_S3_ACCESS_KEY_ID` and `WORKER_S3_SECRET_ACCESS_KEY` in `.env`, then start the document worker from the repository root with `set -a; . ./.env; set +a; (cd apps/backend && CGO_ENABLED=0 go run ./cmd/worker)`. It requires the dedicated `DOCUMENT_WORKER_DATABASE_URL`, Poppler `pdfdetach`, and ClamAV `clamscan`. Set `CLAMAV_DATABASE` to a directory containing nonempty signatures refreshed within 48 hours (or `CLAMAV_MAX_SIGNATURE_AGE`, up to seven days). In `WORKER_PRODUCTION=1`, a fresh official `daily.cvd` or `daily.cld` and a Linux cgroup memory limit at or below 1 GiB are required at startup. On Linux the isolated parser also requires `bwrap`; on macOS it uses `sandbox-exec`. Missing or stale signatures and scanner or parser outages keep jobs pending and retry within the configured budget.

| Component | Local endpoint |
| --- | --- |
| HTTPS web and sign-in | [https://localhost:3000/sign-in](https://localhost:3000/sign-in) |
| Go API liveness | [http://127.0.0.1:8081/healthz](http://127.0.0.1:8081/healthz) |
| PostgreSQL | Local `invoyis_safe_dev` on port 5432; `invoyis_safe_dev_test` for tests |
| S3-compatible API | `http://127.0.0.1:8333`, path-style, region `us-east-1`, private bucket `invoyis-originals` |
| S3 local credentials | `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` in `.env` |
| Mailpit inbox | [http://127.0.0.1:8025](http://127.0.0.1:8025), local SMTP at `127.0.0.1:1025` |

The API serves a direct liveness endpoint. The API process requires working PostgreSQL and mail configuration before it starts; if either is unavailable, `/healthz` is unavailable too. The setup script refuses to overwrite an existing `.env`. Local PostgreSQL needs no password. The bootstrap script records migration version 10 in `public.schema_migrations` so later `golang-migrate` runs can continue from it. It also upgrades an existing archive test database with a recognizable schema but no ledger. Local PostgreSQL data remains in the databases. CI creates a disposable PostgreSQL container with a fresh password.

## Checks and layout

Run `yarn build` from the root and `go build ./...` and `go vet ./...` from `apps/backend`. GitHub Actions also applies, rolls back and reapplies the SQL migrations on a disposable PostgreSQL instance.

Run `yarn check:generated`, `yarn workspace @invoyis/web test`, and `yarn workspace @invoyis/contracts test`. Run `go test ./...` from `apps/backend`; when `TEST_DATABASE_URL` points to a disposable `_test` database, the auth integration tests truncate only the auth tables there. CI also runs these tests against a disposable PostgreSQL database.

The browser's `/api/v1/health` request goes through Next.js to Go's `/healthz`. To point the Next server at another Go process, set the server-only `API_BASE_URL` environment variable before starting Next.js. The direct `/healthz` liveness response remains available on Go's port.

- `apps/web` contains the Next.js App Router.
- `apps/backend` contains the Go module, the API and worker, and paired SQL migrations.
- `packages/ui` holds the shared UI workspace. The shadcn/ui, React Aria and Hebrew RTL foundation is tracked in [issue #5](https://github.com/binyamindavid/inyovis-safe/issues/5).
- `packages/contracts` holds the shared contracts workspace. The OpenAPI and generated client work is tracked in [issue #2](https://github.com/binyamindavid/inyovis-safe/issues/2).

## Archives and invitations

Run `scripts/bootstrap-local-db.sh` to apply migrations through 10, then `scripts/provision-archive-runtime.sh`. The latter creates restricted archive, upload reconciler, and document worker roles and writes their URLs to the ignored `.env`. The worker role has execute-only access to the processing functions and no direct intake-table grants. The Go API requires `ARCHIVE_DATABASE_URL`; the worker requires `DOCUMENT_WORKER_DATABASE_URL`. Use the migration owner in `DATABASE_URL` only for migrations and the existing auth store. For a separate disposable archive integration database ending in `_test`, set `ARCHIVE_TEST_DATABASE_URL` before bootstrapping and provisioning; the script writes its restricted archive URL as `TEST_ARCHIVE_DATABASE_URL`. Run migrations and role provisioning as the same PostgreSQL migration owner so default function grants cover future migrations. Rotate the generated passwords by rerunning the provision script. Never commit the URLs.

Sign in with the synthetic pilot account, then open [https://localhost:3000/archives](https://localhost:3000/archives). Create a Personal archive or a Business archive with a two-letter jurisdiction. Membership and export grants are scoped to each archive. Every new member, including an admin, starts with bulk export disabled. Any archive admin can invite a new email address, subject to five new-account invitations per admin per 30 days. All invitations also have persisted limits of 20 per admin, 50 per archive, and three per recipient per 24 hours. Invitations are encrypted in a database queue; transient delivery errors are retried up to ten attempts and permanent provider rejections stop immediately. Delivery failures are logged without provider details but are not yet shown in the admin UI; Mailpit receives a one-use link. The email uses fixed copy without the user-chosen archive name. An invitee without an account follows that link, signs in with the normal six-digit email code, and accepts the invitation. An uninvited email still cannot enroll. Changing interface language on the archive switcher updates the account only; archive jurisdiction remains unchanged.

The restricted role and RLS protect against application query mistakes; a stolen runtime database credential can set the request identity and is not contained by RLS. The invitation URL carries its token in a fragment, which the browser moves to session storage before sign-in. The browser binds a new account's code challenge to that invitation. Code verification creates a pending, ineligible account and temporary session; normal archive access stays closed until token redemption atomically creates membership and enables the account. Go stores only a token digest. Email delivery uses the existing SMTP/Mailpit or SendGrid adapter. The archive API uses the restricted role, sets account and archive IDs for each database transaction, and checks membership on every request. The migrations enable PostgreSQL RLS for archives, memberships, invitations, invitation queue inserts, and audit rows. Invitation audit entries omit the invitee email; the invitation table retains it until a future retention policy is defined. The integration test verifies missing identity and pooled connection reuse. Document intake and bulk export generation are separate issues; the role and explicit export grant are already defined for those endpoints.

### Web UI foundation

Shared controls in `packages/ui` use React Aria Components for keyboard, focus, and accessibility behavior. Their semantic colors, borders, radii, and variants follow the shadcn/ui visual vocabulary in `packages/ui/base.css`. The project does not install the shadcn CLI or Tailwind; React Aria owns each control's interaction. The synthetic document table on `/archives` is labelled as fixture data and does not query live documents.

## Single-file upload intake

Archive admins and contributors can upload one PDF from an archive page. The browser posts bytes directly to private S3-compatible staging; the Next.js gateway forwards only session creation and completion controls. Set `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_BUCKET` (default `invoyis-originals`), and `S3_ENDPOINT` (default `127.0.0.1:8333`). Set `S3_BROWSER_ENDPOINT` if the browser reaches storage at a different URL. `UPLOAD_MAX_BYTES` defaults to 25 MiB; the grant lasts 15 minutes and finalisation must start within 24 hours. The API requires `UPLOAD_RECONCILER_DATABASE_URL` for automatic recovery. Re-run `scripts/provision-archive-runtime.sh` after migration 10 to provision the dedicated discovery role. The original remains private, and only a verified receipt says “Received · Pending processing.” See [the direct upload ADR](docs/adr/0002-direct-upload-intake.md).

## Review metadata (issue #8)

After applying migration 10 and reprovisioning the restricted archive role, cleared documents expose archive-scoped review and page-preview endpoints. Draft and confirmed metadata are append-only revisions. `documents.current_confirmed_revision_id` is the stable source for the later CSV index; a subsequent draft does not change it. `review_version` and a UUID idempotency key are required on each metadata write. Preview uses Poppler in the credential-free isolated child, stores private PNG derivatives in PostgreSQL, and limits pages to the first ten. The API host needs `pdfinfo`, `pdftoppm`, and the same sandbox facility used by the processing parser.
