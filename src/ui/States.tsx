import { CircleAlert, Info, TriangleAlert } from 'lucide-react';
import { useId, type ReactNode } from 'react';
import { Alert, AlertDescription, AlertTitle } from './alert';
import { Badge as BadgePrimitive } from './badge';
import { Card, CardContent, CardHeader, CardTitle } from './card';
import { describeError } from './messages';

type NoticeTone = 'error' | 'info' | 'warning';

/**
 * A notice is a service fact, never a decoration: the error tone is announced
 * as an alert, everything else as a status. The title stays a heading so the
 * document outline keeps naming each notice, and the tone is carried by the
 * icon and the tinted surface rather than by colour alone.
 */
const NOTICE: Record<NoticeTone, { variant: 'danger' | 'info' | 'warning'; role: 'alert' | 'status'; Icon: typeof Info }> = {
  error: { variant: 'danger', role: 'alert', Icon: CircleAlert },
  warning: { variant: 'warning', role: 'status', Icon: TriangleAlert },
  info: { variant: 'info', role: 'status', Icon: Info },
};

export function Notice({ tone, title, children }: { tone: NoticeTone; title: string; children?: ReactNode }) {
  const { variant, role, Icon } = NOTICE[tone];
  return (
    <Alert variant={variant} role={role} className="my-3">
      <Icon aria-hidden="true" />
      <AlertTitle className="line-clamp-none">
        <h2 className="text-sm font-semibold text-foreground">{title}</h2>
      </AlertTitle>
      {children ? <AlertDescription className="text-foreground">{children}</AlertDescription> : null}
    </Alert>
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
  const variant = { ok: 'success', warn: 'attention', bad: 'danger', unknown: 'neutral' }[tone] as
    | 'success'
    | 'attention'
    | 'danger'
    | 'neutral';
  return <BadgePrimitive variant={variant}>{children}</BadgePrimitive>;
}

/** Facts about one object, as a bordered list of label/value rows. */
export function DataList({ items }: { items: { term: string; value: ReactNode }[] }) {
  return (
    <dl className="overflow-hidden rounded-md border border-border-muted">
      {items.map((item) => (
        <div
          key={item.term}
          className="flex flex-col gap-0.5 border-t border-border-muted px-3 py-2 first:border-t-0 sm:flex-row sm:gap-4"
        >
          <dt className="shrink-0 text-xs text-muted-foreground sm:w-48">{item.term}</dt>
          <dd className="min-w-0 break-anywhere text-sm">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * One titled region of a page: GitHub's Box — a header strip on the muted
 * surface and a body, both inside the component library's card.
 *
 * The region keeps an accessible name (from its own heading) because the
 * journeys navigate by region, and a stable `id` lets a page link to it.
 */
export function Section({
  title,
  id,
  actions,
  children,
}: {
  title: string;
  id?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const generatedId = useId();
  const headingId = id ?? generatedId;
  return (
    <Card role="region" aria-labelledby={headingId} className="mb-4 gap-0 overflow-hidden rounded-md py-0 shadow-none">
      <CardHeader className="flex flex-row items-center justify-between gap-2 bg-muted px-4 py-2">
        <CardTitle>
          <h2 id={headingId} className="text-lg font-semibold text-foreground">
            {title}
          </h2>
        </CardTitle>
        {actions}
      </CardHeader>
      <CardContent className="border-t border-border px-4 py-3">{children}</CardContent>
    </Card>
  );
}
