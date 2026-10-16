import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/** shadcn/ui's class composer: conditional classes merged Tailwind-aware. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
