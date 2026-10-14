create table work_item (
    work_item_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    title text not null,
    target_repository_id text not null,
    requirement jsonb not null,
    requirement_digest text not null,
    acceptance jsonb not null,
    acceptance_digest text not null,
    state text not null,
    version bigint not null,
    current_run_id text references workflow_run (workflow_run_id) on delete restrict,
    created_by_github_user_id text not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    closed_at timestamptz,
    canceled_at timestamptz,
    constraint work_item_id_not_empty check (length(work_item_id) > 0),
    constraint work_item_title_not_empty check (length(btrim(title)) > 0),
    constraint work_item_target_repository_decimal check (target_repository_id ~ '^[1-9][0-9]*$'),
    constraint work_item_requirement_digest_sha256 check (requirement_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint work_item_acceptance_digest_sha256 check (acceptance_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint work_item_state_valid check (state in ('OPEN', 'CLOSED', 'CANCELED')),
    constraint work_item_version_positive check (version > 0),
    constraint work_item_created_by_decimal check (created_by_github_user_id ~ '^[1-9][0-9]*$'),
    constraint work_item_closed_state check ((state = 'CLOSED') = (closed_at is not null)),
    constraint work_item_canceled_state check ((state = 'CANCELED') = (canceled_at is not null))
);

create index work_item_workspace_id_idx on work_item (workspace_id);
create index work_item_current_run_id_idx on work_item (current_run_id);

create table work_item_run_binding (
    work_item_id text not null references work_item (work_item_id) on delete cascade,
    workflow_run_id text not null references workflow_run (workflow_run_id) on delete restrict,
    kind text not null,
    source_run_id text references workflow_run (workflow_run_id) on delete restrict,
    reason text,
    merged_baseline_oid text,
    bound_by_github_user_id text not null,
    bound_at timestamptz not null,
    primary key (work_item_id, workflow_run_id),
    constraint work_item_run_binding_kind_valid check (kind in ('initial', 'repair')),
    constraint work_item_run_binding_source check ((kind = 'repair') = (source_run_id is not null)),
    constraint work_item_run_binding_baseline_oid check (merged_baseline_oid is null or merged_baseline_oid ~ '^[0-9a-f]{40}$'),
    constraint work_item_run_binding_bound_by_decimal check (bound_by_github_user_id ~ '^[1-9][0-9]*$')
);

create index work_item_run_binding_run_idx on work_item_run_binding (workflow_run_id);
create index work_item_run_binding_source_idx on work_item_run_binding (source_run_id);

create table human_task (
    human_task_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    work_item_id text not null references work_item (work_item_id) on delete restrict,
    workflow_run_id text references workflow_run (workflow_run_id) on delete restrict,
    node_run_id text references node_run (node_run_id) on delete restrict,
    request_version bigint not null,
    request jsonb not null,
    request_digest text not null,
    completion_mode text not null,
    kind text not null,
    object_kind text not null,
    object_id text not null,
    object_version text not null,
    object_digest text,
    state text not null,
    version bigint not null,
    candidates jsonb not null,
    roles jsonb not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    resolved_at timestamptz,
    resolved_by_github_user_id text,
    resolution_kind text,
    external_fact_id text,
    expired_reason text,
    constraint human_task_id_not_empty check (length(human_task_id) > 0),
    constraint human_task_request_version_positive check (request_version > 0),
    constraint human_task_request_digest_sha256 check (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint human_task_completion_mode_valid check (completion_mode in ('kowa_answer', 'external_fact')),
    constraint human_task_kind_pattern check (kind ~ '^[a-z][a-z0-9_]*$'),
    constraint human_task_object_kind_pattern check (object_kind ~ '^[a-z][a-z0-9_]*$'),
    constraint human_task_object_version_not_empty check (length(object_version) > 0),
    constraint human_task_state_valid check (state in ('OPEN', 'RESOLVED', 'EXPIRED', 'CANCELED')),
    constraint human_task_version_positive check (version > 0),
    constraint human_task_resolved_state check (
        (state = 'RESOLVED') = (resolved_at is not null and resolved_by_github_user_id is not null)
    ),
    constraint human_task_expired_reason check (state <> 'EXPIRED' or expired_reason is not null)
);

create index human_task_workspace_id_idx on human_task (workspace_id);
create index human_task_work_item_id_idx on human_task (work_item_id);
create index human_task_open_idx on human_task (workspace_id, state) where state = 'OPEN';
create index human_task_object_idx on human_task (workspace_id, object_kind, object_id, object_version) where state = 'OPEN';

create table human_response (
    human_response_id text primary key,
    human_task_id text not null unique references human_task (human_task_id) on delete restrict,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    request_version bigint not null,
    object_kind text not null,
    object_id text not null,
    object_version text not null,
    actor_github_user_id text not null,
    decision text not null,
    payload jsonb not null,
    payload_digest text not null,
    created_at timestamptz not null,
    constraint human_response_actor_decimal check (actor_github_user_id ~ '^[1-9][0-9]*$'),
    constraint human_response_decision_valid check (decision in ('approved', 'rejected', 'submitted')),
    constraint human_response_payload_digest_sha256 check (payload_digest ~ '^sha256:[0-9a-f]{64}$')
);

create index human_response_workspace_idx on human_response (workspace_id);

create table human_task_continuation (
    continuation_id text primary key,
    human_response_id text not null unique references human_response (human_response_id) on delete restrict,
    human_task_id text not null references human_task (human_task_id) on delete restrict,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    workflow_run_id text not null references workflow_run (workflow_run_id) on delete restrict,
    node_run_id text not null,
    intent text not null,
    state text not null,
    created_at timestamptz not null,
    consumed_at timestamptz,
    consumed_by_github_user_id text,
    constraint human_task_continuation_state_valid check (state in ('PENDING', 'CONSUMED')),
    constraint human_task_continuation_consumed check (
        (state = 'CONSUMED') = (consumed_at is not null and consumed_by_github_user_id is not null)
    )
);

create index human_task_continuation_pending_idx on human_task_continuation (workspace_id, workflow_run_id) where state = 'PENDING';

create table external_operation (
    operation_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    work_item_id text not null references work_item (work_item_id) on delete restrict,
    workflow_run_id text not null references workflow_run (workflow_run_id) on delete restrict,
    node_run_id text,
    task_id text,
    kind text not null,
    state text not null,
    repository_id text not null,
    ref text not null,
    base_ref text,
    commit_oid text not null,
    expected_github_user_id text not null,
    requested_by_github_user_id text not null,
    request jsonb not null,
    request_digest text not null,
    request_key text not null,
    result jsonb,
    unknown_reason text,
    version bigint not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    settled_at timestamptz,
    constraint external_operation_kind_valid check (kind in ('verify_change', 'publish_pr')),
    constraint external_operation_state_valid check (
        state in ('PENDING', 'ATTEMPTED', 'SUCCEEDED', 'FAILED', 'UNKNOWN')
    ),
    constraint external_operation_repository_decimal check (repository_id ~ '^[1-9][0-9]*$'),
    constraint external_operation_commit_oid check (commit_oid ~ '^[0-9a-f]{40}$'),
    constraint external_operation_expected_identity_decimal check (expected_github_user_id ~ '^[1-9][0-9]*$'),
    constraint external_operation_requested_by_decimal check (requested_by_github_user_id ~ '^[1-9][0-9]*$'),
    constraint external_operation_version_positive check (version > 0),
    constraint external_operation_settled check ((state in ('SUCCEEDED', 'FAILED')) = (settled_at is not null))
);

create index external_operation_work_item_idx on external_operation (workspace_id, work_item_id);
create index external_operation_request_idx on external_operation (workspace_id, kind, repository_id, ref, commit_oid);

create table github_observation (
    observation_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    repository_id text not null,
    subject_kind text not null,
    subject_key text not null,
    observed_at timestamptz not null,
    actor_github_user_id text,
    unknown boolean not null,
    unknown_reason text,
    fact jsonb,
    constraint github_observation_repository_decimal check (repository_id ~ '^[1-9][0-9]*$'),
    constraint github_observation_subject_kind_valid check (
        subject_kind in ('pull_request', 'pull_request_review', 'pull_request_checks', 'branch_head')
    ),
    constraint github_observation_unknown check (unknown or fact is not null)
);

create index github_observation_latest_idx on github_observation (workspace_id, repository_id, subject_kind, subject_key, observed_at desc);

create table verification_evidence (
    verification_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    work_item_id text not null references work_item (work_item_id) on delete restrict,
    workflow_run_id text,
    node_run_id text,
    kind text not null,
    subject_kind text not null,
    repository_id text,
    commit_oid text,
    artifact_id text,
    artifact_digest text,
    configuration_version text not null,
    producer_kind text not null,
    producer_id text not null,
    source text not null,
    result text not null,
    report_artifact_id text,
    report_digest text,
    observed_at timestamptz not null,
    created_at timestamptz not null,
    constraint verification_evidence_kind_valid check (
        kind in ('required_tests', 'independent_review', 'security_review', 'post_merge_integration',
                 'knowledge_suggestion', 'offline_evaluation')
    ),
    constraint verification_evidence_subject_valid check (
        (subject_kind = 'commit' and repository_id is not null and commit_oid is not null and artifact_id is null)
        or (subject_kind = 'artifact' and artifact_id is not null and artifact_digest is not null
            and repository_id is null and commit_oid is null)
    ),
    constraint verification_evidence_commit_oid check (commit_oid is null or commit_oid ~ '^[0-9a-f]{40}$'),
    constraint verification_evidence_repository_decimal check (repository_id is null or repository_id ~ '^[1-9][0-9]*$'),
    constraint verification_evidence_artifact_digest check (
        artifact_digest is null or artifact_digest ~ '^sha256:[0-9a-f]{64}$'
    ),
    constraint verification_evidence_report_digest check (report_digest is null or report_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint verification_evidence_producer_kind_valid check (producer_kind in ('task', 'external_ci', 'control_plane')),
    constraint verification_evidence_source_valid check (source in ('real', 'simulated')),
    constraint verification_evidence_result_valid check (result in ('passed', 'failed'))
);

create index verification_evidence_work_item_idx on verification_evidence (workspace_id, work_item_id);

create table closure_evidence (
    closure_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    work_item_id text not null unique references work_item (work_item_id) on delete restrict,
    workflow_run_id text not null references workflow_run (workflow_run_id) on delete restrict,
    definition_name text not null,
    definition_version integer not null,
    definition_digest text not null,
    policy_version text not null,
    requirement_digest text not null,
    knowledge_snapshot_id text,
    knowledge_binding_event_id text,
    merge_repository_id text not null,
    merge_commit_oid text not null,
    pull_request_number text not null,
    merge_observation_id text not null references github_observation (observation_id) on delete restrict,
    review_observation_id text not null references github_observation (observation_id) on delete restrict,
    review_head_oid text not null,
    reviewer_github_user_id text not null,
    required_test_ids jsonb not null,
    independent_review_id text not null references verification_evidence (verification_id) on delete restrict,
    post_merge_verification_id text not null references verification_evidence (verification_id) on delete restrict,
    checklist_digest text not null,
    created_by_github_user_id text not null,
    created_at timestamptz not null,
    constraint closure_evidence_definition_digest_sha256 check (definition_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint closure_evidence_requirement_digest_sha256 check (requirement_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint closure_evidence_merge_commit_oid check (merge_commit_oid ~ '^[0-9a-f]{40}$'),
    constraint closure_evidence_review_head_oid check (review_head_oid ~ '^[0-9a-f]{40}$'),
    constraint closure_evidence_reviewer_decimal check (reviewer_github_user_id ~ '^[1-9][0-9]*$'),
    constraint closure_evidence_checklist_digest_sha256 check (checklist_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint closure_evidence_created_by_decimal check (created_by_github_user_id ~ '^[1-9][0-9]*$')
);

create index closure_evidence_run_idx on closure_evidence (workflow_run_id);
