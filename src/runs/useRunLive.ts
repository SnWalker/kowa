import { useEffect, useRef, useState } from 'react';
import type { RunDetail } from '../api/contracts';

/**
 * Re-reads one live run projection and is honest about the moment it stops.
 *
 * The read is a poll, not a socket: the frozen web contract publishes no event
 * stream, so "live" here means "re-read the server projection on an interval"
 * and the browser never derives a run fact it did not receive.
 *
 * The three outcomes are deliberately distinct:
 *
 * - the first read never arrived → an error; nothing is known, so nothing may
 *   be shown as known;
 * - a later read failed → the projection that stays on screen is the last one
 *   really read, and the caller is told it is disconnected, so it can say so;
 * - a read succeeded → `lastObservedAt` is the server's own watermark, which is
 *   the only clock that can say how old the projection is.
 *
 * Polling stops once the run reaches a terminal state: a finished run is not
 * going to change, and re-reading it forever would dress a settled fact up as
 * live.
 */
const TERMINAL_RUN_STATES: readonly RunDetail['state'][] = ['SUCCEEDED', 'FAILED', 'CANCELED'];

export const RUN_LIVE_INTERVAL_MS = 4000;

export type RunLiveState = {
  status: 'loading' | 'ready' | 'error';
  detail?: RunDetail;
  error?: unknown;
  /** A read failed after the projection was already available. */
  disconnected: boolean;
  /** The server watermark of the last successful read. */
  lastObservedAt?: string;
  reconnect: () => void;
};

export function useRunLive(options: {
  load: (signal: AbortSignal) => Promise<RunDetail>;
  scope: string;
  generation: number;
  intervalMs?: number;
}): RunLiveState {
  const { scope, generation, intervalMs = RUN_LIVE_INTERVAL_MS } = options;
  const loadRef = useRef(options.load);
  loadRef.current = options.load;

  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<{
    status: 'loading' | 'ready' | 'error';
    detail?: RunDetail;
    error?: unknown;
    disconnected: boolean;
    lastObservedAt?: string;
    forScope?: string;
  }>({ status: 'loading', disconnected: false });

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;

    const schedule = () => {
      timer = setTimeout(() => void read(false), intervalMs);
    };

    async function read(first: boolean) {
      try {
        const detail = await loadRef.current(controller.signal);
        if (cancelled || controller.signal.aborted) return;
        setState({ status: 'ready', detail, disconnected: false, lastObservedAt: detail.watermark.observedAt, forScope: scope });
        if (!TERMINAL_RUN_STATES.includes(detail.state)) schedule();
      } catch (error) {
        if (cancelled || controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        setState((current) =>
          // A first read that never landed leaves nothing to be stale about:
          // that is an error, and it is never dressed up as a disconnect.
          first || current.status !== 'ready'
            ? { status: 'error', error, disconnected: false, forScope: scope }
            : { ...current, disconnected: true },
        );
        schedule();
      }
    }

    // A new identity/Workspace never inherits the previous projection; the same
    // scope keeps what it already read while the re-read is in flight.
    setState((current) => (current.forScope === scope && current.status === 'ready' ? current : { status: 'loading', disconnected: false }));
    void read(true);

    return () => {
      cancelled = true;
      controller.abort();
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [scope, generation, intervalMs, attempt]);

  if (state.forScope !== undefined && state.forScope !== scope) {
    return { status: 'loading', disconnected: false, reconnect: () => setAttempt((value) => value + 1) };
  }
  return { ...state, reconnect: () => setAttempt((value) => value + 1) };
}
