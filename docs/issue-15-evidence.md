# Issue 15 verification

Base: `cb9082ade106272d9fbbf919881c5b4dbc4519d8` (`main` on 2026-11-14). Work is on `codex/issue-15-retention-review`.

## Checks

- Full `go test ./internal/archive ./internal/retention` — passed against the disposable restricted-role archive test database with `UPLOAD_RECONCILER_TEST_DATABASE_URL` set to that database's restricted reconciler role. This includes the #14 lower-bound and reassessment cases, export grant checks, viewer denial, holds and Personal reminders.
- `TestExportRetentionUsesConfirmedRevisionSnapshot` — passed. A concurrent correction and reassessment between metadata and retention reads leaves the export paired with the old confirmed revision and its decision.
- `TestEligibleRetentionHTTPResponse` — passed. The real Go HTTP response serializes `"reviewBlocker":""` for an eligible document. Its captured response was used in the browser check, which showed the eligible action.
- `TestHoldReleaseHTTPRequiresReason` — passed: missing reason is rejected, and the accepted reason appears in hold history.
- Synthetic FR, IE and IL evaluator tests, independent FR/IE publication queue test, and invalid live-rule activation test — passed.
- Migrations 23 and 24 up and down — passed inside one rolled-back PostgreSQL transaction on a clean test schema. Populated rollback rehearsals rejected migration 23 with carried-forward rule identities and migration 24 with a released hold reason before schema changes. The rule identities, hold row and release reason remained present afterward. Populated releases require a forward repair rather than a destructive rollback.
- `yarn workspace @invoyis/web build`, `yarn workspace @invoyis/web test`, `yarn workspace @invoyis/contracts test`, `yarn check:generated`, and gateway route tests — passed.
- `BASE_URL=http://127.0.0.1:3100 RETENTION_RESPONSE_OUTPUT=/private/tmp/issue15-eligible-response.json node scripts/verify-retention-browser.mjs` — passed on the local production build. English, French and Hebrew RTL screens, keyboard focus, mixed-direction references, a late hold, required hold-release reason, Personal date-only reminder, and the actual captured Go API payload were checked.

## Screenshots

| Language | Retention | Shredding review |
| --- | --- | --- |
| English | [retention-en.png](screenshots/retention-review/retention-en.png) | [review-en.png](screenshots/retention-review/review-en.png) |
| French | [retention-fr.png](screenshots/retention-review/retention-fr.png) | [review-fr.png](screenshots/retention-review/review-fr.png) |
| Hebrew RTL | [retention-he.png](screenshots/retention-review/retention-he.png) | [review-he.png](screenshots/retention-review/review-he.png) |

## Remaining verification

The browser screenshots use synthetic API responses; the eligible-action regression additionally uses bytes captured from the real Go HTTP handler. A signed-in end-to-end browser pass with real storage, processing and the #35 PDF fixtures remains outstanding.
