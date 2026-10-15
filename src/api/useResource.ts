import { useCallback, useEffect, useRef, useState } from 'react';

export type Resource<T> = {
  status: 'loading' | 'ready' | 'error';
  data?: T;
  error?: unknown;
  reload: () => void;
};

/**
 * Loads one authorized projection and keeps it bound to exactly one
 * identity/Workspace.
 *
 * Two lifecycles are deliberately separate:
 *
 * - `options.scope` (identity + Workspace + cache epoch) names who the caller
 *   is reading for. Changing it hides the previous projection until the new one
 *   lands, so a switch never inherits another identity's data; callers key
 *   their forms on the scope to drop drafts as well.
 * - `options.generation` (session read generation) invalidates outstanding
 *   reads: a focus return aborts the read that was in flight and re-reads, and
 *   a late answer from the superseded generation — success or failure — is
 *   ignored even if it never observed its abort. The current projection stays
 *   visible while that re-read is in flight, so an unchanged identity keeps its
 *   draft instead of being reset to a spinner (writes are refused separately
 *   while the session refreshes).
 */
export function useResource<T>(
  loader: (signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[],
  options: { scope: string; generation: number },
): Resource<T> {
  const { scope, generation } = options;
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<{
    status: 'loading' | 'ready' | 'error';
    data?: T;
    error?: unknown;
    forScope?: string;
    forGeneration?: number;
  }>({ status: 'loading' });
  const loaderRef = useRef(loader);
  loaderRef.current = loader;
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const generationRef = useRef(generation);
  generationRef.current = generation;

  useEffect(() => {
    const controller = new AbortController();
    const runScope = scopeRef.current;
    const runGeneration = generationRef.current;
    // Same scope: keep showing the current projection while re-reading, so a
    // focus refresh does not unmount a form and discard its draft. New scope:
    // never present the previous identity's data as this one's.
    setState((current) => (current.forScope === runScope ? current : { status: 'loading' }));
    loaderRef.current(controller.signal).then(
      (data) => {
        if (controller.signal.aborted) return;
        if (runGeneration !== generationRef.current) return; // superseded read
        setState({ status: 'ready', data, forScope: runScope, forGeneration: runGeneration });
      },
      (error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        if (runGeneration !== generationRef.current) return; // superseded read
        setState({ status: 'error', error, forScope: runScope, forGeneration: runGeneration });
      },
    );
    return () => controller.abort();
  }, [...deps, attempt, generation]);

  const reload = useCallback(() => setAttempt((value) => value + 1), []);
  // A result that belongs to another scope is not this caller's data: report a
  // load in progress until the projection for the current scope arrives.
  if (state.forScope !== undefined && state.forScope !== scope) {
    return { status: 'loading', reload };
  }
  return { ...state, reload };
}
