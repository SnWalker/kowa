# Kowa project skills

Repository-scoped Agent Skills used during Kowa implementation. Installed on 2026-09-28; source instructions never override the repository `AGENTS.md`, stage contracts, permission boundaries, or required verification.

| Source | Commit | Installed selection |
| :--- | :--- | :--- |
| `samber/cc-skills-golang` | `19a0626ae8565d27a7b7bdf59d8d99d94d7e284c` | 21 of 46: `golang-how-to`, `golang-code-style`, `golang-naming`, `golang-structs-interfaces`, `golang-error-handling`, `golang-context`, `golang-concurrency`, `golang-data-structures`, `golang-design-patterns`, `golang-safety`, `golang-security`, `golang-testing`, `golang-database`, `golang-troubleshooting`, `golang-observability`, `golang-continuous-integration`, `golang-lint`, `golang-dependency-management`, `golang-dependency-injection`, `golang-uber-fx`, `golang-project-layout` |
| `vercel-labs/agent-skills` | `063bee94c3f4df8453406c830b0a7df0f2860278` | `react-best-practices`, `composition-patterns` |
| `supabase/agent-skills` | `551274ed2fe97c8fea1325f7ceb05803a542f8df` | `supabase-postgres-best-practices` |
| `anthropics/skills` | `33375500bcea98d610eb30ce10ac4e59b89c390d` | `webapp-testing` |
| `addyosmani/agent-skills` | `2686b620fc1fed2e8f60c704839c766b8594c6b6` | `git-workflow-and-versioning` (Kowa-adapted) |
| `mattpocock/skills` (MIT) | `d81f3a183412e71a5b1e84ca21bc1a35eea03a60` | `engineering/tdd`, `engineering/diagnosing-bugs` (verbatim, no local patches) |

## Discovery

- Codex reads this directory directly (`.agents/skills`).
- Claude Code reads `.claude/skills`, which is a relative symlink to `../.agents/skills`, so both tools see the same single copy. Do not create a second copy.
- Claude Code does **not** read `.agents/skills` on its own (verified: without `.claude/skills` a probe in this repo sees none of them). Through the symlink it lists 11 skills at session start; the other 17 Go skills declare `paths: ["**/*.go"]` and are activated only after a `.go` file is read in the session (verified by invoking `golang-testing` after reading `internal/execution/types.go`). So a missing `golang-*` entry in the initial skill list is expected, not an install failure.
- Kowa records already cite `tdd` and `diagnosing-bugs`; they were previously resolved only from a user-global skills manager, which is not reproducible from a clone. They are now pinned here.

## Safety and applicability

- Skills are guidance, not authority. Kowa's formal contracts and stage plans remain authoritative.
- Only skills relevant to code Kowa actually contains are installed (Go with `pgx` + `uber/fx` + standard library, React 18 + Vite + Playwright, GitHub Actions, PostgreSQL). Skills for libraries Kowa does not use (samber/*, spf13/*, testify, wire, dig, gRPC, GraphQL, Swagger) and low-value generic ones (benchmark, performance, cli, gopls, pkg-go-dev, modernize, refactoring, documentation, popular-libraries, stay-updated) were removed on 2026-10-01; install one again only with the dependency or stage that needs it. Installed skills still contain cross-references to removed ones; treat those as inert.
- Kowa currently uses React 18 without Next.js. Ignore React 19-only and Next.js server rules unless an approved design change updates that baseline.
- `webapp-testing` includes Python Playwright examples and a server-process helper; review commands and targets before execution.
- GitHub API does not report a license for `addyosmani/agent-skills`; verify upstream licensing before redistributing or commercially reusing its skill text outside this repository.
- `tdd` asks to agree test seams with the user before writing tests and mentions the `codebase-design` and `code-review` skills, which are not installed here. In Kowa the stage contract's test section is the agreed seam list; do not infer that the missing skills are available.
- `tdd` and `diagnosing-bugs` mention an optional `GLOSSARY.md` (older upstream: `CONTEXT.md`); Kowa has neither, so ignore that sentence.
- `diagnosing-bugs/scripts/hitl-loop.template.sh` is an interactive human-in-the-loop template: copy and edit it before use and never place secrets in captured values.
- Update skills deliberately by reviewing the upstream diff and recording the new commit here.

## Local patches

- `golang-how-to/SKILL.md` carries a Kowa note that overrides its routes to removed skills, forbids adopting their libraries, and forbids Configure mode (which would edit `AGENTS.md`).
- Fixed `golang-troubleshooting/references/methodology.md` to link back to `../SKILL.md`; upstream snapshot used the broken `./SKILL.md` path.
- Fixed three compiled links in `react-best-practices/AGENTS.md` to include the `rules/` directory.
- Normalized trailing whitespace and final newlines across installed text files so repository diff checks remain usable.
- Adapted `git-workflow-and-versioning` to Kowa's stage state machine, managed-worktree lifecycle, enterprise branch names, explicit mutation authorization, remote CI evidence, and history-repair safety; removed generic tutorials, JavaScript-only hooks, fixed line-count targets, and routine destructive recovery advice.
