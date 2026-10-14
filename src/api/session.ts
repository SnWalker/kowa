/**
 * In-memory Web session client for `kowa.web-session.v1`.
 *
 * Consumer obligations implemented here (frozen in
 * `api/web-session/v1/README.md`, "Recovery algorithm and browser consumption"):
 *
 * 1. CSRF, identity, sessionId and every authorized cache live in memory only --
 *    never in localStorage/sessionStorage or the URL. A full reload recovers
 *    state through `session.bootstrap` (read-only, no CSRF required).
 * 2. Every bootstrap carries a unique `requestId` and captures the local
 *    authentication generation. Only the latest request in an unchanged
 *    generation may populate state; the echoed `requestId` is compared and an
 *    unexpected shape fails closed. Logout, login start, identity/Workspace
 *    switches and unauthorized responses increase the generation and clear
 *    state before further requests; previous reads are aborted and their late
 *    results ignored.
 * 3. Each tab bootstraps on its own and never rotates credentials; tabs notify
 *    one another about invalidation without transmitting credentials; returning
 *    focus invalidates outstanding generations and re-bootstraps before writes.
 * 4. `UNAUTHORIZED` discards authentication state and requires a new login; a
 *    recovery failure is not a logged-out success and a failed logout is not
 *    presented as durable revocation. `RESOURCE_UNAVAILABLE` is an external or
 *    configuration fact, never proof of authentication.
 * 5. A recovered identity carries no Workspace role: membership, object
 *    ownership, version and idempotency are re-checked by the server per call.
 */
import { ApiError, createApiClient } from './client';
import type { ApiClient } from './client';
import type { ApiErrorCategory, WebIdentity } from './contracts';

export type SessionStatus = 'checking' | 'authenticated' | 'unauthenticated' | 'unavailable' | 'error';

export type SessionState = {
  status: SessionStatus;
  identity?: WebIdentity;
  csrfToken?: string;
  sessionId?: string;
  expiresAt?: string;
  /** True while a background re-bootstrap is in flight. */
  refreshing: boolean;
  /** Human-readable reason for `unavailable`/`error`; never a credential. */
  message?: string;
  category?: ApiErrorCategory;
};

export type SessionStore = ReturnType<typeof createSessionStore>;

const CHANNEL_NAME = 'kowa-web-session.v1';

export function newRequestId(prefix: string): string {
  const random = typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `${prefix}-${random}`.slice(0, 128);
}

export function createSessionStore(options: {
  client: ApiClient;
  /** Cross-tab invalidation channel; injectable for tests. */
  createChannel?: () => BroadcastChannel | undefined;
  /** Subscription to focus/visibility changes; injectable for tests. */
  subscribeFocus?: (listener: () => void) => () => void;
}) {
  let state: SessionState = { status: 'checking', refreshing: false };
  let generation = 0;
  const listeners = new Set<() => void>();
  let inflight: AbortController | undefined;
  let channel: BroadcastChannel | undefined;
  let channelClosed = false;
  let started = false;

  function notify() {
    for (const listener of listeners) listener();
  }

  function setState(next: SessionState) {
    state = next;
    notify();
  }

  function beginRead(): { generation: number; controller: AbortController } {
    inflight?.abort();
    inflight = new AbortController();
    return { generation, controller: inflight };
  }

  /**
   * Identity/Workspace switch or any invalidation: bump and clear before
   * further work. Authentication state is discarded, so the only truthful
   * status left is "unauthenticated until a new recovery snapshot says
   * otherwise" -- an invalidated session is never rendered as usable.
   */
  function invalidate(): number {
    generation += 1;
    inflight?.abort();
    inflight = undefined;
    setState({ status: 'unauthenticated', refreshing: false });
    return generation;
  }

  function broadcastInvalidated() {
    channel?.postMessage({ type: 'invalidated' });
  }

  function handleMessage(event: MessageEvent) {
    const data = event.data as { type?: string } | null;
    if (data && data.type === 'invalidated') {
      invalidate();
      void bootstrap();
    }
  }

  function ensureChannel() {
    if (channel || channelClosed) return;
    const create = options.createChannel ?? (() => (typeof BroadcastChannel === 'undefined' ? undefined : new BroadcastChannel(CHANNEL_NAME)));
    channel = create();
  }

  /**
   * Read-only recovery snapshot. Never rotates credentials and never changes
   * role, expiry or CSRF on the server.
   */
  async function bootstrap(): Promise<void> {
    const { generation: started_, controller } = beginRead();
    const requestId = newRequestId('bootstrap');
    setState({ ...state, refreshing: state.status === 'authenticated' });
    try {
      const snapshot = await options.client.bootstrapSession(requestId, controller.signal);
      if (generation !== started_) return; // a newer generation owns the state
      if (snapshot.requestId !== requestId) return; // not this request's answer
      setState({
        status: 'authenticated',
        identity: snapshot.identity,
        csrfToken: snapshot.csrfToken,
        sessionId: snapshot.sessionId,
        expiresAt: snapshot.expiresAt,
        refreshing: false,
      });
    } catch (error) {
      if (generation !== started_) return;
      if (error instanceof ApiError && error.category === 'UNAUTHORIZED') {
        setState({ status: 'unauthenticated', refreshing: false });
        return;
      }
      if (error instanceof ApiError && error.category === 'RESOURCE_UNAVAILABLE') {
        setState({
          status: 'unavailable',
          refreshing: false,
          category: error.category,
          message: '身份/外部依赖当前不可用；这不表示已登出，也不证明任何权限。',
        });
        return;
      }
      if (error instanceof DOMException && error.name === 'AbortError') return;
      setState({
        status: 'error',
        refreshing: false,
        category: error instanceof ApiError ? error.category : undefined,
        message: '会话状态无法确认，请重试。',
      });
    }
  }

  /** Start the OAuth handoff. Returns the owner-produced authorization URL. */
  async function beginLogin(returnTo: string): Promise<string> {
    invalidate();
    const { controller } = beginRead();
    const response = await options.client.beginLogin(returnTo, controller.signal);
    return response.authorizationUrl;
  }

  /**
   * Durable revocation. A failure keeps the current state: it must never be
   * presented as a successful logout.
   */
  async function logout(): Promise<void> {
    if (state.status !== 'authenticated' || !state.csrfToken) {
      invalidate();
      broadcastInvalidated();
      return;
    }
    const { controller } = beginRead();
    try {
      await options.client.logout(state.csrfToken, controller.signal);
    } catch (error) {
      if (error instanceof ApiError && error.category === 'UNAUTHORIZED') {
        invalidate();
        broadcastInvalidated();
        return;
      }
      setState({
        ...state,
        refreshing: false,
        message: '登出未确认：服务端撤销未完成，当前会话可能仍然有效。',
      });
      throw error;
    }
    invalidate();
    broadcastInvalidated();
  }

  /** Called when the tab regains focus: outstanding reads are invalidated first. */
  async function resume(): Promise<void> {
    if (state.status !== 'authenticated') return;
    generation += 1;
    inflight?.abort();
    inflight = undefined;
    await bootstrap();
  }

  function start(): () => void {
    if (started) return () => undefined;
    started = true;
    ensureChannel();
    if (channel && 'addEventListener' in channel) {
      // onmessage covers older engines; addEventListener covers teardown.
      channel.addEventListener('message', handleMessage);
    }
    const unsubscribeFocus = (options.subscribeFocus ?? subscribeWindowFocus)(() => {
      void resume();
    });
    void bootstrap();
    return () => {
      unsubscribeFocus();
      channel?.removeEventListener?.('message', handleMessage);
      channel?.close();
      channel = undefined;
      channelClosed = true;
      started = false;
    };
  }

  return {
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getState(): SessionState {
      return state;
    },
    /** Authentication generation; changes whenever caches must be discarded. */
    generation(): number {
      return generation;
    },
    start,
    bootstrap,
    beginLogin,
    logout,
    resume,
    invalidate,
  };
}

/**
 * Compose the client and the store so any `UNAUTHORIZED` answer from any
 * operation invalidates authentication state exactly once, in one place.
 */
export function createAppSession(options: {
  fetch?: typeof fetch;
  createChannel?: () => BroadcastChannel | undefined;
  subscribeFocus?: (listener: () => void) => () => void;
} = {}) {
  const holder: { store?: SessionStore } = {};
  const client = createApiClient({
    ...(options.fetch ? { fetch: options.fetch } : {}),
    onUnauthorized: () => holder.store?.invalidate(),
  });
  const store = createSessionStore({
    client,
    ...(options.createChannel ? { createChannel: options.createChannel } : {}),
    ...(options.subscribeFocus ? { subscribeFocus: options.subscribeFocus } : {}),
  });
  holder.store = store;
  return { client, store };
}

function subscribeWindowFocus(listener: () => void): () => void {
  if (typeof document === 'undefined') return () => undefined;
  const onVisible = () => {
    if (document.visibilityState === 'visible') listener();
  };
  document.addEventListener('visibilitychange', onVisible);
  window.addEventListener('focus', listener);
  return () => {
    document.removeEventListener('visibilitychange', onVisible);
    window.removeEventListener('focus', listener);
  };
}
