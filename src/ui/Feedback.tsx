import { Inbox, LoaderCircle } from 'lucide-react';
import type { ReactNode } from 'react';

/**
 * Loading and empty are states of the page, not of a widget: they keep the
 * live status (or the empty screen's own heading) and read as one quiet block
 * instead of a card, because nothing is known yet or nothing is there.
 */
export function LoadingState() {
  return (
    <div role="status" className="flex items-center gap-2 rounded-md border border-border-muted bg-card px-4 py-3 text-sm text-muted-foreground">
      <LoaderCircle aria-hidden="true" className="size-4 animate-spin" />
      正在加载，请稍候…
    </div>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <section className="rounded-md border border-dashed border-border px-4 py-10 text-center">
      <Inbox aria-hidden="true" className="mx-auto mb-3 size-6 text-muted-foreground" />
      <h2 className="text-lg font-semibold text-foreground">{title}</h2>
      {children ? <div className="mt-1 text-sm text-muted-foreground">{children}</div> : null}
    </section>
  );
}
