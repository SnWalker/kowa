# kowa.observability.v1

Owner: backend:S07. Layer: HTTP. Consumer: the Web operations view and the S08 real-integration acceptance, which freezes the watermarks before and after a run.

This contract publishes two read-only projections and nothing else:

| operation | shape |
| :--- | :--- |
| `observability.health` | The process and dependency facts: build identity, migration version, database reachability, evaluation backend availability with its pending spool, and the watermarks this process observed itself. |
| `observability.watermarks` | The per-work-item "freeze the watermarks before you start" statement: definition identity, execution-configuration version, run counts, the contactability of each authorized runner, the business-progress watermark, the last successful and last attempted GitHub observation per subject, and the artifact namespace. |

Rules the shape enforces rather than describes:

- **A heartbeat is not progress.** Contactability and business progress are two watermarks with two producers. A runtime that answers heartbeats and a run that stopped advancing are reported separately, and the progress watermark is never derived from Runner contact.
- **An old observation is not a new fact.** An observation watermark moves its success time only on a successful read; a failed read advances the attempt time and degrades the freshness to `stale` or `unknown` immediately. `negative-semantic-stale-masquerade.json` is the case the contract refuses.
- **No credential is published.** Every free-text field is `SafeText`, which the schema refuses when it still matches a credential shape; the surface also never carries a token, a private key, a complete user input or a prompt body.
- **No business verdict.** Neither projection decides whether a node, a run or a delivery succeeded. Run state is reported as the execution authority published it.

The manifest pins `schema.json` by SHA-256; a consumer must pin both the manifest file digest and the `schemaSha256` it carries, and must recompute them from the merged manifest rather than copying a value from a stage document.
