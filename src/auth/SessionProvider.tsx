import { useEffect, type ReactNode } from 'react';
import { SessionContext, defaultSession, type SessionValue } from './session-context';

export function SessionProvider({ children, value }: { children: ReactNode; value?: SessionValue }) {
  const session = value ?? defaultSession;
  // Starts the per-tab recovery snapshot and the cross-tab/focus subscriptions.
  useEffect(() => session.store.start(), [session.store]);
  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}
