import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ErrorBoundary } from '../../src/ui/ErrorBoundary';
import { EmptyState, LoadingState } from '../../src/ui/Feedback';
import { Page } from '../../src/ui/Page';
import { createFakeFetch, renderApp, withRoutes } from './render';

/**
 * S00 shell characterization retained for S01. The shell semantics (skip link,
 * route-change focus, router navigation, theme without storage, error
 * containment) must survive the new guarded routes; the home route itself is
 * intentionally replaced by the workspace entry, which is asserted here as the
 * new target behaviour rather than folded into the old expectation.
 */
describe('application shell', () => {
  it('renders semantic shell and composes a guarded route for an anonymous visitor', async () => {
    const { fetchImpl } = createFakeFetch(withRoutes({}));
    renderApp({ fetch: fetchImpl });
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '登录 Kowa' })).toBeInTheDocument();
    expect(screen.getByRole('main')).toContainElement(screen.getByRole('heading', { name: '登录 Kowa' }));
    expect(screen.getByRole('link', { name: '工作区' })).toBeInTheDocument();
  });

  it('recovers an unknown deep link and moves focus on navigation', async () => {
    const user = userEvent.setup();
    const { fetchImpl } = createFakeFetch(withRoutes({}));
    renderApp({ fetch: fetchImpl, initialEntries: ['/missing/deep'] });
    expect(screen.getByRole('heading', { name: '页面不存在' })).toBeInTheDocument();
    await user.click(screen.getByRole('link', { name: '返回工作区' }));
    expect(await screen.findByRole('heading', { name: '登录 Kowa' })).toBeInTheDocument();
    expect(screen.getByRole('main')).toHaveFocus();
    expect(document.title).toBe('登录 · Kowa');
  });

  it('switches theme without storing session or permission data', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    const { fetchImpl } = createFakeFetch(withRoutes({}));
    renderApp({ fetch: fetchImpl });
    const select = screen.getByLabelText('外观');
    fireEvent.change(select, { target: { value: 'dark' } });
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    fireEvent.change(select, { target: { value: 'light' } });
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(setItem).not.toHaveBeenCalled();
    await waitFor(() => expect(localStorage.length).toBe(0));
    expect(sessionStorage.length).toBe(0);
  });

  it('announces loading and composes empty-state actions', () => {
    const { rerender } = render(<LoadingState />);
    expect(screen.getByRole('status')).toHaveTextContent('正在加载');
    rerender(<EmptyState title="暂无内容"><a href="/">返回</a></EmptyState>);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '暂无内容' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '返回' })).toBeInTheDocument();
  });

  it('accepts arbitrary page content without business-specific props', () => {
    render(<Page title="阅读"><p>内容</p><button>操作</button></Page>);
    expect(screen.getByRole('heading', { level: 1, name: '阅读' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '操作' })).toBeInTheDocument();
  });

  it('contains render failures, focuses the message and retries', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const handleExpectedError = (event: ErrorEvent) => {
      if (event.error instanceof Error && event.error.message === 'private diagnostic') event.preventDefault();
    };
    window.addEventListener('error', handleExpectedError);
    let broken = true;
    function Content() {
      if (broken) throw new Error('private diagnostic');
      return <p>已恢复</p>;
    }
    render(<ErrorBoundary><Content /></ErrorBoundary>);
    expect(screen.getByRole('alert')).toHaveFocus();
    expect(screen.queryByText('private diagnostic')).not.toBeInTheDocument();
    broken = false;
    await userEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(screen.getByText('已恢复').parentElement).toHaveFocus();
    window.removeEventListener('error', handleExpectedError);
  });
});
