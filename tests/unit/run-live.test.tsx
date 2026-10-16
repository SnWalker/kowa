/**
 * "An old observation is not a new fact."
 *
 * While a run is live the browser keeps re-reading the server projection, and
 * the moment a re-read fails the last projection stops being current. The
 * failure that matters here is silent staleness: a page that keeps rendering a
 * disconnected read as if it were the current one. These tests pin both halves
 * — the last observed watermark stays visible, and the disconnect is stated in
 * words rather than implied by a missing spinner.
 */
import { act, cleanup, render, renderHook, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RunDetail } from '../../src/api/contracts';
import { LiveStatus } from '../../src/runs/LiveStatus';
import { useRunLive } from '../../src/runs/useRunLive';
import { runDetailFixture } from '../fixtures/contracts';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const detail = (overrides: Partial<RunDetail> = {}) =>
  runDetailFixture({ watermark: { observedAt: '2026-10-16T08:05:03Z', runUpdatedAt: '2026-10-16T08:05:03Z' }, ...overrides });

/** A loader whose answer the test swaps between calls. */
function sequence(...answers: (RunDetail | Error)[]) {
  let index = 0;
  const load = async (): Promise<RunDetail> => {
    const answer = answers[Math.min(index, answers.length - 1)];
    index += 1;
    if (answer instanceof Error) throw answer;
    return answer as RunDetail;
  };
  return { load, calls: () => index };
}

describe('useRunLive keeps the last observation and reports a disconnect', () => {
  it('keeps the last projection but reports itself disconnected when a refresh fails', async () => {
    vi.useFakeTimers();
    const { load } = sequence(detail(), new Error('network down'));
    const { result } = renderHook(() =>
      useRunLive({ load, scope: 'scope', generation: 1, intervalMs: 1000 }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.status).toBe('ready');
    expect(result.current.disconnected).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(result.current.disconnected).toBe(true);
    // The projection a disconnected page shows is the last one it really read.
    expect(result.current.detail?.workflowRunId).toBe(detail().workflowRunId);
    expect(result.current.lastObservedAt).toBe('2026-10-16T08:05:03Z');
  });

  it('clears the disconnect once a read succeeds again', async () => {
    vi.useFakeTimers();
    const { load } = sequence(detail(), new Error('network down'), detail({ version: 5 }));
    const { result } = renderHook(() =>
      useRunLive({ load, scope: 'scope', generation: 1, intervalMs: 1000 }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(result.current.disconnected).toBe(true);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(result.current.disconnected).toBe(false);
    expect(result.current.detail?.version).toBe(5);
  });

  it('stops polling once the run reached a terminal state', async () => {
    vi.useFakeTimers();
    const { load, calls } = sequence(detail({ state: 'SUCCEEDED' }));
    const { result } = renderHook(() =>
      useRunLive({ load, scope: 'scope', generation: 1, intervalMs: 1000 }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.status).toBe('ready');
    const afterInitial = calls();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(calls()).toBe(afterInitial);
  });

  it('reports a first read that never succeeded as an error, not as staleness', async () => {
    vi.useFakeTimers();
    const { load } = sequence(new Error('network down'));
    const { result } = renderHook(() =>
      useRunLive({ load, scope: 'scope', generation: 1, intervalMs: 1000 }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.status).toBe('error');
    expect(result.current.detail).toBeUndefined();
    expect(result.current.disconnected).toBe(false);
  });
});

describe('LiveStatus states the connection instead of implying it', () => {
  it('says the live update is down, names the last watermark and offers a retry', async () => {
    const user = userEvent.setup({ delay: null });
    const onRetry = vi.fn();
    render(<LiveStatus disconnected lastObservedAt="2026-10-16T08:05:03Z" onRetry={onRetry} />);

    expect(screen.getByText(/实时更新已断开/)).toBeInTheDocument();
    expect(screen.getByText(/最后观测/)).toBeInTheDocument();
    // The server watermark itself is on screen, as its own machine fact.
    expect(screen.getByText(/^2026-10-16T08:05:03/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /立即重读/ }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('never claims the stale projection is current', () => {
    render(<LiveStatus disconnected lastObservedAt="2026-10-16T08:05:03Z" onRetry={() => undefined} />);
    // The wording may not present the retained projection as the present fact.
    expect(screen.queryByText(/实时更新中/)).not.toBeInTheDocument();
    expect(screen.getByText(/此后的事实未知/)).toBeInTheDocument();
  });

  it('shows only the last watermark while the connection is healthy', () => {
    render(<LiveStatus disconnected={false} lastObservedAt="2026-10-16T08:05:03Z" onRetry={() => undefined} />);
    expect(screen.getByText(/最后观测/)).toBeInTheDocument();
    expect(screen.queryByText(/实时更新已断开/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
