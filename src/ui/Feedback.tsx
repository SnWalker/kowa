import type { ReactNode } from 'react';

export function LoadingState() {
  return <p className="feedback" role="status">正在加载，请稍候…</p>;
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <section className="feedback empty-state">
      <span className="empty-mark" aria-hidden="true">○</span>
      <h2>{title}</h2>
      {children}
    </section>
  );
}
