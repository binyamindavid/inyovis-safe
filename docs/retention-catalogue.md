# Retention rule catalogue

The active database release is the only source of automatic retention decisions. A
document with no matching `complete` coverage and active validated rule remains
pending and cannot enter shredding review. A missing rule is not evidence that a
document is outside legal scope. `excluded` coverage needs affirmative source
evidence and a separately reviewed release.

## Initial coverage matrix (14 November 2026)

| Jurisdiction | Confirmed category | Personal | Business | Evidence / next validation |
| --- | --- | --- | --- | --- |
| France | Supplier invoice | Awaiting validation | Awaiting validation | Distinguish household purchase from business accounting obligations. |
| France | Utility bill, including electricity | Awaiting validation | Awaiting validation | [Service Public](https://www.service-public.gouv.fr/particuliers/vosdroits/F19134) lists five years for household electricity and gas bills. The row does not state the starting event; the current `bill` classification also does not distinguish electricity from water or telecom. |
| France | Lease | Awaiting validation | Awaiting validation | Verify term, starting event and exceptions. |
| France | Court correspondence | Awaiting validation | Awaiting validation | Verify case-specific obligations. |
| Ireland | Supplier invoice, utility bill, lease, court correspondence | Awaiting validation | Awaiting validation | Obtain and independently review official sources by scope. |
| Israel | Supplier invoice, utility bill, lease, court correspondence | Awaiting validation | Awaiting validation | Obtain and independently review official sources by scope. |
| France, Ireland, Israel | `other` (unclassified) | Outside initial product scope | Outside initial product scope | A confirmed, specific document category is required before rule matching. This is a product classification limit, not a legal exclusion. |

No live rule or `excluded` coverage is published by this matrix. “Supported” will
only appear after an approved rule and complete coverage have been published.

## Import and publication

Use the migration owner connection for these operations, never the archive runtime
role. Apply migrations through `000025` first. Build a UTF-8 JSON manifest with
`version`, `rules` and `coverage` arrays. Each rule must include `id`, integer
`version`, `jurisdiction` (`FR`, `IE`, `IL`), `entityScope` (`person` or `company`),
`documentScope` (a confirmed document type), `effectiveFrom`, exclusive
`effectiveUntil` or `null`, `selectionDate`, official HTTPS `source`, `trigger`,
integer `years`, `active`, `exceptions` (array, including `[]` when reviewed and
none found), and `validation`. The validation object has `status`,
`scope_verified`, `starting_event_verified`, `reviewer`, `reviewed_at`, and
`evidence`. A `complete` coverage declaration states that **all** applicable
obligations for its scope are represented; it is not a synonym for one known rule.

```
python3 scripts/retention-catalogue.py validate release.json
MIGRATION_DATABASE_URL='postgres://...' python3 scripts/retention-catalogue.py stage release.json
MIGRATION_DATABASE_URL='postgres://...' python3 scripts/retention-catalogue.py publish release-id
```

`stage` creates an immutable inactive release in one transaction. `publish`
checks every active rule's approval, scope and starting event, then atomically
switches the active release. PostgreSQL also validates coverage completeness and
contradictions. New releases must carry forward unchanged rule and coverage
identities explicitly; omitting one withdraws that evidence and queues affected
documents for reassessment. Check the active pointer, pending queue and a
representative decision after publication. Keep the manifest and reviewers'
source notes in version control for audit. Corrections require another release.

Do not publish a broad `bill` rule as a French electricity rule: the current
confirmed classification cannot distinguish electricity from other bills. First
add a confirmed finer category and verify the official starting event and any
exceptions. Only then can its scope be marked complete.

## Synthetic French household example

`TestSyntheticFrenchHouseholdElectricityBill` uses a **synthetic** Personal bill
dated 12 February 1997. With no coverage, the evaluator returns pending with
`coverage_missing` and no keep-through date. With synthetic complete coverage
and a five-year issue-date rule, it calculates 12 February 2002. This proves
the matching and date arithmetic without asserting that the official French
source uses the issue date. The synthetic rule is never part of a live release.
