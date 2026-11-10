# Leased document processing

Migration 8 extends the one-job-per-document queue created by upload intake. The worker claims due work with `FOR UPDATE SKIP LOCKED`, a fresh token, and a lease. It reaps expired leases on every poll, including when no pending work exists. Every heartbeat, parser start, retry, failure, and completion checks the unexpired token. An interrupted attempt can be reclaimed; a final expired claim or spent parser budget becomes a terminal failure. Storage reads, scanning, and parsing happen outside database transactions. The extraction run, job success, and document `needs_review` state commit together. Human metadata revisions remain outside this table.

The worker database login can execute only narrow `SECURITY DEFINER` functions and cannot read or write intake tables directly. Its storage credentials must allow only `GetObject` on private originals. The parser child receives neither database nor storage credentials. On macOS `sandbox-exec` denies network and home-directory access; on Linux `bwrap` mounts only the parser executable and system libraries and provides a separate network namespace. Build the Linux worker with `CGO_ENABLED=0` to keep the parser child self-contained. Poppler `pdfdetach` validates the PDF container and extracts one canonical `factur-x.xml` attachment. XML directives are rejected. Input, extracted XML, output, process CPU, file output, and elapsed time are bounded. Both platforms use a 384 MiB Go runtime soft memory limit and a parent-side 512 MiB RSS watchdog for the parser process tree. The watchdog can observe a short peak between checks; production mode refuses startup without a kernel-enforced worker cgroup limit at or below 1 GiB.

ClamAV `clamscan` scans the preserved bytes before parsing. A missing scanner or failing signature database causes a transient scanner failure, never a clean result. A positive scan blocks the document. The upload receipt and original object remain unchanged. Job metrics return only the oldest eligible age and terminal counts by stable code; no document identifiers appear in metric labels.

The scanner checks `CLAMAV_DATABASE` before every scan. Missing, empty, or stale signature files return `scanner_unavailable` even if a scanner executable would answer clean. The default age threshold is 48 hours; production mode requires an official daily CVD/CLD whose recorded build time is fresh. Production startup also requires a Linux cgroup memory limit at or below 1 GiB. The parser keeps its 384 MiB Go memory soft limit and 512 MiB RSS watchdog inside that kernel-enforced worker limit.

| Failure code | Decision |
| --- | --- |
| `storage_unavailable`, `scanner_unavailable` | Retry while the five-claim budget remains; clearance stays pending. |
| `parser_timeout`, `parser_resource_exhausted` | Retry while both the five-claim and two-parser-start budgets remain. |
| `lease_expired_exhausted`, `parser_attempts_exhausted` | Terminal after recovery finds a spent budget. |
| `original_integrity_mismatch`, `scan_rejected`, `invalid_pdf`, `unsafe_xml`, `content_limit_exceeded` | Terminal; clearance is blocked. |
| `no_supported_xml`, `ambiguous_invoice_xml`, `unsupported_profile` | Save an empty candidate result and typed warning; the document needs review. |

The status API maps queue and document states to `pending`, `processing`, `needs_review`, `file_blocked`, and `processing_failed`. It does not expose internal failure details, source content, or credentials. The archive page lists recent statuses and polls so the result survives reload. Preview, correction, download, and manual retry remain separate work.
