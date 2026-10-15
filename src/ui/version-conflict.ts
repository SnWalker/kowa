/**
 * Presentation state for a refused versioned write: the baseline the operator
 * submitted against and the version the server really holds. Keeping both is
 * what lets the conflict notice tell the truth about what is being abandoned
 * instead of showing only the server's version.
 */
export type VersionConflict = { submittedVersion: number; serverVersion: number };
