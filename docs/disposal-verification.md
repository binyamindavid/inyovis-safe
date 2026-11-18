# Document disposal verification record

Status: **Trash foundation only. Permanent shredding is not implemented or enabled.**

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

## Required before a shredding PR

- Final transaction must lock and re-evaluate current retention decision, published rule coverage, confirmed revision, clearance, inclusive keep-through date, active holds, and actor permission.
- A durable per-object cleanup ledger must handle original and derived objects, shared references, object-store failure, retries, crash recovery, pending workers, export bundles and expiry, and backup expiry. Historic orphan keys from original consolidation need an explicit reconciliation procedure.
- Run restricted-role PostgreSQL tests against a disposable test database and verify English, French, and Hebrew RTL on desktop, mobile, and keyboard. Capture screenshots from those runs.
