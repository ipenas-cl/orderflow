# ADR 0005: OIDC/JWT authentication (Auth0) behind an auth port, with RBAC in the application layer

- Status: accepted
- Date: 2026-10-22

## Context
The backoffice is used by staff with different permissions: viewers read, support can refund and move shipments, and admins can adjust stock and read the audit log. Production uses Auth0. Tests and the local stack must work offline, with no tenant.

## Decision
- **Port:** `auth.Verifier` (`internal/platform/auth`) turns a bearer token into a `Principal{sub, email, roles}`.
- **Adapter:** `JWTVerifier` validates RS256 tokens against the issuer's JWKS, checking `iss`, `aud` and `exp` (with 30 s leeway). It caches keys and refreshes them on an unknown `kid` (key rotation) at most every 30 s. It works with any OIDC issuer, and Auth0 is configured through `OIDC_ISSUER`/`OIDC_AUDIENCE`.
- **Roles:** they come from a namespaced custom claim (`https://orderflow.dev/roles`), set by an Auth0 Action from the user's assigned roles.
- **Dev issuer:** `cmd/devissuer` serves a JWKS and mints tokens for three demo identities. Because the **same verifier code** checks its tokens, the dev path exercises real signature validation and is not a bypass. It is never deployed (no Helm template).
- **RBAC** lives in the application layer (`admin/app.Permissions`), not only in HTTP middleware, so every driving adapter gets the same rules. The SPA reads `/api/me` permissions only to *hide* buttons, and the server enforces them.
- **Audit:** every mutating action (refund, stock adjustment, shipment step) is appended to an append-only `audit_log` table with actor, role, target and outcome, failures included.
- **SPA tokens:** in Auth0 mode the SPA uses Authorization Code + PKCE (`@auth0/auth0-spa-js`) with an in-memory cache. Internal services sit behind the BFF and are not exposed. In a cluster, east-west traffic would be protected by mTLS from the mesh.

## Consequences
- Switching IdP (Okta, Cognito, Keycloak) is configuration only.
- Tests cover a valid token, expiry, a wrong audience, a forged signature and role gating (`auth_test.go`, `admin/adapters/http/http_test.go`).
