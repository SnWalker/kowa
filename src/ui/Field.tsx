import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';
import { Checkbox } from './checkbox';
import { Label } from './label';

/**
 * A form field: the label above its control, both inside one labelling
 * element, so the control keeps the exact accessible name the journeys and the
 * component tests already address it by.
 */
export function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Label className={cn('flex w-full flex-col items-start gap-1 text-sm font-normal select-text', className)}>
      <span className="font-semibold text-foreground">{label}</span>
      {children}
    </Label>
  );
}

/** Two or more fields on one row, wrapping to one column when narrow. */
export function FieldRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('flex flex-wrap items-end gap-x-4 gap-y-3', className)}>{children}</div>;
}

/**
 * A checkbox with its own visible text, associated by id (the library's
 * checkbox is a button, so the text is a real label element pointing at it —
 * clicking the text toggles the control, exactly as the native pair did).
 */
export function CheckboxField({
  id,
  checked,
  disabled,
  onCheckedChange,
  children,
  ariaLabel,
  className,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  onCheckedChange: (checked: boolean) => void;
  children: ReactNode;
  /** An explicit name for the control when the visible text is not the name. */
  ariaLabel?: string;
  className?: string;
}) {
  return (
    <div className={cn('flex min-h-8 items-center gap-2', className)}>
      <Checkbox id={id} aria-label={ariaLabel} checked={checked} disabled={disabled} onCheckedChange={onCheckedChange} />
      <Label htmlFor={id} className="text-sm font-normal">
        {children}
      </Label>
    </div>
  );
}
