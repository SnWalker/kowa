import type { Page, Route } from '@playwright/test';
import {
  executionConfigFixture,
  gitRemoteStatusFixture,
  runtimeListFixture,
  sessionBootstrapFixture,
  workspaceConfigFixture,
} from '../../fixtures/contracts';
import { actor, isControlPlane } from './mode';

export type RecordedCall = { method: string; path: string; headers: Record<string, string>; body: unknown };

type Handler = (call: RecordedCall, route: Route) => Promise<{ status: number; body?: unknown; headers?: Record<string, string> }> | { status: number; body?: unknown; headers?: Record<string, string> };

export type FixtureServer = {
  calls: RecordedCall[];
  /** Replace a route answer while the page runs (for expiry/conflict flows). */
  set(method: string, pathPattern: string, handler: Handler): void;
  /** Serve no authenticated session: every recovery request answers 401. */
  signOut(): void;
};

const json = (status: number, body?: unknown, headers?: Record<string, string>) => ({
  status,
  headers: {
    'content-type': 'application/json',
    'cache-control': 'no-store',
    'referrer-policy': 'no-referrer',
    'x-content-type-options': 'nosniff',
    ...headers,
  },
  body: body === undefined ? undefined : JSON.stringify(body),
});

/**
 * Contract-bound API double at the HTTP boundary: every answer is a frozen
 * contract payload (validated against the owner schemas in the unit suite) or
 * the frozen error envelope. It is a stub for the network, never a substitute
 * for the real control plane, which `scripts/web-e2e-control-plane.sh` runs.
 */
export async function installFixtures(page: Page, shared?: { authenticated: boolean }): Promise<FixtureServer> {
  const calls: RecordedCall[] = [];
  // The double starts like a browser without a session cookie; the login
  // journey is what authenticates it, exactly as in the control plane.
  const state = shared ?? { authenticated: false };
  let authenticated = state.authenticated;
  let workspaceVersion = 2;
  let workspaceName = 'Kowa';
  const handlers: { method: string; pattern: RegExp; handler: Handler }[] = [];

  function set(method: string, pathPattern: string, handler: Handler) {
    handlers.unshift({ method, pattern: new RegExp(pathPattern), handler });
  }

  await page.route('**/api/**', async (route) => {
    const request = route.request();
    const method = request.method();
    const url = new URL(request.url());
    const call: RecordedCall = {
      method,
      path: url.pathname,
      headers: request.headers(),
      body: request.postDataJSON?.() ?? undefined,
    };
    calls.push(call);

    for (const entry of handlers) {
      if (entry.method === method && entry.pattern.test(url.pathname)) {
        const answer = await entry.handler(call, route);
        await route.fulfill(json(answer.status, answer.body, answer.headers));
        return;
      }
    }

    // Recovery snapshot.
    authenticated = state.authenticated;
    if (method === 'POST' && url.pathname === '/api/v2/auth/session') {
      if (!authenticated) {
        await route.fulfill(json(401, { error: 'UNAUTHORIZED' }));
        return;
      }
      const requestId = (call.body as { requestId?: string } | undefined)?.requestId;
      await route.fulfill(json(200, sessionBootstrapFixture({ requestId: requestId ?? 'fixture', identity: actor })));
      return;
    }
    if (method === 'POST' && url.pathname === '/api/v2/auth/github/login') {
      // The stub authorize endpoint returns to the callback, which hands the
      // browser back to the app exactly like the owner's 303 UI handoff.
      // In the double the provider step returns straight to the app's frozen
      // callback, so the app's own 303 UI handoff is still exercised while the
      // external provider hop stays outside the browser journey.
      const returnTo = (call.body as { returnTo?: string } | undefined)?.returnTo ?? '/workspaces';
      // The real owner answers with an absolute authorization URL; the double
      // stays on the test origin but keeps that shape (a relative URL is not a
      // navigation target and is refused by the consumer).
      const origin = new URL(request.url()).origin;
      await route.fulfill(json(200, {
        authorizationUrl: `${origin}/api/v2/auth/github/callback?code=fixture-code&state=fixture-state&returnTo=${encodeURIComponent(returnTo)}`,
      }));
      return;
    }
    if (method === 'POST' && url.pathname === '/api/v2/auth/logout') {
      authenticated = false;
      state.authenticated = false;
      await route.fulfill(json(204));
      return;
    }
    if (method === 'POST' && url.pathname === '/api/v1/workspaces') {
      workspaceName = (call.body as { name: string }).name;
      workspaceVersion = 1;
      await route.fulfill(json(201, workspaceConfigFixture({ workspaceId: 'workspace-created', name: workspaceName, version: 1, members: [{ githubUserId: actor.githubUserId, role: 'admin' }], project: undefined, knowledge: undefined })));
      return;
    }
    if (method === 'GET' && /^\/api\/v1\/workspaces\/[^/]+$/.test(url.pathname)) {
      await route.fulfill(json(200, workspaceConfigFixture({ name: workspaceName, version: workspaceVersion })));
      return;
    }
    if (method === 'PUT' && url.pathname.endsWith('/repositories')) {
      workspaceVersion += 1;
      await route.fulfill(json(200, workspaceConfigFixture({ version: workspaceVersion })));
      return;
    }
    if (method === 'GET' && url.pathname.endsWith('/runtimes')) {
      await route.fulfill(json(200, runtimeListFixture()));
      return;
    }
    if (method === 'GET' && /\/runtimes\/[^/]+$/.test(url.pathname)) {
      await route.fulfill(json(200, { schemaVersion: 'kowa.runtime-web.v1', kind: 'runtimeView', runtime: runtimeListFixture().runtimes[0] }));
      return;
    }
    if (method === 'GET' && url.pathname.endsWith('/execution-config')) {
      await route.fulfill(json(200, executionConfigFixture()));
      return;
    }
    if (method === 'GET' && url.pathname.endsWith('/git-remote-status')) {
      await route.fulfill(json(200, gitRemoteStatusFixture()));
      return;
    }
    throw new Error(`unmapped fixture request ${method} ${url.pathname}`);
  });

  // The app's callback answers with the same 303 UI handoff the owner freezes.
  await page.route(/\/api\/v2\/auth\/github\/callback/, async (route) => {
    const url = new URL(route.request().url());
    authenticated = true;
    state.authenticated = true;
    // The real callback answers 303 Location: origin + returnTo. Playwright
    // cannot fulfil a 3xx in every engine, so the double hands the browser back
    // with the equivalent document-level redirect and the exact returnTo the
    // server would have chosen.
    const returnTo = url.searchParams.get('returnTo') ?? '/workspaces';
    await route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: `<!doctype html><title>session established</title><meta http-equiv="refresh" content="0;url=${returnTo}">`,
    });
  });

  return {
    calls,
    set,
    signOut() {
      authenticated = false;
      state.authenticated = false;
    },
  };
}

const installed = new WeakMap<Page, Promise<FixtureServer>>();

/**
 * Only fixture mode installs the double; the control plane is answered for
 * real. Installing twice on the same page is a no-op so a caller that adds
 * route overrides keeps them.
 */
export async function installApi(page: Page, shared?: { authenticated: boolean }): Promise<FixtureServer | undefined> {
  if (isControlPlane) return undefined;
  const existing = installed.get(page);
  if (existing) return existing;
  const pending = installFixtures(page, shared);
  installed.set(page, pending);
  return pending;
}
