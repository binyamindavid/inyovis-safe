# Issue 35 verification — 2026-11-11

Tested base: `21934a8ad3188a710098cbd8bee024dd1f28351b` (`main`). All data below is synthetic. The browser used a signed-in `issue35-contributor@example.test` account and a disposable PostgreSQL database, SeaweedFS, Mailpit, API, and document worker. The first pass used that account as archive owner (Admin); the second pass used it as Contributor after adding a separate synthetic admin.

## Browser and worker flow

The archive page uploaded three generated PDF files through its normal direct-to-storage control. Each returned a receipt, the worker completed with `clearance=cleared`, the archive showed **Needs review**, and the review page displayed page 1 of the original preview. Each was confirmed and the reloaded archive showed **Ready** with the confirmed title and type.

| Fixture | Document ID | Confirmed type and title | Original SHA-256 |
| --- | --- | --- | --- |
| lease.pdf | `42b9be85-119c-4720-a14f-57afe4e736d8` | Lease · Bail synthétique — Paris | `3d770ee4ccf4379e90b9433529c97283386b0b01e3535681d6562bc1516b8d51` |
| court.pdf | `03ba37f8-abc7-48bd-a3f3-51dce0beac0d` | Court correspondence · מכתב בית משפט / Court letter | `af3b80b19368cd7d9fe4bc920b45099b770734a26ff98f250d9f01c1fef29971` |
| other.pdf | `8c7884d4-5340-4fba-8269-1d92cc985abe` | Other · Synthetic handover record | `ce0436828c7ac4cc421c968eb4ff574cd9aaec35341327eec510810be2dfa7f8` |

The court letter and generic PDF were confirmed with only title and type. The lease draft also saved sender or parties `Élodie Exemple / דוד Test`, date `2026-11-11`, reference `00042-AB`, and notes `1 rue Exemple, תל אביב`. Reload retained each value before confirmation. The generic type displayed no invoice warning or invoice amount input. Database inspection found null typed total and stated tax for all three confirmed revisions; stored SHA-256 values matched the generated fixture hashes. After saving `Draft only, not confirmed` on the Ready Other record, the archive still displayed its confirmed title.

The Contributor rerun uploaded the same three PDFs through the archive UI, observed separate receipts and Needs review rows, opened all three original previews, and confirmed each with title and type only. A reloaded archive showed `Contributor lease` (`26ec850c-b87b-4be7-ae17-44eeffcdf280`), `Contributor court letter` (`e5efb825-f795-4eec-baa4-dcb1255921a3`), and `Contributor other document` (`76bbbeae-c582-45c1-9a14-40508963d3a3`) as Ready with their respective types.

On the Ready Other record, switching to Invoice, editing supplier and invoice ID, and attempting to switch back opened the unsaved-value dialog. Cancel kept both edits. Accept cleared the invoice fields and restored the generic field group. The existing revision and extraction preservation tests cover a seeded invoice suggestion, but this browser case did not start from an invoice extraction suggestion.

## Code and checks

The first browser upload failed before completion: SeaweedFS bound to loopback but advertised `10.0.0.1` to its volume client. Adding `-ip=127.0.0.1` to the local mini invocation made the subsequent browser uploads pass. The test now checks both the bind and advertised addresses.

- `go test -count=1 ./...` from `apps/backend`: pass with separate disposable auth and archive test databases and the restricted archive and upload-reconciler roles.
- `yarn workspace @invoyis/web test`: 31 pass. The first sandboxed attempt could not create loopback test sockets; the unrestricted rerun passed.
- `yarn workspace @invoyis/contracts test`: 3 pass.
- `yarn check:generated`: pass; OpenAPI was unchanged.
- `node --test scripts/dev-local-storage.test.mjs`: 2 pass.
- `yarn build`: pass.

The Go suite covers generic validation (including explicit zero and populated tax lines), invoice and credit-note validation, revision history and extraction preservation, viewer write denial, cross-archive isolation, and pending or blocked clearance for review. The browser confirms the draft reload and later-draft archive pointer. The original download and bulk export endpoints are not yet present on `main`; issue #9 remains open. Thus individual download permission, blocked-original access through that endpoint, byte-for-byte downloaded and exported originals, export manifest, UTF-8 CSV output, and confirmed-versus-draft export behavior remain unverified. `IndexRow` unit coverage checks shared columns and blank invoice columns, but it does not substitute for an export.

There is no retention evaluation, keep-through column, or disposal path in the current schema or app. These three types therefore received no invoice retention calculation, but a future retention implementation needs a separate end-to-end check before the final acceptance box can be marked.
