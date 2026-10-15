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
  // `generation` guards session reads (any invalidation, including focus
  // return, makes outstanding answers stale). `cacheEpoch` guards authorized
  // projections and only moves when authentication state is actually
  // discarded: identity/Workspace switch, logout, cross-tab invalidation,
  // `UNAUTHORIZED`, or a recovery snapshot that comes back as another
  // identity. A focus refresh must not throw away unsubmitted drafts.
  let generation = 0;
  let cacheEpoch = 0;
  /** Sequence of recovery reads; only the newest one may populate state. */
  let requestSequence = 0;
  const listeners = new Set<() => void>();
  let inflight: AbortController | undefined;
  let channel: BroadcastChannel | undefined;
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
    cacheEpoch += 1;
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

  /**
   * Every effective `start()` re-establishes the channel: React StrictMode and
   * HMR remount the provider (start → cleanup → start), and a remounted tree
   * must own a live listener and broadcast path again instead of silently
   * losing the frozen cross-tab invalidation obligation.
   */
  function ensureChannel() {
    if (channel) return;
    const create = options.createChannel ?? (() => (typeof BroadcastChannel === 'undefined' ? undefined : new BroadcastChannel(CHANNEL_NAME)));
    channel = create();
  }

  /**
   * Read-only recovery snapshot. Never rotates credentials and never changes
   * role, expiry or CSRF on the server.
   */
  async function bootstrap(): Promise<void> {
    const { generation: started_, controller } = beginRead();
    const sequence = (requestSequence += 1);
    const requestId = newRequestId('bootstrap');
    setState({ ...state, refreshing: state.status === 'authenticated' });
    try {
      const snapshot = await options.client.bootstrapSession(requestId, controller.signal);
      if (generation !== started_) return; // a newer generation owns the state
      // Only the latest read of this generation may populate state, even if an
      // older one resolves late without observing its abort (defence in depth;
      // a real fetch rejects on abort).
      if (sequence !== requestSequence) return;
      if (snapshot.requestId !== requestId) {
        // A snapshot that does not echo this request is a protocol violation,
        // exactly like an unknown field: fail closed, say so, and stay
        // retryable instead of waiting forever on a spinner.
        setState({
          status: 'error',
          refreshing: false,
          message: '会话恢复响应与本次请求不匹配，已按协议错误拒绝使用该数据；请重试。',
        });
        return;
      }
      // A recovered identity that differs from the one this tab held means the
      // authentication state changed underneath it: discard the authorized
      // caches (workspaces, members, runtimes) rather than inheriting them.
      if (state.identity !== undefined && state.identity.githubUserId !== snapshot.identity.githubUserId) {
        cacheEpoch += 1;
      }
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

  /**
   * Called when the tab regains focus: outstanding session reads are
   * invalidated and a fresh bootstrap runs before any write (writes are
   * refused while `refreshing`). The cache epoch deliberately does not move
   * here: the same identity in the same Workspace keeps its drafts and
   * projections, and the server still decides every write against the version
   * it really holds.
   */
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
    /** Read generation; changes on every invalidation, including focus return. */
    generation(): number {
      return generation;
    },
    /**
     * Identity/Workspace cache epoch; include it in resource dependencies.
     * Changes only when authentication state is discarded.
     */
    cacheEpoch(): number {
      return cacheEpoch;
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
