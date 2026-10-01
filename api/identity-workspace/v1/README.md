# kowa.identity-workspace.v1

Owner: `backend:S01` / Identity and Workspace. Producers are the GitHub App OAuth adapter and Workspace administration use cases. Consumers are the Web client, future Workflow admission, audit, and repository preparation.

## HTTP surface

| Method and path | Authentication | Input | Success |
| :--- | :--- | :--- | :--- |
| `POST /api/v1/auth/github/login` | none | JSON `{returnTo}` | authorization URL; hardened one-time state cookie |
| `GET /api/v1/auth/github/callback` | state cookie | query `state`, `code` | `LoginCallbackResponse`; hardened opaque session cookie |
| `POST /api/v1/auth/logout` | session + CSRF | none | `204`; session revoked |
| `POST /api/v1/workspaces` | session + CSRF + `Idempotency-Key` | `BootstrapWorkspaceCommand` | `WorkspaceConfig` |
| `GET /api/v1/workspaces/{workspaceId}` | session | none | authorized `WorkspaceConfig` |
| `PUT /api/v1/workspaces/{workspaceId}/repositories` | session + CSRF + `Idempotency-Key` | `ConfigureRepositoriesCommand` | new `WorkspaceConfig` version |
| `PUT /api/v1/workspaces/{workspaceId}/members/{githubUserId}` | admin session + CSRF + `Idempotency-Key` | `UpsertMemberCommand` | `204`; role granted or changed |
| `DELETE /api/v1/workspaces/{workspaceId}/members/{githubUserId}` | session + CSRF + `Idempotency-Key` | `RevokeMemberCommand` | `204` |

The actor always comes from the server-side session. A request body cannot nominate an actor. OAuth state and PKCE are single-use and expire after ten minutes. The browser receives no GitHub user token; the opaque session cookie is `__Host-`, `Secure`, `HttpOnly`, `Path=/`, and `SameSite=Lax`. Mutating requests require both an exact trusted HTTPS `Origin` and `X-CSRF-Token`.

Every mutable Workspace command carries `expectedVersion` where an object already exists and an `Idempotency-Key` header. The lookup identity is actor + command type + target + request key. Same key/same normalized request returns the saved response; same key/different request returns `VERSION_CONFLICT`.

`project` is the only `read_write` binding. `knowledge` is always `read`. Stable decimal GitHub repository and installation IDs are authoritative; display names are not. A second binding for either role is `POLICY_BLOCKED`, and a repository not visible through the injected GitHub installation adapter is `RESOURCE_UNAVAILABLE`.

## Consumer rules

- Do not infer Workspace authority from GitHub login, GitHub App installation, repository visibility, a known object ID, or hidden UI controls.
- Do not send or persist GitHub user tokens. Keep the returned CSRF token in memory and send it only on same-origin mutating requests.
- Treat `401`, `403`, `409`, `422`, and `503` through the response `error` category; HTTP status alone does not authorize retry.
- On `VERSION_CONFLICT`, refetch the Workspace and require the user to confirm against its new version. Do not silently replay a changed request with the same idempotency key.
- Never turn the knowledge binding into a writable repository or append a second project repository. A multi-project write requirement is outside MVP and maps to acceptance case A02.
- Web identity, GitHub App installation, and future Runner machine GitHub identity remain separate facts. This contract never claims TaskSpec enforces GitHub-side hard isolation.

## Failure and acceptance mapping

| Contract behavior | Error/category | Acceptance |
| :--- | :--- | :--- |
| unauthenticated or invalid/revoked session | `UNAUTHORIZED` | A28, C06 |
| non-member, non-admin, or cross-Workspace object | `FORBIDDEN` | A28, C04 |
| stale object version or changed idempotent request | `VERSION_CONFLICT` | C03 |
| installation cannot see repository | `RESOURCE_UNAVAILABLE` | A03, C05 |
| second writable project or knowledge write | `POLICY_BLOCKED` | A02 |
| audit insert fails | `INTERNAL_ERROR`; business mutation rolls back | A28 |

Positive and negative examples are under `examples/`. Real GitHub App installation and authorization are deliberately not part of S01; S08 supplies those external facts without changing this contract.

## S01 producer correction and assembled delivery (2026-10-01)

The actual callback now serializes the frozen lower-camel identity fields; the schema is unchanged. Omitted/empty returnTo is normalized to `/workspaces`, matching the callback schema's nonempty path. The new UI redirect and refresh/bootstrap operation are separately frozen in [web-session v1](../../web-session/v1/README.md); only that new auth surface uses `/api/v2`. Workspace semantics and routes remain v1. `delivery.json` records the real Server-mounted operations and the unchanged schema digest. Default installation visibility fails closed until a verified adapter is supplied; real App/TLS remain S08. Strict producer-schema validation and PostgreSQL/Server isolation are required evidence; examples alone do not establish wire conformance.
