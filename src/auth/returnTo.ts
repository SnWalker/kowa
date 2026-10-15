/**
 * Only unencoded absolute UI paths the owner contract accepts are echoed back:
 * ASCII letters, digits, `_`, `-`, `/`, no `//`, no `/api/` and no dot segments.
 */
export function safeReturnTo(value: string): string {
  const path = value.split(/[?#]/)[0] ?? '';
  const allowed = /^\/[A-Za-z0-9/_-]*$/.test(path);
  const hasDoubleSlash = path.includes('//');
  const hasDotSegment = /\/\.\.?(\/|$)/.test(path);
  const isApiPath = path === '/api' || path.startsWith('/api/');
  return allowed && !hasDoubleSlash && !hasDotSegment && !isApiPath ? path : '/workspaces';
}
