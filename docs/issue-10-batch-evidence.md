# Issue #10 local browser evidence — 2026-11-11

PR #43 remains draft. Tested on `codex/issue-10-batch-upload` with local PostgreSQL 17, SeaweedFS 4.53, Go API, document worker, Mailpit, HTTPS Next.js, and Chrome 157.0.8103.40. The PDFs in `.local-bin/issue35-fixtures` are synthetic. The signed-in account is a synthetic contributor in a local development archive. No real invoice or personal record was used.

## Repeatable setup

The local stack was already running on ports 3000, 8081, 8333, and 8025. `yarn dev:local` can start it when those ports are free. To run the browser scripts with an eligible, synthetic account and archive:

```sh
set -a; source .env; set +a
export BATCH_TEST_ARCHIVE_ID=<synthetic-local-archive-uuid>
export BATCH_TEST_STORAGE_STATE=/tmp/invoyis-issue10-auth.json
export BATCH_TEST_EMAIL=<eligible-synthetic-account@example.test>
node scripts/batch-upload/sign-in.mjs
node scripts/batch-upload/retry-paths.mjs
node scripts/batch-upload/checklist.mjs
node scripts/batch-upload/access.mjs
node scripts/batch-upload/worker-outcomes.mjs
node scripts/batch-upload/worker-real-failure.mjs
```

The sign-in script uses the normal HTTPS form and the new code delivered to local Mailpit. The scripts require the disposable `invoyis_safe_dev` database and the prior synthetic fixture directory. They intercept only browser requests to the local SeaweedFS or same-origin gateway. The worker-outcome script writes terminal states to **new synthetic documents only**, simulating both UI mappings. The separate real-worker script uploads a deliberately malformed synthetic PDF that passes intake and waits for the worker to report a terminal parser failure.

## Results

- `TestUploadStagingOutcomes` passed against `invoyis_safe_archive_test`, including missing staging, invalid bytes, transient read error, inaccessible session, completion replay, and one submission and job.
- Storage accepted bytes and its response was dropped: one storage transfer, one completion, one receipt, one submission, one job.
- Storage did not receive bytes: completion returned `UPLOAD_STAGED_OBJECT_MISSING`, the same session resent the file, then one receipt, submission, and job were committed. Two transfer attempts and two completions were observed.
- Completion committed but its response was dropped: retry completed the same session and recovered the receipt with one storage transfer, two completion calls, one submission, and one job.
- A batch containing a valid Hebrew-named PDF, a rejected text file, and a mixed Hebrew/Latin-named PDF with two dropped transfer attempts retained the first receipt. Only the failed row offered Retry; after retry, both valid rows had receipts.
- Different PDF bytes with the same filename produced two independent receipts.
- An expired grant was injected into the local session row and first browser response. The browser made a second create-session request with the same idempotency key and got the **same session ID**; both it and the next queued file were received. This run found and fixed a self-transition bug in `session_created`.
- An injected API 401 on the second row paused the queue and left the third queued. An injected API 429 with `Retry-After: 2` delayed the third session by 2,012 ms in the final run.
- In Hebrew RTL, a mixed Hebrew/Latin filename remained isolated in `<bdi dir="auto">`. The retry control was reachable by Tab and activated by Enter; focus remained on the row status. The polite region announced the file outcome. Reload cleared the page-local queue while the received document remained in the archive list. A document absent from the 20-item list still returned through `/documents/{id}/status`.
- Synthetic database state injection for `processing_failed` and `file_blocked` showed each outcome beside a retained receipt, with no upload retry. A separate malformed synthetic PDF also reached **Received**, then the real worker reported **Processing failed**. The browser retained receipt `2819ef5d-d83d-4024-b554-ae6640e03bf3` and offered no upload retry.

The SQL check used by `scripts/batch-upload/retry-paths.mjs` for each retry case is:

```sql
SELECT s.id, r.document_id,
  (SELECT count(*) FROM invoyis.submissions WHERE upload_session_id = s.id),
  (SELECT count(*) FROM invoyis.processing_jobs j WHERE j.document_id = r.document_id)
FROM invoyis.upload_sessions s
JOIN invoyis.upload_receipts r ON r.upload_session_id = s.id
WHERE s.id = $session_id;
```

Each count returned `1`. The browser scripts also asserted transfer and completion request counts. The final lost-completion run used session `f7e3b7f5-1efd-4c71-863c-216540cfac62` and document `ad262015-5cc4-456d-bcd9-f2713fd877ed`.

## Commands and limits

```sh
set -a; source .env; set +a
(cd apps/backend && go test ./internal/archive -run '^TestUploadStagingOutcomes$' -v -count=1)
scripts/bootstrap-local-db.sh
# Set UPLOAD_RECONCILER_TEST_DATABASE_URL to the test database with the existing restricted reconciler role.
(cd apps/backend && go test -count=1 ./...)
yarn workspace @invoyis/web test
yarn workspace @invoyis/contracts test
yarn check:generated
yarn workspace @invoyis/web build
```

The full Go suite initially found the two test databases at migration 11 and a missing reconciler test URL. After applying migration 12 and using the restricted reconciler URL for `invoyis_safe_archive_test`, `go test -count=1 ./...` passed. The web suite passed 36/36, the contract suite passed 3/3, generated client was current, and the web build passed. No browser scenario remains skipped. The real worker terminal-failure path was additionally verified with a malformed synthetic PDF; `file_blocked` presentation used synthetic database state injection.
