# Document disposal verification record

Status: **Issue #76 draft. Permanent shredding is disabled by default and must not be enabled in production.**

## Draft shredding path

- An archive admin must type the current original filename in the separate **Shred permanently** confirmation. A Personal reminder never enters the authorization decision. Business archives additionally require a current, calculated decision from the validated active rule scope and a date strictly after the inclusive keep-through day. The 30-day Trash deadline never calls or authorizes this path; a document in Trash must first be restored.
- `ENABLE_DOCUMENT_SHREDDING=true` is an explicit test-only gate pending the release checks below. The final transaction locks permission, archive, document, active rule set and original object; it rechecks the confirmed revision, clearance, holds, worker and preview claims, exports, decision fingerprint and date. It creates one durable shred record and one original-object cleanup ledger row if the original is not shared. A shared original remains in storage until every live reference is gone.
- The reconciler deletes only the key recorded for the document and stored object, then checks that the active object is absent. Failures remain visible and retry with a lease. A crash after object deletion can retry and verify absence. Database preview images are removed in the authorization transaction. The visible result says **pending**, **failed**, or **active storage verified**; it never asserts that backups vanished.
- Existing issued export bundles and in-progress exports block authorization. They must expire and be removed by the export cleanup worker before shredding can proceed. Existing processing and preview claims block authorization. New document access is hidden after authorization.

## Open release blockers

- The current draft retains document metadata, revisions, extraction data, upload sessions and prepared/staging copies. This is not yet a minimal audit record. The persistent `stored_objects` row also prevents a later upload of identical bytes after its key is removed. Complete the relational cleanup and deduplication lifecycle before enabling the gate.
- Historical orphan keys from migration 17 and duplicate upload sessions are not yet proven to belong to a single document. Inventory them separately, establish ownership and retention evidence, then reconcile them without deleting keys still referenced by another document.
- A verified backup expiry policy and a restore rehearsal for the corresponding private originals are not documented. Operators must provide this before release; the API does not claim immediate backup erasure.
- Run real object-storage failure and versioning tests, all blocker and concurrency tests, and browser verification on desktop and mobile in English, French and Hebrew RTL, including keyboard use. Attach screenshots and exact commands to the draft PR before release.

## Current behavior

- Moving a document to Trash records the actor, reason, document ID, timestamp, and 30-day recovery deadline in a durable event table and archive audit. A repeated move returns the same deadline.
- Runtime reads of `documents` hide trashed rows by default. The Trash API opts in within one archive-scoped transaction. Restore is available to an admin or contributor before the deadline.
- No physical deletion job runs when a document is moved to Trash or when the recovery deadline passes. The document remains in Trash after the deadline and cannot be restored through the current API. Operators must not describe this as erasure.
- Issued export bundles remain downloadable until their existing expiry. New exports should omit trashed documents through the restrictive document read policy. Backup copies remain subject to the existing backup lifecycle; no immediate backup erasure is asserted.
- Shared original bytes remain in object storage. Existing duplicate consolidation may have left old, unreferenced keys for separate operational cleanup; this implementation does not delete them.

## Checks run

- `yarn generate:client`: passed.
- `yarn build`: passed, including TypeScript.
- Go retention blocker unit test: passed.
- Restricted-role Trash integration test: added but **skipped** because `ARCHIVE_TEST_DATABASE_URL` and `TEST_ARCHIVE_DATABASE_URL` are not configured in this workspace.
- `git diff --check`: passed.

## Required before release

- Final transaction must lock and re-evaluate current retention decision, published rule coverage, confirmed revision, clearance, inclusive keep-through date, active holds, and actor permission.
- A durable per-object cleanup ledger must handle original and derived objects, shared references, object-store failure, retries, crash recovery, pending workers, export bundles and expiry, and backup expiry. Historic orphan keys from original consolidation need an explicit reconciliation procedure.
- Run restricted-role PostgreSQL tests against a disposable test database and verify English, French, and Hebrew RTL on desktop, mobile, and keyboard. Capture screenshots from those runs.

## Draft verification on 2026-11-19

- Applied migrations 1–31 to a disposable PostgreSQL 17 database and provisioned `invoyis_archive_runtime` without ownership or bypass RLS.
- `go test ./internal/archive -run '^Test(PersonalShredRestrictedRoleHoldAndIdempotency|BusinessShredRequiresFreshValidatedDecision|ShredPreservesSharedOriginal)$' -count=1 -v`: passed. These tests cover the Personal admin path, role and archive isolation, hold and export blockers, Trash state, stale and missing Business decisions, duplicate requests, shared originals, a simulated storage failure, retry and recovery after deletion before acknowledgement.
- `go test ./internal/archive -count=1`: passed after the restricted upload reconciler URL was configured. The first full-suite attempt failed because that URL was omitted; it was a test setup failure, not a product failure.
- `yarn generate:client` and `yarn build`: passed. `node scripts/verify-shredding-browser.mjs`: passed with mocked API data in desktop and mobile viewports for English, French and Hebrew RTL, including keyboard confirmation and pending state. Screenshots are in `docs/screenshots/shredding-draft/`.
- Browser screenshots use mocked responses. They do not prove a live API and object-store end-to-end flow. Actual object-storage and backup lifecycle verification remain open release blockers.
