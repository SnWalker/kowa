# kowa.runtime-web.v1

Owner: `backend:S04`. Producer: the runtime-operations service over the real Server composition root and PostgreSQL stores; the S01 session and membership authorities gate every request. Consumers: the future Web runtime view and the fixed-workflow run admission (S05/S06) for frozen execution configuration. Machine contract: [schema.json](schema.json); operations and layers: [delivery.json](delivery.json).

| Operation | Method/path | Required proof | Success |
| :--- | :--- | :--- | :--- |
| runtime.list | GET /api/v1/workspaces/{workspaceID}/runtimes | session cookie + current workspace membership | 200 `runtimeList` |
| runtime.view | GET /api/v1/workspaces/{workspaceID}/runtimes/{runtimeID} | session + membership + runtime authorized in the workspace execution configuration | 200 `runtimeView`; FORBIDDEN when the runtime is not authorized there |
| executionConfig.read | GET /api/v1/workspaces/{workspaceID}/execution-config | session + membership | 200 `executionConfig` (zero version with empty lists before the first write) |
| executionConfig.update | PUT /api/v1/workspaces/{workspaceID}/execution-config | exact trusted Origin + session + admin member + X-CSRF-Token | 200 `executionConfig` with the next version |

All responses set `Cache-Control: no-store`, `Referrer-Policy: no-referrer` and `X-Content-Type-Options: nosniff`. Responses and errors never carry credentials, environment values, tokens or task content; the schema rejects unknown fields, so an accidental secret field fails closed instead of being projected.

## Layers and freshness

The four capability-ladder layers are separate machine facts (`executable`, `versionCompatible`, `authenticated`, `canExecute`) plus `cliVersion`; they are never merged into a single "healthy" answer. A projection's `freshness` is one of:

- `fresh`: the latest observation belongs to the runtime's current epoch and its server watermark (`receivedAt`) is inside the observation TTL (15 minutes);
- `stale`: the watermark expired (`staleReason=expired`) or the newest observation belongs to a superseded epoch (`staleReason=superseded-epoch`); the observation stays visible for audit;
- `unknown`: the runtime has never reported an observation.

`stale` and `unknown` are never presented as usable. `observedAt` is the Runner clock and is advisory; only the server watermark decides staleness.

## Authorization and ownership

Runtime self-reports never grant workspace visibility. A runtime appears in a workspace only because an admin enabled it in that workspace's execution configuration; membership and the configuration are re-read on every request, so revoking either takes effect immediately. Machine GitHub identity (the runtime's registered `gitHubUserId`) and the Web actor are separate facts. Queries never trigger execution.

## Execution configuration

The execution configuration freezes, per workspace and exact version: the capabilities (`id@version`) that may run there and the runtimes authorized there. Writes are admin-only, require `requestKey` + `expectedVersion`, are idempotent for an identical `(caller, requestKey, content)` triple, conflict on a reused request key with different content, and conflict on version drift. The version increases monotonically; run admission consumes one concrete version and never "latest".

## Observation reporting

Authenticated Runtimes report observations over the S04 transport (`POST /api/v1/runtime/observations`), which enforces the current epoch. Reports are idempotent per `(runtimeId, runtimeEpoch, observedAt)`; the identical re-delivery keeps the original watermark, and the same identity with different content conflicts. A failed report degrades projection freshness only and never fails a task.

## Consumer status

The Web runtime view and the run-admission consumer are **NOT_STARTED**. This directory publishes the frozen v1 contract, its schema digest and the operation/layer registry so those consumers can depend on them explicitly; no compatibility facade or pre-release variant exists.
