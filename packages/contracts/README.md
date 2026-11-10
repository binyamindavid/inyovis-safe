# Invoyis contracts

`openapi.yaml` is the source of truth. Run `yarn generate:client` at the repository root after changing it, then commit the generated `src/generated` directory. `yarn check:generated` regenerates and fails if committed output drifts.

The generated Fetch client uses relative `/api/v1/...` URLs. Browsers call Next.js on the same origin; `API_BASE_URL` is read only by the Next route handler and defaults to `http://127.0.0.1:8080`. The `/api/v1/health` route maps to Go's existing `/healthz` endpoint.

For future session routes, call the server-only forwarding helper with `session: true` and an explicit `cookieNames` allowlist. It sets `Cache-Control: no-store` and preserves separate upstream `Set-Cookie` headers. Go remains responsible for session verification, validation, authorization, and domain decisions. Issue #3 will add email-code sign-in, session revocation, and mutation protection; issue #4 will add archive membership.

Problem responses use RFC 9457 `application/problem+json`, a stable `code`, and a `requestId`. `problemMessage()` maps codes to English, French, or Hebrew UI text. Amounts are exact two-place decimal strings with currency `ILS` or `EUR`; unspecified tax is `null`. Calendar dates use `YYYY-MM-DD`, UTC timestamps use RFC 3339 with `Z`, and list pages use opaque cursors with bounded limits. Each future list operation must state its stable sort and unique tie-breaker.
