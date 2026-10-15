import { Workflow } from 'lucide-react';
import { Suspense, useEffect, useRef } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { ErrorBoundary } from '../ui/ErrorBoundary';
import { LoadingState } from '../ui/Feedback';
import { ThemeSelect } from './ThemeSelect';

/**
 * The application chrome: one header (brand, session, appearance), a sidebar
 * for the workspace's own navigation, and the routed content on the muted
 * canvas the content boxes sit on. The order of the first tab stops — skip
 * link, brand, appearance, primary navigation — is part of the shell's frozen
 * keyboard contract.
 */
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
      <div className="flex min-h-dvh flex-col bg-muted">
        <header className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-border bg-card px-4 py-2">
          <NavLink className="flex items-center gap-2 text-foreground no-underline" to="/workspaces" aria-label="Kowa 首页">
            <span className="grid size-8 place-items-center rounded-md border border-border bg-muted text-primary-fg">
              <Workflow aria-hidden="true" className="size-4" />
            </span>
            <span className="text-lg font-semibold">Kowa</span>
          </NavLink>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <SessionSummary />
            <ThemeSelect />
          </div>
        </header>
        <div className="flex min-w-0 flex-1 flex-col md:flex-row">
          <aside className="border-b border-border bg-muted px-3 py-3 md:w-60 md:shrink-0 md:border-b-0 md:border-r">
            <nav aria-label="主导航" className="flex flex-col gap-0.5">
              <NavLink className={navLinkClass} to="/workspaces" end>工作区</NavLink>
              {workspaceId && (
                <>
                  <NavLink className={navLinkClass} to={`/workspaces/${encodeURIComponent(workspaceId)}/settings`}>Workspace 设置</NavLink>
                  <NavLink className={navLinkClass} to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items`}>工作项</NavLink>
                  <NavLink className={navLinkClass} to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes`}>Runtime</NavLink>
                </>
              )}
            </nav>
            <p className="mt-6 hidden px-3 text-xs text-muted-foreground md:block">研发协作，从这里开始。</p>
          </aside>
          <main id="main" ref={main} tabIndex={-1} className="min-w-0 flex-1 px-4 py-6 md:px-8">
            <ErrorBoundary key={location.key}>
              <Suspense fallback={<LoadingState />}><Outlet /></Suspense>
            </ErrorBoundary>
          </main>
        </div>
        <footer className="border-t border-border px-4 py-3 text-xs text-muted-foreground">Kowa</footer>
      </div>
    </>
  );
}

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    'block rounded-md px-3 py-1.5 text-sm no-underline',
    isActive
      ? 'bg-primary-muted font-semibold text-primary-fg'
      : 'text-foreground hover:bg-accent',
  );

function SessionSummary() {
  const { state, logout } = useSession();
  if (state.status !== 'authenticated') {
    return <p className="text-xs text-muted-foreground">{state.status === 'checking' ? '正在确认会话…' : '未登录'}</p>;
  }
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs text-muted-foreground">
      <span className="break-anywhere">
        登录身份：{state.identity?.login}（<code>{state.identity?.githubUserId}</code>）
      </span>
      <Button
        variant="secondary"
        size="sm"
        type="button"
        onClick={() => {
          void logout().catch(() => undefined); // the store keeps the session and reports the failure
        }}
      >
        登出
      </Button>
      {state.message ? <span role="status" className="font-semibold text-attention">{state.message}</span> : null}
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
