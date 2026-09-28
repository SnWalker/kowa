---
name: git-workflow-and-versioning
description: Manage Kowa branches, commits, worktrees, pull requests, conflict recovery, history repair, tags, and releases. Load when a task will inspect or mutate Git state, coordinate parallel repository work, or decide versioning. This skill never grants permission to commit, push, switch branches, merge, delete, or rewrite history.
metadata:
  upstream: addyosmani/agent-skills
  upstream-commit: 2686b620fc1fed2e8f60c704839c766b8594c6b6
  adapted-for: Kowa
---

# Kowa Git Workflow and Versioning

Use Git to make Kowa changes reviewable, reversible, and compatible with the
project's staged delivery model. This skill supplies workflow guidance only.

## Authority and permission boundary

Apply rules in this order:

1. The user's current request and explicit authorization.
2. Repository `AGENTS.md` and the active stage contract.
3. `CONTRIBUTING.md`, when present.
4. This skill.

Loading this skill does not authorize a state-changing Git or GitHub action.
Read-only inspection is allowed when relevant. Commit, push, branch switching,
worktree creation/removal, PR mutation, merge, tagging, release creation, and
history rewriting require authorization from the user or the active workflow.

Never use `git reset --hard`, `git checkout --`, `git clean`, force push, or
history rewriting as routine recovery. Preserve user changes and prefer a
recoverable backup when an explicitly authorized rewrite is unavoidable.

## Start with repository facts

Before choosing a workflow, inspect only what is needed:

```bash
git status --short --branch
git rev-parse HEAD
git branch -vv
git remote -v
```

For GitHub work, also inspect the relevant PR and checks with `gh`. Determine:

- the remote default branch and its current commit;
- whether the worktree contains pre-existing changes;
- whether the current branch already owns an open PR;
- the active Kowa lane and stage;
- whether another session or worktree owns overlapping files.

Stop rather than merge over unknown changes, overlapping ownership, or a
drifting frozen contract.

## Branch strategy

Use short-lived branches based on the latest remote default branch. A Kowa
stage normally uses one branch and one PR. Use multiple branches only when the
stage contract permits isolated, non-overlapping parallel work.

Prefer descriptive team-style names:

```text
feature/backend-s01-identity-workspace
fix/runner-lease-expiry
chore/harden-ci
docs/update-delivery-contract
refactor/workflow-compiler
```

Do not invent a tool-specific prefix such as `codex/` unless repository policy
or the user explicitly requires it. Do not append unrelated work to a branch
whose PR is already under review. After a PR merges, fetch and verify the new
remote default branch before starting the next branch; do not assume a blind
`git pull` is safe when local history may have diverged.

## Commit discipline

Each commit should represent one coherent, independently reviewable change.
Split behavior, refactoring, generated artifacts, dependency changes, and
documentation when they have different reasons or rollback boundaries. Do not
split mechanically by line count; use the smallest unit that stays valid and
testable.

Use Conventional Commit-style subjects when they fit:

```text
feat: add workspace membership authorization
fix: preserve governance directories in clones
test: cover stale task result rejection
docs: record S01 verification evidence
chore: pin GitHub Actions by commit SHA
```

Before committing:

1. Inspect `git diff` and `git diff --cached`.
2. Stage only intended paths; never re-stage or discard unknown user changes.
3. Check the staged diff for credentials, tokens, private keys, personal data,
   generated output, and files excluded by `.gitignore`.
4. Run the active stage's required tests and gates.
5. Confirm the commit message explains the intent.

A passing test does not authorize a commit or push.

## Pull requests and CI

Create or update a PR only when authorized. The PR should name the stage or
concern, summarize the contract being satisfied, list verification evidence,
and disclose known risks or intentionally deferred work.

For a Kowa stage that requires remote verification:

1. Run local required gates.
2. Commit and push the authorized branch.
3. Read the actual GitHub check results and run/job identifiers.
4. Treat a remote failure as evidence, not noise. Diagnose its producer,
   transition, and consumer; reopen the responsible completed stage when the
   repository state machine requires it.
5. Mark the stage `DONE` only after its necessary local and remote evidence is
   recorded. Push the final state and verify checks on that final commit.

Never infer success from a green earlier commit when the PR head has changed.
Do not merge a PR unless the user explicitly authorizes it; Kowa normally
leaves the final merge to a human under repository Rulesets.

## Worktrees and parallel work

Prefer the Codex managed-worktree capability when available. Inspect attached
worktrees before creating another one, assign non-overlapping write scopes, and
use the exact returned workspace path. Do not delete a managed checkout with
shell commands; archive it through the managed worktree operation when the
work is genuinely complete or abandoned.

Raw `git worktree` commands are appropriate only when the environment has no
managed lifecycle and the user authorized the operation. Never let two active
worktrees own the same branch.

## Conflicts and history repair

Use the repository's merge-conflict workflow for an in-progress merge or
rebase. Resolve from the documented contract and tests, not by choosing one
side wholesale. Preserve evidence of what changed and rerun affected gates.

When history cleanup is explicitly authorized:

- resolve exact branches and remote state first;
- create a recoverable bundle or equivalent backup outside refs that may be
  pushed;
- remove sensitive or oversized paths from every reachable commit, not only
  from the tip;
- reconnect rewritten work to the real remote base;
- verify ignored paths and large blobs are absent from the objects that will be
  pushed;
- use `--force-with-lease` rather than an unconstrained force push when a
  remote rewrite is actually required.

Do not rewrite a shared default branch merely to improve commit aesthetics.

## Versioning and releases

Use semantic versions only after Kowa has a consumer-facing release contract:

- `MAJOR`: incompatible contract or behavior change;
- `MINOR`: backward-compatible capability;
- `PATCH`: backward-compatible fix.

Tags and GitHub releases are external mutations. Create them only with explicit
authorization, from an accepted commit, after release gates pass. Keep a
human-readable changelog focused on user and operator impact rather than a raw
commit dump.

## Handoff checklist

Report:

- branch, base commit, HEAD, and PR URL when applicable;
- commits created and why they are atomic;
- local and remote verification results;
- files intentionally not touched;
- remaining risks, required human actions, and whether merge is still pending;
- the exact next-stage or release boundary.
