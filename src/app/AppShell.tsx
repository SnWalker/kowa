import { Suspense, useEffect, useRef } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { ErrorBoundary } from '../ui/ErrorBoundary';
import { LoadingState } from '../ui/Feedback';
import { ThemeSelect } from './ThemeSelect';

export function AppShell() {
  const location = useLocation();
  const main = useRef<HTMLElement>(null);
  const previousLocation = useRef(location.key);

  useEffect(() => {
    document.title = `${location.pathname === '/' ? '首页' : '页面不存在'} · Kowa`;
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
          <NavLink className="brand" to="/" aria-label="Kowa 首页">
            <span className="brand-mark" aria-hidden="true">K</span><span>Kowa</span>
          </NavLink>
          <ThemeSelect />
        </header>
        <aside className="sidebar">
          <nav aria-label="主导航">
            <NavLink className="nav-link" to="/" end>首页</NavLink>
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
