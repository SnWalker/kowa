import { ChevronDown } from 'lucide-react';
import type * as React from 'react';
import { cn } from '@/lib/utils';

/**
 * The theme's select control.
 *
 * It is deliberately the platform `<select>` styled with the same tokens,
 * border, height and chevron as the library's richer controls: the frozen
 * journey contracts drive these two selects (`外观` and the member role) with
 * `selectOption`, `toHaveValue` and Tab-focus assertions, which only exist for
 * a native control. Replacing them with a listbox would rewrite those frozen
 * assertions into a different interaction model, so the native element stays
 * and takes the theme's surface instead. Nothing else in the product uses it.
 */
export function Select({ className, children, ...props }: React.ComponentProps<'select'>) {
  return (
    <span className="relative inline-flex w-full min-w-0 items-center">
      <select
        data-slot="select"
        className={cn(
          'h-8 w-full min-w-0 cursor-pointer appearance-none rounded-md border border-input bg-card pl-3 pr-8 text-sm text-foreground',
          'focus-visible:outline-2 focus-visible:outline-ring focus-visible:-outline-offset-2',
          'disabled:cursor-not-allowed disabled:opacity-60',
          className,
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown aria-hidden="true" className="pointer-events-none absolute right-2 size-4 text-muted-foreground" />
    </span>
  );
}
