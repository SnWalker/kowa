# kowa.workflow-execution.v1

Owner: `backend:S02`. Workflow produces immutable definitions and compiled plans; Execution produces runs, node rounds, Task leases, Runtime registrations, progress observations, and accepted results. Consumers are Web run views, the control-plane scheduler, future Runner/Worker implementations, HumanTask continuation, and audit.

## Frozen responsibilities

| Message | Producer | Consumer | Authoritative rule |
| :--- | :--- | :--- | :--- |
| `WorkflowDefinition` | built-in definition author | Workflow compiler | unknown fields, duplicate nodes, missing dependencies, ordinary cycles, illegal bindings/conditions, and unavailable exact capability versions reject publication |
| `CompiledPlan` | Workflow compiler | run admission and scheduler | name + version identifies one immutable digest; an existing run never reloads a newer definition/catalog |
| `RuntimeRegistration` | authenticated Runner process | Execution dispatcher | Runtime epoch, exact capabilities, Provider identity/version/authentication, and machine GitHub user are separate facts |
| `TaskLease` / `TaskSpec` | Execution dispatcher | Runner/Worker | Task, NodeRun, attempt, input digest, Provider rule, machine GitHub identity, Git scopes, lease, and fencing token are all explicit |
| `ProgressEvent` | Runner | Execution observation | monotonic observation only; it never decides a node or advances the DAG |
| `TaskResult` | Runner | Execution result acceptance | current Task + lease + fencing + current node round + result digest are checked atomically; same digest is idempotent and a different digest conflicts |
| `RunView` / `NodeView` | control-plane projection | Web and audit | Task completion, node decision, business verdict, and run status remain distinct |

Every envelope uses `schemaVersion: "kowa.workflow-execution.v1"`, a known `kind`, and strict unknown-field rejection. IDs are opaque Kowa identities; GitHub user and repository IDs are decimal strings. Digests include the `sha256:` prefix. Timestamps are UTC RFC 3339 values.

## Attempt and state rules

- Duplicate delivery of one execution reuses the same Task. Retry creates a new Task in the same NodeRun with the same input digest and `previousTaskId`.
- Human continuation creates a new Task in the same NodeRun, links the accepted `humanResponseId`, and may carry a `resumeRef`; Provider session state is optional and never substitutes for the persisted response.
- Business revision/Rerun creates a new NodeRun iteration and new Task. The fixed automatic revision policy allows the initial round plus at most three automatic revisions; exhaustion is `POLICY_BLOCKED`, not success.
- `COMPLETED` means execution ended. A `needs_revision` verdict is still a decided node round but does not satisfy a release gate.
- A lease is current only when Task ID, lease ID, fencing token, Runtime epoch, expiry, and current NodeRun iteration match. Old or late reports are retained as audit/external-fact input but cannot replace current facts.
- Cancellation stops new dispatch and first enters `CANCELING` while leased work remains. Lease expiry isolates writes but cannot claim an external GitHub side effect stopped.

## Provider and Git authority

`providerSelection` is exact for v1; a Runtime cannot silently substitute another Provider or version. `gitExecutionUserId` is the Runner machine's verified GitHub user, not the Web actor. `gitScopes` describes Kowa's authorized repository role, ref, and operation; it is a logical acceptance boundary, not a claim that the host's personal `gh` credential is technically sandboxed. External writes outside scope may already have happened and require later evidence/reconciliation.

## Errors and acceptance mapping

| Behavior | Error/category | Acceptance |
| :--- | :--- | :--- |
| invalid definition, cycle, binding, condition, or result shape | `VALIDATION_ERROR` | B01—B04 |
| unknown definition or capability; Runtime/Provider unavailable | `RESOURCE_UNAVAILABLE` | B05, B07, C07 |
| same definition identity with changed digest; same report identity with changed digest | `VERSION_CONFLICT` | B05, C03 |
| old lease, epoch, fencing token, node iteration, or input | `STALE_RESULT` | A12—A14 |
| automatic revision budget exhausted or mandatory gate not satisfied | `POLICY_BLOCKED` | A08—A10, B08—B09 |
| external Git side effect cannot yet be confirmed | `EXTERNAL_UNKNOWN` | A14, C06 |

Positive and negative protocol examples are under `examples/`. S02 validates the wire and isolated PostgreSQL behavior without invoking a real Provider or GitHub write. HTTP routes and long-poll timing are intentionally left to the Runner transport stage; changing transport must preserve this contract.
