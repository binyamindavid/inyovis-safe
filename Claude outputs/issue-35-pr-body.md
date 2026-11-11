Closes #35 (draft)

Lets a cleared PDF go from upload through review, confirmation and retrieval without invoice metadata (lease, court correspondence, Other) using the existing review form and #8's revision contract.

**Base:** `main` at 5074cde, which already contains #8 (PR #33). #9 (CSV export) has no branch or code on `main` yet, so this PR only provides the shared row definition (`IndexColumns` / `IndexRow`); it does not add an export endpoint.

## What changed
- **Processing:** no code change needed. A cleared PDF with no invoice XML already completes with an empty extraction and reaches Needs review. Added tests with real synthetic PDFs, plus blocked, genuine-failure and scanner-outage cases.
- **Validation (Go):** each type owns one field group. Lease / court_correspondence / other need only `title` + type to confirm; `sender`, `issueDate` (document date), `reference`, `notes` are optional text. Any non-empty/non-null invoice value on a non-invoice submission is rejected, and invoice types reject document-group values. Invoice/bill/credit-note rules, exact decimals and null-vs-zero tax are unchanged.
- **Revisions:** same append-only revisions, pointers, version check, idempotency and audit. Non-invoice revisions marshal without invoice keys. Migration 11 adds a CHECK so a non-invoice revision cannot carry typed amounts.
- **API:** OpenAPI updated (`ReviewMetadata`, `DocumentStatus.confirmedType/confirmedTitle`); client regenerated with `yarn generate:client`.
- **List:** confirmed title and type come from the confirmed revision only and are hidden if the document is no longer cleared.
- **UI:** same Select, preview, Save draft and Confirm; fields and warnings follow the selected type; confirm dialog before a type switch discards unsaved values; en/fr/he strings.
- **CSV seam for #9:** `IndexColumns` / `IndexRow` with blank invoice columns, text-preserved references and spreadsheet-formula neutralisation.

## Not in this PR
No download endpoint, export endpoint or retention evaluation exist on `main`, so those items could not be implemented or exercised. Non-invoice records get no keep-through date and nothing in this PR adds one. Automatic classification/extraction, specialised forms, search and new retention rules are follow-ups.

## Verified
Go: archive, auth, money, processing, rendering and cmd tests pass (each package against a freshly migrated Postgres 16). Web: 28/28, contracts: 3/3, `tsc --noEmit` clean, generated client matches the spec. UI exercised in Chromium against a stubbed API in en/fr/he (RTL, mixed Hebrew/Latin, reference `000417/2026-ת`, type-switch warning, submitted bodies). Not exercised: full browser upload → review → confirm run with real storage, original download, CSV export, mobile layout.

Note: processing tests assume an empty job queue; run Go packages with `-p 1` and a freshly migrated DB per package (this predates the PR).
