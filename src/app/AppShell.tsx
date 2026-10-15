import { Suspense, useEffect, useRef } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { useSession } from '../auth/session-context';
import { ErrorBoundary } from '../ui/ErrorBoundary';
import { LoadingState } from '../ui/Feedback';
import { ThemeSelect } from './ThemeSelect';

export function AppShell() {
  const location = useLocation();
  const main = useRef<HTMLElement>(null);
  const previousLocation = useRef(location.key);
  const workspaceId = workspaceIdFromPath(location.pathname);

  useEffect(() => {
    document.title = `${titleFor(location.pathname)} · Kowa`;
    if (previousLocation.current !== location.key) {
      main.current?.focus();
      previousLocation.current = location.key;
    }
  }, [location.key, location.pathname]);

  return (
    <>
      <a className="skip-link" href="#main" onClick={() => main.current?.focus()}>跳至主要内容</a>
      <div className="app-shell">
        <header className="app-header">
          <NavLink className="brand" to="/workspaces" aria-label="Kowa 首页">
            <span className="brand-mark" aria-hidden="true">K</span><span>Kowa</span>
          </NavLink>
          <div className="header-tools">
            <SessionSummary />
            <ThemeSelect />
          </div>
        </header>
        <aside className="sidebar">
          <nav aria-label="主导航">
            <NavLink className="nav-link" to="/workspaces" end>工作区</NavLink>
            {workspaceId && (
              <>
                <NavLink className="nav-link" to={`/workspaces/${encodeURIComponent(workspaceId)}/settings`}>Workspace 设置</NavLink>
                <NavLink className="nav-link" to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items`}>工作项</NavLink>
                <NavLink className="nav-link" to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes`}>Runtime</NavLink>
              </>
            )}
          </nav>
          <p className="sidebar-caption">研发协作，从这里开始。</p>
        </aside>
        <main id="main" ref={main} tabIndex={-1}>
          <ErrorBoundary key={location.key}>
            <Suspense fallback={<LoadingState />}><Outlet /></Suspense>
          </ErrorBoundary>
        </main>
        <footer className="app-footer">Kowa</footer>
      </div>
    </>
  );
}

function SessionSummary() {
  const { state, logout } = useSession();
  if (state.status !== 'authenticated') {
    return <p className="session-summary">{state.status === 'checking' ? '正在确认会话…' : '未登录'}</p>;
  }
  return (
    <div className="session-summary">
      <span className="session-identity">
        登录身份：{state.identity?.login}（{state.identity?.githubUserId}）
      </span>
      <button
        type="button"
        onClick={() => {
          void logout().catch(() => undefined); // the store keeps the session and reports the failure
        }}
      >
        登出
      </button>
      {state.message ? <span role="status" className="session-warning">{state.message}</span> : null}
    </div>
  );
}

const WORKSPACE_PATH = /^\/workspaces\/([^/]+)(?:\/(settings|runtimes|work-items)(?:\/[^/]+)?)?$/;

function workspaceIdFromPath(pathname: string): string | undefined {
  const match = WORKSPACE_PATH.exec(pathname);
  return match?.[1] ? decodeURIComponent(match[1]) : undefined;
}

function titleFor(pathname: string): string {
  if (pathname === '/' || pathname === '/workspaces') return 'Workspace';
  if (pathname === '/login') return '登录';
  const match = WORKSPACE_PATH.exec(pathname);
  if (!match) return '页面不存在';
  if (match[2] === 'settings') return 'Workspace 设置';
  if (match[2] === 'runtimes') return pathname.split('/').length > 4 ? 'Runtime 详情' : 'Runtime';
  if (match[2] === 'work-items') {
    if (pathname.endsWith('/new')) return '新建工作项';
    return pathname.split('/').length > 4 ? '工作项详情' : '工作项';
  }
  return 'Workspace';
}
