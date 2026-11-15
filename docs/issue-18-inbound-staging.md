# Forwarded email intake: staging gate

## Configuration

SendGrid Inbound Parse owns the MX record for `inbound.invoyis.com`. Configure **one** Parse setting for that hostname with `send_raw: false` and the default parsed multipart payload. Attach a signature-enabled security policy. Keep `info@invoyis.com` and the apex-domain MX records on Google Workspace.

Set `SENDGRID_INBOUND_PUBLIC_KEY` to the Base64 DER `signature.public_key` returned by that policy, not the outbound API key. The webhook URL is `/api/inbound/sendgrid`; it forwards the exact raw multipart bytes to Go's `/v1/inbound/sendgrid`. The application caps the HTTP body at 64 MiB, caps total file parts at 50 MiB and ten parts, and uses the direct-upload per-PDF cap (25 MiB by default). Configure the external ingress body limit to **64 MiB** as well. Allow multipart overhead above the 50 MiB file budget. The app returns 413 before creating intake rows when this limit is exceeded.

New active-address deliveries are limited to 100 per rolling day and each archive to 500 per rolling day across rotations. Admitted deliveries count once; durable quota rejections do not extend the quota window. Known retries resume without another charge.

`INBOUND_FIRST_SEEN_HOURS` defaults to 72 and accepts 1 to 720 hours. A verified, already-admitted fingerprint can finish or replay after this window; an unseen signed delivery cannot. Outbound sender feedback is not implemented and remains off. Do not attach any automated feedback sender until the outer From address is aligned with authenticated sender evidence and exactly matches a verified member mailbox. Subjects, bodies, filenames, and full forwarding addresses must stay out of routine logs.

## Local synthetic rehearsal

Run migrations and provision the restricted archive runtime role, then run the inbound integration tests against a disposable `_test` database. The tests sign exact raw multipart bodies, exercise mismatched display recipients and mixed-MX envelope recipients, reject multiple inbound aliases, and cover failure before admission, partial publication, retries after rotation, and archive-scoped exact deduplication. The worker sees new documents through the existing `processing_jobs` and private `stored_objects` path. A blocked scan must leave the original quarantined; a cleared original must download with the attachment's SHA-256.

## Live SendGrid checks before pilot activation

1. Send synthetic mail directly to two generated addresses on `inbound.invoyis.com`. Capture the **SMTP envelope** in the default parsed multipart `envelope` field, including a message jointly addressed to an archive and `info@invoyis.com`, duplicate aliases, and two distinct inbound aliases. Check that `info@invoyis.com` still arrives in Google Workspace.
2. Capture multipart part keys, `attachment-info`, disposition, content type, filename, and byte hash for a PDF, an inline PDF, an inline image, and a ZIP. Confirm the bytes and fields that the provider actually sends.
3. Force a retry after a failure **before the first database write**. Record only redacted hashes of stable message fields, the signed timestamp, request-body hash, and observed provider delivery ID if present. Compare retry attempts with a separate deliberate forward of the same PDF. Confirm retries have a stable fingerprint and separate forwards have distinct stable data. Identical parsed deliveries can be indistinguishable without a stable per-delivery provider value.
4. Force a retry beyond the configured first-seen window. If SendGrid reuses the original signature timestamp, either set the window beyond the supported retry horizon plus clock skew or establish a separately authenticated durable ingress for the first attempt. Verify an old signed replay of a known delivery and an unseen late legitimate retry.
5. Verify two PDFs reach review, scanned-out files stay quarantined, and every cleared original download hashes to the source SHA-256. Check 401 invalid signatures, 413 transport cap, terminal 200 responses, and retryable 5xx outages.

Local verification completes the implementation PR. The live checks above gate pilot activation.
