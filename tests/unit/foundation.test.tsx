import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { App } from '../../src/app/App';
import { ErrorBoundary } from '../../src/ui/ErrorBoundary';
import { EmptyState, LoadingState } from '../../src/ui/Feedback';
import { Page } from '../../src/ui/Page';

describe('application foundation', () => {
  it('renders semantic shell and composes the home route', () => {
    render(<MemoryRouter><App /></MemoryRouter>);
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument();
    expect(screen.getByRole('main')).toContainElement(screen.getByRole('heading', { name: '欢迎使用 Kowa' }));
    expect(screen.getByRole('link', { name: '首页', current: 'page' })).toBeInTheDocument();
  });

  it('recovers an unknown deep link and moves focus on navigation', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter initialEntries={['/missing/deep']}><App /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: '页面不存在' })).toBeInTheDocument();
    await user.click(screen.getByRole('link', { name: '返回首页' }));
    expect(screen.getByRole('heading', { name: '欢迎使用 Kowa' })).toBeInTheDocument();
    expect(screen.getByRole('main')).toHaveFocus();
    expect(document.title).toBe('首页 · Kowa');
  });

  it('switches theme without storing session or permission data', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    render(<MemoryRouter><App /></MemoryRouter>);
    const select = screen.getByLabelText('外观');
    fireEvent.change(select, { target: { value: 'dark' } });
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    fireEvent.change(select, { target: { value: 'light' } });
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(setItem).not.toHaveBeenCalled();
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
