import type { ReactNode } from 'react';

export function Page({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="page">
      <h1>{title}</h1>
      {children}
    </div>
  );
}
