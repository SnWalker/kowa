import { useCallback, useEffect, useRef, useState } from 'react';

export type Resource<T> = {
  status: 'loading' | 'ready' | 'error';
  data?: T;
  error?: unknown;
  reload: () => void;
};

/**
 * Loads one authorized projection and discards it whenever the caller's
 * dependencies change. Passing the session generation (and the workspace id)
 * as dependencies is what keeps caches isolated across identities and
 * workspaces: a switch resets this state instead of inheriting it.
 */
export function useResource<T>(loader: (signal: AbortSignal) => Promise<T>, deps: readonly unknown[]): Resource<T> {
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<{ status: 'loading' | 'ready' | 'error'; data?: T; error?: unknown }>({ status: 'loading' });
  const loaderRef = useRef(loader);
  loaderRef.current = loader;

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: 'loading' });
    loaderRef.current(controller.signal).then(
      (data) => {
        if (!controller.signal.aborted) setState({ status: 'ready', data });
      },
      (error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        setState({ status: 'error', error });
      },
    );
    return () => controller.abort();
  }, [...deps, attempt]);

  const reload = useCallback(() => setAttempt((value) => value + 1), []);
  return { ...state, reload };
}
