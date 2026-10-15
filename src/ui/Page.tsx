import type { ReactNode } from 'react';

/**
 * The page frame every route renders inside: GitHub's page header pattern —
 * the title, an optional line of machine facts under it, and the page's own
 * actions on the right, all separated from the content by a hairline.
 *
 * `meta` carries identifiers, versions and timestamps (rendered monospaced);
 * explanatory prose belongs in the content, not in the header.
 */
export function Page({
  title,
  meta,
  actions,
  children,
}: {
  title: string;
  meta?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mx-auto w-full max-w-[80rem] break-anywhere">
      <header className="mb-5 flex flex-wrap items-start justify-between gap-x-4 gap-y-2 border-b border-border-muted pb-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold text-foreground">{title}</h1>
          {meta ? <p className="mt-1 text-xs text-muted-foreground">{meta}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </header>
      {children}
    </div>
  );
}
