import { render } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { App } from '../../src/app/App';
import { SessionProvider } from '../../src/auth/SessionProvider';
import { createAppSession } from '../../src/api/session';
import type { SessionValue } from '../../src/auth/session-context';
import { createApiClient } from '../../src/api/client';
import { sessionBootstrapFixture } from '../fixtures/contracts';

export type FakeResponse = { status: number; body?: unknown; headers?: Record<string, string> };

/**
 * Records requests and answers them from a route table keyed by
 * `METHOD path`. Unmapped requests fail the test loudly instead of silently
 * returning an invented shape.
 */
export function createFakeFetch(routes: Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>) {
  const calls: { method: string; url: string; headers: Headers; body: unknown }[] = [];
  const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    const path = new URL(url, 'https://kowa.test').pathname;
    const key = `${method} ${path}`;
    const headers = new Headers(init?.headers ?? {});
    const body = init?.body === undefined || init?.body === null ? undefined : JSON.parse(String(init.body));
    calls.push({ method, url: path, headers, body });
    const route = routes[key];
    if (!route) throw new Error(`unexpected request ${key}`);
    const response = typeof route === 'function' ? await route(new Request(`https://kowa.test${path}`, init)) : route;
    return new Response(response.body === undefined ? null : JSON.stringify(response.body), {
      status: response.status,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as unknown as typeof fetch;
  return { fetchImpl, calls };
}

/**
 * A recovery answer that echoes the caller's `requestId`, which the contract
 * requires for the snapshot to be accepted at all.
 */
export function sessionRoute(body?: unknown) {
  return async (request: Request): Promise<FakeResponse> => {
    const payload = (await request.json()) as { requestId?: string };
    const fixture = (body ?? sessionBootstrapFixture()) as { requestId?: string };
    return { status: 200, body: { ...fixture, requestId: payload.requestId ?? fixture.requestId } };
  };
}

/** Adds the unauthenticated recovery answer unless the test overrides it. */
export function withRoutes(routes: Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>) {
  return {
    'POST /api/v2/auth/session': { status: 401, body: { error: 'UNAUTHORIZED' } } as FakeResponse,
    ...routes,
  };
}

export function renderApp(options: {
  fetch: typeof fetch;
  initialEntries?: string[];
  navigateAway?: (url: string) => void;
  /** Captures the focus/visibility listener so a test can trigger a resume. */
  onFocusSubscribed?: (trigger: () => void) => void;
  createChannel?: () => BroadcastChannel | undefined;
}) {
  const session = createAppSession({
    fetch: options.fetch,
    createChannel: options.createChannel ?? (() => undefined),
    subscribeFocus: (listener) => {
      options.onFocusSubscribed?.(listener);
      return () => undefined;
    },
  });
  const value: SessionValue = { ...session, navigateAway: options.navigateAway ?? (() => undefined) };
  const view = render(
    <SessionProvider value={value}>
      <MemoryRouter initialEntries={options.initialEntries ?? ['/workspaces']}>
        <App />
      </MemoryRouter>
    </SessionProvider>,
  );
  return { session, value, ...view };
}

export const unauthorized: FakeResponse = { status: 401, body: { error: 'UNAUTHORIZED' } };

export { createApiClient };

/**
 * Renders an arbitrary subtree with the two providers a page-level component
 * needs (session and router) but without mounting the whole app shell, so a
 * component that owns its own reads can be exercised on its own.
 */
export function renderWithProviders(
  ui: ReactNode,
  options: { fetch: typeof fetch; initialEntries?: string[] },
) {
  const session = createAppSession({
    fetch: options.fetch,
    createChannel: () => undefined,
    subscribeFocus: () => () => undefined,
  });
  const value: SessionValue = { ...session, navigateAway: () => undefined };
  return render(
    <SessionProvider value={value}>
      <MemoryRouter initialEntries={options.initialEntries ?? ['/']}>{ui}</MemoryRouter>
    </SessionProvider>,
  );
}
