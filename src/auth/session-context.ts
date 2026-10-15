import { createContext, useContext, useSyncExternalStore } from 'react';
import { createAppSession, type SessionState, type SessionStore } from '../api/session';
import type { ApiClient } from '../api/client';

export type SessionValue = {
  store: SessionStore;
  client: ApiClient;
  /** Leaves the SPA for the owner-produced authorization URL. */
  navigateAway: (url: string) => void;
};

export const SessionContext = createContext<SessionValue | null>(null);

export const defaultSession: SessionValue = {
  ...createAppSession(),
  navigateAway: (url: string) => {
    // Defence in depth: the decoder already refuses non-http(s) navigation
    // targets; the navigation site never hands the browser another scheme.
    if (!/^https?:\/\//i.test(url)) throw new Error('拒绝导航到非 http(s) 地址');
    window.location.assign(url);
  },
};

export function useSession() {
  const session = useContext(SessionContext);
  if (!session) throw new Error('SessionProvider is missing');
  const state: SessionState = useSyncExternalStore(session.store.subscribe, session.store.getState);

  return {
    ...session,
    state,
    /** Read generation: changes on any invalidation, including focus return. */
    generation: session.store.generation(),
    /** Identity/Workspace cache epoch: include it in resource dependencies. */
    cacheEpoch: session.store.cacheEpoch(),
    async login(returnTo: string) {
      const url = await session.store.beginLogin(returnTo);
      session.navigateAway(url);
    },
    async logout() {
      await session.store.logout();
    },
    async refresh() {
      await session.store.bootstrap();
    },
  };
}

/**
 * CSRF for a mutating call. Called from submit handlers, not render: without an
 * authenticated session the mutation is refused locally instead of being sent
 * without proof. While a focus-triggered re-bootstrap is in flight the write is
 * also refused: the contract requires bootstrapping again before writes, and
 * the draft stays in the form until the refresh completes.
 */
export function requireCsrf(state: SessionState): string {
  if (state.status !== 'authenticated' || !state.csrfToken) {
    throw new Error('会话未确认或已失效：写操作已在浏览器侧拒绝');
  }
  if (state.refreshing) {
    throw new Error('会话正在刷新，请稍候重试：写操作需要最新的会话确认');
  }
  return state.csrfToken;
}
