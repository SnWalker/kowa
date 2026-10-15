import { describe, expect, it, vi } from 'vitest';
import { createAppSession } from '../../src/api/session';
import { sessionBootstrapFixture } from '../fixtures/contracts';
import { createFakeFetch, sessionRoute, type FakeResponse } from './render';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

type Channel = { postMessage: (message: unknown) => void; addEventListener: () => void; removeEventListener: () => void; close: () => void };

function setup(routes: Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>, channel?: Channel) {
  const { fetchImpl, calls } = createFakeFetch(routes);
  const posted: unknown[] = [];
  const fakeChannel: Channel = channel ?? {
    postMessage: (message) => posted.push(message),
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    close: () => undefined,
  };
  let focusListener: (() => void) | undefined;
  const session = createAppSession({
    fetch: fetchImpl,
    createChannel: () => fakeChannel as unknown as BroadcastChannel,
    subscribeFocus: (listener) => {
      focusListener = listener;
      return () => undefined;
    },
  });
  const stop = session.store.start();
  return { ...session, calls, posted, focus: () => focusListener?.(), stop };
}

describe('session client obligations', () => {
  it('recovers the authenticated snapshot on bootstrap', async () => {
    const app = setup({ 'POST /api/v2/auth/session': sessionRoute() });
    await app.store.bootstrap();
    const state = app.store.getState();
    expect(state.status).toBe('authenticated');
    expect(state.identity).toEqual({ githubUserId: '101', login: 'member-a' });
    expect(state.csrfToken).toBe('B'.repeat(43));
    // The recovery request is read-only and carries a unique requestId.
    const call = app.calls[0];
    expect(call?.method).toBe('POST');
    expect((call?.body as { requestId: string }).requestId).toBeTruthy();
    // Nothing lands in web storage.
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it('treats an unauthorized recovery as unauthenticated, never as success', async () => {
    const app = setup({ 'POST /api/v2/auth/session': { status: 401, body: { error: 'UNAUTHORIZED' } } });
    await app.store.bootstrap();
    expect(app.store.getState().status).toBe('unauthenticated');
    expect(app.store.getState().csrfToken).toBeUndefined();
  });

  it('reports an unavailable dependency without pretending the user is logged out', async () => {
    const app = setup({ 'POST /api/v2/auth/session': { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } } });
    await app.store.bootstrap();
    const state = app.store.getState();
    expect(state.status).toBe('unavailable');
    expect(state.message).toContain('不表示已登出');
  });

  it('fails closed on an unexpected snapshot shape', async () => {
    const tampered = { ...sessionBootstrapFixture(), sessionToken: 'secret' };
    const app = setup({ 'POST /api/v2/auth/session': { status: 200, body: tampered } });
    await app.store.bootstrap();
    expect(app.store.getState().status).toBe('error');
    expect(app.store.getState().csrfToken).toBeUndefined();
  });

  it('refuses a response whose echoed requestId is not the one it asked for, loudly and retryably', async () => {
    let answered = 0;
    const app = setup({
      'POST /api/v2/auth/session': async (request) => {
        answered += 1;
        const payload = (await request.json()) as { requestId: string };
        // The first answer echoes someone else's requestId (protocol
        // violation); a retry answers correctly and must recover.
        const requestId = answered === 1 ? 'someone-else' : payload.requestId;
        return { status: 200, body: sessionBootstrapFixture({ requestId }) };
      },
    });
    // The store's own start-up recovery is the request that gets the foreign
    // requestId; it must end as a loud, retryable failure, not a spinner.
    await vi.waitFor(() => expect(app.store.getState().status).toBe('error'));
    const state = app.store.getState();
    expect(state.csrfToken).toBeUndefined();
    expect(state.message).toMatch(/不匹配|协议错误/);

    await app.store.bootstrap();
    expect(app.store.getState().status).toBe('authenticated');
    expect(app.store.getState().csrfToken).toBe('B'.repeat(43));
  });

  it('lets only the latest request of an unchanged generation populate state', async () => {
    const pendingAnswers: ((identity: { githubUserId: string; login: string }) => void)[] = [];
    let call = 0;
    const app = setup({
      'POST /api/v2/auth/session': async (request) => {
        call += 1;
        const payload = (await request.json()) as { requestId: string };
        if (call === 1) {
          // The store's own start-up recovery answers promptly.
          return { status: 200, body: sessionBootstrapFixture({ requestId: payload.requestId }) };
        }
        return new Promise<FakeResponse>((resolve) => {
          pendingAnswers.push((identity) =>
            resolve({ status: 200, body: sessionBootstrapFixture({ identity, requestId: payload.requestId }) }));
        });
      },
    });
    await vi.waitFor(() => expect(app.store.getState().status).toBe('authenticated'));

    const first = app.store.bootstrap();
    const second = app.store.bootstrap(); // same generation: no invalidation in between
    await vi.waitFor(() => expect(pendingAnswers).toHaveLength(2));
    // The newer request answers first, the older one straggles in afterwards
    // (a fetch that ignores its abort signal), so "last write wins" would be
    // wrong: only the latest request may populate state.
    pendingAnswers[1]?.({ githubUserId: '202', login: 'member-b' });
    await vi.waitFor(() => expect(app.store.getState().identity).toEqual({ githubUserId: '202', login: 'member-b' }));
    pendingAnswers[0]?.({ githubUserId: '101', login: 'member-a' });
    await Promise.all([first, second]);
    expect(app.store.getState().identity).toEqual({ githubUserId: '202', login: 'member-b' });
  });

  it('re-establishes the cross-tab channel after a stop/start cycle', async () => {
    const created: Channel[] = [];
    const posted: unknown[] = [];
    let listener: ((event: { data: unknown }) => void) | undefined;
    const { fetchImpl, calls } = createFakeFetch({
      'POST /api/v2/auth/session': sessionRoute(),
      'POST /api/v2/auth/logout': { status: 204 },
    });
    const app = createAppSession({
      fetch: fetchImpl,
      subscribeFocus: () => () => undefined,
      createChannel: () => {
        const channel: Channel = {
          postMessage: (message) => posted.push(message),
          addEventListener: (() => undefined) as unknown as () => void,
          removeEventListener: () => undefined,
          close: () => undefined,
        };
        (channel as unknown as { addEventListener: (type: string, cb: (event: { data: unknown }) => void) => void })
          .addEventListener = (type, callback) => {
            if (type === 'message') listener = callback;
          };
        created.push(channel);
        return channel as unknown as BroadcastChannel;
      },
    });
    const stopFirst = app.store.start();
    await vi.waitFor(() => expect(app.store.getState().status).toBe('authenticated'));
    stopFirst();
    const stopSecond = app.store.start();
    await vi.waitFor(() => expect(app.store.getState().status).toBe('authenticated'));

    // The remount must own a live channel again: it can broadcast, and it
    // listens for other tabs again.
    expect(created.length).toBe(2);
    await app.store.logout();
    expect(posted).toContainEqual({ type: 'invalidated' });

    const before = calls.length;
    listener?.({ data: { type: 'invalidated' } });
    await vi.waitFor(() => expect(calls.length).toBeGreaterThan(before));
    stopSecond();
  });

  it('discards a late response from a superseded generation', async () => {
    const first = deferred<FakeResponse>();
    let call = 0;
    const app = setup({
      'POST /api/v2/auth/session': async (request) => {
        call += 1;
        if (call === 1) return first.promise;
        const payload = (await request.json()) as { requestId: string };
        return {
          status: 200,
          body: sessionBootstrapFixture({ identity: { githubUserId: '202', login: 'member-b' }, requestId: payload.requestId }),
        };
      },
    });
    const stale = app.store.bootstrap();
    app.store.invalidate(); // identity switch while the first read is in flight
    const fresh = app.store.bootstrap();
    first.resolve({ status: 200, body: sessionBootstrapFixture({ identity: { githubUserId: '101', login: 'member-a' } }) });
    await Promise.all([stale, fresh]);
    expect(app.store.getState().identity).toEqual({ githubUserId: '202', login: 'member-b' });
  });

  it('clears authentication state when login starts', async () => {
    const app = setup({
      'POST /api/v2/auth/session': sessionRoute(),
      'POST /api/v2/auth/github/login': { status: 200, body: { authorizationUrl: 'https://github.com/login/oauth/authorize?client_id=x' } },
    });
    await app.store.bootstrap();
    expect(app.store.getState().status).toBe('authenticated');
    const before = app.store.generation();
    const url = await app.store.beginLogin('/workspaces');
    expect(url).toContain('https://github.com/login/oauth/authorize');
    expect(app.store.generation()).toBeGreaterThan(before);
    expect(app.store.getState().status).toBe('unauthenticated');
    expect(app.store.getState().csrfToken).toBeUndefined();
  });

  it('revokes durably on logout and notifies other tabs without credentials', async () => {
    const app = setup({
      'POST /api/v2/auth/session': sessionRoute(),
      'POST /api/v2/auth/logout': { status: 204 },
    });
    await vi.waitFor(() => expect(app.store.getState().status).toBe('authenticated'));
    await app.store.logout();
    expect(app.store.getState().status).toBe('unauthenticated');
    expect(app.posted).toEqual([{ type: 'invalidated' }]);
    const logoutCall = app.calls.find((entry) => entry.url.endsWith('/logout'));
    expect(logoutCall?.headers.get('X-CSRF-Token')).toBe('B'.repeat(43));
  });

  it('never presents a failed logout as durable revocation', async () => {
    const app = setup({
      'POST /api/v2/auth/session': sessionRoute(),
      'POST /api/v2/auth/logout': { status: 500, body: { error: 'INTERNAL_ERROR' } },
    });
    await app.store.bootstrap();
    await expect(app.store.logout()).rejects.toThrow();
    const state = app.store.getState();
    expect(state.status).toBe('authenticated');
    expect(state.message).toContain('登出未确认');
    expect(app.posted).toEqual([]);
  });

  it('discards state when a workspace call answers UNAUTHORIZED', async () => {
    const app = setup({
      'POST /api/v2/auth/session': sessionRoute(),
      'GET /api/v1/workspaces/workspace-a1b2c3d4': { status: 401, body: { error: 'UNAUTHORIZED' } },
    });
    await app.store.bootstrap();
    await expect(app.client.getWorkspace('workspace-a1b2c3d4')).rejects.toThrow();
    expect(app.store.getState().status).toBe('unauthenticated');
  });

  it('re-bootstraps on focus return after invalidating outstanding generations', async () => {
    const app = setup({ 'POST /api/v2/auth/session': sessionRoute() });
    await vi.waitFor(() => expect(app.store.getState().status).toBe('authenticated'));
    const before = app.calls.length;
    app.focus();
    await vi.waitFor(() => expect(app.calls.length).toBeGreaterThan(before));
    expect(app.store.getState().status).toBe('authenticated');
  });

  it('re-bootstraps when another tab reports invalidation', async () => {
    let listener: ((event: { data: unknown }) => void) | undefined;
    const channel: Channel = {
      postMessage: () => undefined,
      addEventListener: (() => undefined) as unknown as () => void,
      removeEventListener: () => undefined,
      close: () => undefined,
    };
    const { fetchImpl, calls } = createFakeFetch({
      'POST /api/v2/auth/session': sessionRoute(),
    });
    const app = createAppSession({ fetch: fetchImpl, createChannel: () => channel as unknown as BroadcastChannel, subscribeFocus: () => () => undefined });
    const store = app.store;
    // The channel registers through addEventListener('message', handler).
    const events = channel as unknown as { addEventListener: (type: string, cb: (event: { data: unknown }) => void) => void };
    events.addEventListener = (type, callback) => {
      if (type === 'message') listener = callback;
    };
    const stop = store.start();
    await vi.waitFor(() => expect(store.getState().status).toBe('authenticated'));
    const before = calls.length;
    store.invalidate();
    listener?.({ data: { type: 'invalidated' } });
    await vi.waitFor(() => expect(calls.length).toBeGreaterThan(before));
    await vi.waitFor(() => expect(store.getState().status).toBe('authenticated'));
    stop();
  });
});
