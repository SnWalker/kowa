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
    /** Authentication generation: include it in resource dependencies. */
    generation: session.store.generation(),
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
 * without proof.
 */
export function requireCsrf(state: SessionState): string {
  if (state.status !== 'authenticated' || !state.csrfToken) {
    throw new Error('会话未确认或已失效：写操作已在浏览器侧拒绝');
  }
  return state.csrfToken;
}
