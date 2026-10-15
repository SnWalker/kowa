import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { Button } from '../ui/button';
import { LoadingState } from '../ui/Feedback';
import { Notice } from '../ui/States';
import { safeReturnTo } from './returnTo';
import { useSession } from './session-context';

/**
 * Authentication guard. It only reflects the server-established session: an
 * unconfirmed session is never treated as authenticated, and an unavailable
 * dependency is reported as such instead of being rendered as a logged-out
 * success or as usable content.
 */
export function RequireSession() {
  const { state, refresh } = useSession();
  const location = useLocation();

  if (state.status === 'checking') return <LoadingState />;
  if (state.status === 'unauthenticated') {
    const returnTo = safeReturnTo(`${location.pathname}${location.search}`);
    return <Navigate to={`/login?returnTo=${encodeURIComponent(returnTo)}`} replace />;
  }
  if (state.status === 'unavailable' || state.status === 'error') {
    return (
      <Notice tone="warning" title="会话状态暂时无法确认">
        <p>{state.message ?? '请稍后重试。'}</p>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button type="button" variant="secondary" onClick={() => void refresh()}>重试</Button>
        </div>
      </Notice>
    );
  }
  return <Outlet />;
}
