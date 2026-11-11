# Issue #9 verification (2026-11-11)

The integration branch is `codex/issue-35-browser-flow` (draft PR #40). The disposable stack used PostgreSQL 17, SeaweedFS, the running Go worker and API, and the HTTPS Next.js app. All accounts and PDFs were synthetic.

## Browser path

- A signed-in synthetic contributor uploaded `apps/backend/internal/processing/testdata/basic.pdf` in the archive page. The UI reported its 1,562 bytes and SHA-256 `380ac00117ed5f42fcff52f71103048ed85386e7dc8d9a274146ebb06bd394d1`. The worker moved it through Processing to Needs review. The review page opened its preview, showed the extracted invoice suggestion, and confirmed it as an invoice with explicit `0.00` tax; the archive then showed Ready.
- The Download original action returned a PDF with the same SHA-256 and a byte-for-byte `cmp` match to `basic.pdf`. `pdfdetach` found `factur-x.xml` in both copies; the extracted XML SHA-256 was `eba167565f9b5aa585b3bc4c17baaae959c6893c18229d778ea497572fada4f5` for both.
- The same archive already contained the three #35 fixtures uploaded through the browser and worker in the prior verification pass. The contributor downloaded their originals from their review pages. Each matched its fixture byte-for-byte: lease `3d770ee4ccf4379e90b9433529c97283386b0b01e3535681d6562bc1516b8d51`, court letter `af3b80b19368cd7d9fe4bc920b45099b770734a26ff98f250d9f01c1fef29971`, other `ce0436828c7ac4cc421c968eb4ff574cd9aaec35341327eec510810be2dfa7f8`.
- The contributor initially had `can_bulk_export=false`, and the archive showed no export action. After granting it in the disposable database, the archive showed Export archive and downloaded a ZIP. Its seven original entries (the Factur-X PDF plus six prior #35 records) all matched `sha256.csv`. The Factur-X ZIP entry matched the upload bytes; its embedded XML matched the two individual copies. The three generic ZIP entries matched their fixtures. `index.csv` contained mixed French/Hebrew text and the exact reference `00042-AB` for the confirmed lease, with blank invoice columns. A distinct later draft on an Other record remained absent from the confirmed index.

## Automated cases

`TestExportDownloadPermissionsIntegrityAndLifecycle` uses a migrated disposable PostgreSQL database and fake immutable storage to cover viewer individual read, admin without grant, granted export, duplicate filenames, confirmed metadata despite a later draft, revoked grant and membership, pending and blocked clearance, expired delivery and cleanup, missing and mismatched originals, and cross-archive access. The web gateway tests check binary attachment headers and byte forwarding. The existing #35 tests cover generic metadata validation, revisions, invoice compatibility and review permissions.

Commands: `go test -count=1 ./...` from `apps/backend` with disposable test database URLs; `yarn workspace @invoyis/web test`; `yarn workspace @invoyis/contracts test`; `yarn build`; `yarn check:generated`. The first four passed during implementation. The generated-client check is run after committing generated files.

## Remaining live checks

The viewer, admin, grant revocation, membership revocation, clearance transitions, expiry, duplicate filenames and corrupt-object branches were exercised by integration tests, not all through distinct browser sessions. #35 still needs the invoice-suggestion type-switch browser pass and retention/disposal verification called out in that issue. Keep #35 open.
