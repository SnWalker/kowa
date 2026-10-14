/**
 * E2E target selection.
 *
 * Fixture mode (CI, default) serves the production build and answers `/api/**`
 * with contract-bound payloads at the HTTP boundary. Control-plane mode
 * (`KOWA_E2E_CONTROL_PLANE_URL`) drives the same journeys against the real
 * `internal/server` composition root with isolated external adapters, started
 * by `scripts/web-e2e-control-plane.sh`.
 */
export const controlPlaneURL = process.env.KOWA_E2E_CONTROL_PLANE_URL ?? '';
export const controlPlaneStub = process.env.KOWA_E2E_CONTROL_PLANE_STUB ?? '';
export const isControlPlane = controlPlaneURL !== '';

/** Identities the harness and the fixture doubles are configured with. */
export const actor = { githubUserId: '101', login: 'member-a' };
export const otherActor = { githubUserId: '202', login: 'member-b' };
