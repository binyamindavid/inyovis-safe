# Document disposal verification record

Status: **Issue #76, draft PR #77. Do not merge or deploy until the remaining release evidence below is complete.** Permanent shredding has no feature flag in the branch. Eligibility, permission, retention, holds and the filename confirmation govern the action. The 30-day Trash deadline has no shred job or authorisation role.

## Policy and lifecycle

An archive admin types the current original filename in a separate **Shred permanently** dialog. A cleared, confirmed Personal document needs the admin confirmation; its reminder date never authorises deletion. A Business document needs a fresh resolved retention decision for the current confirmed revision, validated rule coverage, and a date after the inclusive keep-through day. The authorisation transaction locks the membership, archive, document, rule and object inputs, then rechecks holds, jobs, exports, revision and decision evidence. Missing or ambiguous evidence blocks authorisation. Trashed documents must first be restored.

Authorisation records the actor, time, archive and document IDs, confirmed revision, decision ID, keep-through day and input fingerprint. This is the minimal audit record. It stores no filename, extracted content, object key or original bytes. A per-key ledger tracks original, final, prepared and staging copies from receipt-linked upload sessions, including duplicate-session final keys and migration 17 consolidation orphans attributable through those sessions. The ledger retains only key fingerprints and verified outcome after cleanup. Unattributed historical keys require separate ownership investigation; the worker must not guess ownership or delete them.

A shared original remains until no other live document references it. Upload grant and finalisation windows must expire before cleanup. The worker claims one ledger row with a lease, verifies reference safety, removes every version of the exact object key, checks versioned object-store absence, and acknowledges the result. Storage errors are recorded as failed and retried. Once all owned keys are verified or shared, the finaliser purges document, revision, extraction, preview, review, retention, hold, reminder, receipt, submission, upload-session and unreferenced stored-object metadata. Duplicate requests return the existing shred state. A crash between object deletion and acknowledgement can retry exact-key deletion. The UI reports pending, failed or active-storage verified; it does not imply backups vanish immediately.

Issued exports and in-progress export builds block authorisation. Existing exports must pass expiry and be removed by the export cleanup worker first. Processing and preview claims block authorisation. Backup copies follow the existing production backup lifecycle and are outside the immediate active-storage verification.

## Verification on 2026-11-19

- Applied migrations 1–32 on disposable PostgreSQL 17. Runtime and upload reconciler used restricted roles without ownership or BYPASSRLS.
- `go test ./internal/archive -count=1` passed with `ARCHIVE_TEST_DATABASE_URL`, `TEST_ARCHIVE_DATABASE_URL` and `UPLOAD_RECONCILER_TEST_DATABASE_URL` set to the disposable database. It covers role and archive isolation, Personal and Business blockers, holds, exports, Trash and restore, shared originals, duplicate requests, storage failure, retry and crash recovery. The first attempt used the wrong reconciler environment variable and failed from a missing schema; the corrected run passed.
- `SHRED_OBJECT_STORAGE_TEST=1 go test ./internal/archive -run '^TestReal(StorageDocumentLifecycle|ObjectStorageShredVersionsAndExactKey)$' -count=1 -v` passed against local SeaweedFS. The full document flow used the restricted PostgreSQL role and checked the real bucket for remaining keys or versions. The exact-key test preserved a neighbour key.
- Previous mocked browser checks and screenshots are in `docs/screenshots/shredding-draft/`. They cover desktop and mobile English, French and Hebrew RTL with keyboard confirmation, but are **not** a live API browser pass.

## Remaining release evidence

- Verify the production backup expiry window from the existing approved policy and rehearse restoration with the corresponding private originals. No policy source or production restore evidence was available in this workspace; no expiry window is asserted here.
- Run a live browser flow against the actual API on desktop and mobile in English, French and Hebrew RTL, including keyboard use, and attach screenshots.
- Reconcile any historical orphan keys that have no receipt-linked document ownership. Do not mark a document verified if a recoverable application copy attributable to it remains.
- Verify production object-store versioning, replication and lifecycle configuration and export cleanup with the deployment's actual storage. Local SeaweedFS results are not proof of production behaviour.

PR #77 stays draft and issue #76 stays open until these checks pass. The branch is not a release approval.
