import type { ReactNode } from 'react';

/**
 * One validated GitHub link. The href only ever comes from
 * `githubRepositorySlug` plus an encoded number or oid, so the anchor can never
 * carry another origin or a script-bearing scheme. It opens in a new tab and
 * says so, and it carries the destination kind in its accessible name so a
 * screen reader is not left with a bare number.
 */
export function GitHubLink({ href, children, label }: { href: string; children: ReactNode; label: string }) {
  return (
    <a
      className="text-foreground underline underline-offset-2 hover:text-accent-foreground"
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      aria-label={`${label}（在 GitHub 新标签页打开）`}
    >
      {children}
    </a>
  );
}
