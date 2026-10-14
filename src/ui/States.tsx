import type { ReactNode } from 'react';
import { describeError } from './messages';

export function Notice({ tone, title, children }: { tone: 'error' | 'info' | 'warning'; title: string; children?: ReactNode }) {
  return (
    <div className={`notice notice-${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      <h2 className="notice-title">{title}</h2>
      {children}
    </div>
  );
}

export function ErrorNotice({ error, title = '无法完成操作' }: { error: unknown; title?: string }) {
  return (
    <Notice tone="error" title={title}>
      <p>{describeError(error)}</p>
    </Notice>
  );
}

export type BadgeTone = 'ok' | 'warn' | 'bad' | 'unknown';

export function Badge({ tone, children }: { tone: BadgeTone; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export function DataList({ items }: { items: { term: string; value: ReactNode }[] }) {
  return (
    <dl className="data-list">
      {items.map((item) => (
        <div className="data-row" key={item.term}>
          <dt>{item.term}</dt>
          <dd>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Section({ title, children, id }: { title: string; id?: string; children: ReactNode }) {
  return (
    <section className="panel" aria-labelledby={id}>
      <h2 id={id}>{title}</h2>
      {children}
    </section>
  );
}
