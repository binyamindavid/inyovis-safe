# Issue 15 verification

Base: `cb9082ade106272d9fbbf919881c5b4dbc4519d8` (`origin/main` on 2026-11-14). The implementation is on `codex/issue-15-retention-review`.

## Checks

- `GOCACHE=/private/tmp/invoyis-go-cache go test ./internal/retention` — passed, including independent synthetic FR, IE and IL coverage and unvalidated-scope blocking.
- `GOCACHE=/private/tmp/invoyis-go-cache go test ./internal/archive -run 'Test(DocumentHolds|PersonalReminder|Export|Retention|ShreddingReview)' and `go test ./internal/archive -run 'TestRetention(PublicationTargetsChangedScope|ActivationRequiresVersionedRule)' -count=1` -count=1` with the disposable restricted-role archive test database — passed. This includes lower bound then filing date, preserved pending snapshot, repeat evaluation, inclusive date boundary, late hold, stale input, original availability, viewer API denial, and export grant tests.
- New migration up and down SQL — passed inside a rolled-back PostgreSQL test transaction. Migration 23 was applied to the disposable archive test database before the final trigger changes; the final trigger, coverage guard and rule identity schema were then applied there directly and the targeted publication and invalid activation tests passed.
- `yarn workspace @invoyis/web build`, `yarn workspace @invoyis/web exec tsc --noEmit`, `yarn workspace @invoyis/contracts test`, and `node --test apps/web/lib/archive-gateway.test.mjs` — passed.
- `BASE_URL=http://127.0.0.1:3100 node scripts/verify-retention-browser.mjs` against a local production build with a mocked archive API — passed in English, French and Hebrew RTL. Checked keyboard focus, date and reference rendering, eligible action, a hold added after details loaded, the review notice, and Personal date-only reminder with no shredding action.
- Full `go test ./internal/archive ./internal/retention` with the existing `.env` — failed in unrelated upload reconciler tests because `UPLOAD_RECONCILER_TEST_DATABASE_URL` points to a database without the `invoyis` schema. The export fixture expectations introduced by this change were fixed and focused export tests pass.

## Screenshots

| Language | Retention | Shredding review |
| --- | --- | --- |
| English | [retention-en.png](screenshots/retention-review/retention-en.png) | [review-en.png](screenshots/retention-review/review-en.png) |
| French | [retention-fr.png](screenshots/retention-review/retention-fr.png) | [review-fr.png](screenshots/retention-review/review-fr.png) |
| Hebrew RTL | [retention-he.png](screenshots/retention-review/retention-he.png) | [review-he.png](screenshots/retention-review/review-he.png) |

## Remaining verification

- Browser screenshots use synthetic API responses. An authenticated browser pass against real PostgreSQL, processing, storage and the #35 PDF fixtures remains to be done.
- Rehearse a clean migration 23 on a fresh disposable database, including rule carry-forward and the publication guard.
- The full Go suite needs a valid upload reconciler test database URL.
