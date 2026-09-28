# Kowa project skills

Repository-scoped Agent Skills used during Kowa implementation. Installed on 2026-09-28; source instructions never override the repository `AGENTS.md`, stage contracts, permission boundaries, or required verification.

| Source | Commit | Installed selection |
| :--- | :--- | :--- |
| `samber/cc-skills-golang` | `19a0626ae8565d27a7b7bdf59d8d99d94d7e284c` | All 46 skills under `skills/` |
| `vercel-labs/agent-skills` | `063bee94c3f4df8453406c830b0a7df0f2860278` | `react-best-practices`, `composition-patterns` |
| `supabase/agent-skills` | `551274ed2fe97c8fea1325f7ceb05803a542f8df` | `supabase-postgres-best-practices` |
| `anthropics/skills` | `33375500bcea98d610eb30ce10ac4e59b89c390d` | `webapp-testing` |

## Safety and applicability

- Skills are guidance, not authority. Kowa's formal contracts and stage plans remain authoritative.
- Library-specific Go skills apply only when the corresponding dependency is actually selected; installation does not approve Cobra, Viper, Wire, Dig, GraphQL, gRPC, or Samber libraries.
- Kowa currently uses React 18 without Next.js. Ignore React 19-only and Next.js server rules unless an approved design change updates that baseline.
- `golang-benchmark` contains optional Linux host-tuning examples using `sudo`; do not run them without explicit user authorization and an isolated benchmark host.
- `webapp-testing` includes Python Playwright examples and a server-process helper; review commands and targets before execution.
- Update skills deliberately by reviewing the upstream diff and recording the new commit here.

## Local patches

- Fixed `golang-troubleshooting/references/methodology.md` to link back to `../SKILL.md`; upstream snapshot used the broken `./SKILL.md` path.
- Fixed three compiled links in `react-best-practices/AGENTS.md` to include the `rules/` directory.
- Fixed four `golang-documentation` template links to point to `../assets/templates/`.
- Normalized trailing whitespace and final newlines across installed text files so repository diff checks remain usable.
