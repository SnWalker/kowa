create table workflow_definition (
    name text not null,
    version integer not null,
    digest text not null,
    definition jsonb not null,
    compiled_plan jsonb not null,
    created_at timestamptz not null,
    primary key (name, version),
    constraint workflow_definition_name_not_empty check (length(name) > 0),
    constraint workflow_definition_version_positive check (version > 0),
    constraint workflow_definition_digest_sha256 check (digest ~ '^sha256:[0-9a-f]{64}$')
);

create table workflow_run (
    workflow_run_id text primary key,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    work_item_id text not null,
    started_by_github_user_id text not null,
    git_execution_user_id text,
    definition_name text not null,
    definition_version integer not null,
    definition_digest text not null,
    state text not null,
    version bigint not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    foreign key (definition_name, definition_version)
        references workflow_definition (name, version) on delete restrict,
    constraint workflow_run_id_not_empty check (length(workflow_run_id) > 0),
    constraint workflow_run_work_item_id_not_empty check (length(work_item_id) > 0),
    constraint workflow_run_started_by_decimal check (started_by_github_user_id ~ '^[1-9][0-9]*$'),
    constraint workflow_run_git_execution_user_decimal check (
        git_execution_user_id is null or git_execution_user_id ~ '^[1-9][0-9]*$'
    ),
    constraint workflow_run_definition_digest_sha256 check (definition_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint workflow_run_state_valid check (
        state in ('ACTIVE', 'PAUSED', 'CANCELING', 'SUCCEEDED', 'FAILED', 'CANCELED')
    ),
    constraint workflow_run_version_positive check (version > 0)
);

create index workflow_run_workspace_id_idx on workflow_run (workspace_id);
create index workflow_run_work_item_id_idx on workflow_run (work_item_id);

create table node_run (
    node_run_id text primary key,
    workflow_run_id text not null references workflow_run (workflow_run_id) on delete cascade,
    node_id text not null,
    iteration integer not null,
    state text not null,
    verdict text,
    input_digest text not null,
    output jsonb,
    version bigint not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    unique (workflow_run_id, node_id, iteration),
    constraint node_run_id_not_empty check (length(node_run_id) > 0),
    constraint node_run_node_id_not_empty check (length(node_id) > 0),
    constraint node_run_iteration_positive check (iteration > 0),
    constraint node_run_state_valid check (
        state in ('PENDING', 'READY', 'RUNNING', 'WAITING_HUMAN', 'DECIDED', 'EXECUTION_FAILED', 'SKIPPED', 'CANCELED')
    ),
    constraint node_run_input_digest_sha256 check (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint node_run_version_positive check (version > 0)
);

create index node_run_workflow_run_id_idx on node_run (workflow_run_id);

create table runtime_registration (
    runtime_id text primary key,
    runtime_epoch text not null,
    github_user_id text not null,
    provider_id text,
    provider_version text,
    provider_authenticated boolean not null,
    registered_at timestamptz not null,
    last_heartbeat_at timestamptz not null,
    revoked_at timestamptz,
    constraint runtime_registration_id_not_empty check (length(runtime_id) > 0),
    constraint runtime_registration_epoch_not_empty check (length(runtime_epoch) > 0),
    constraint runtime_registration_github_user_decimal check (github_user_id ~ '^[1-9][0-9]*$')
);

create table runtime_capability (
    runtime_id text not null references runtime_registration (runtime_id) on delete cascade,
    capability_id text not null,
    capability_version integer not null,
    primary key (runtime_id, capability_id, capability_version),
    constraint runtime_capability_id_not_empty check (length(capability_id) > 0),
    constraint runtime_capability_version_positive check (capability_version > 0)
);

create index runtime_capability_lookup_idx on runtime_capability (capability_id, capability_version, runtime_id);

create table execution_task (
    task_id text primary key,
    node_run_id text not null references node_run (node_run_id) on delete cascade,
    attempt integer not null,
    attempt_kind text not null,
    previous_task_id text references execution_task (task_id) on delete restrict,
    human_response_id text,
    resume_ref jsonb,
    capability_id text not null,
    capability_version integer not null,
    provider_id text not null,
    provider_version text not null,
    git_scopes jsonb not null,
    state text not null,
    input_digest text not null,
    deadline_at timestamptz not null,
    runtime_id text references runtime_registration (runtime_id) on delete restrict,
    runtime_epoch text,
    lease_id text,
    fencing_token bigint not null default 0,
    lease_expires_at timestamptz,
    result_digest text,
    result jsonb,
    progress_sequence bigint not null default 0,
    progress_phase text,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    unique (node_run_id, attempt),
    constraint execution_task_id_not_empty check (length(task_id) > 0),
    constraint execution_task_attempt_positive check (attempt > 0),
    constraint execution_task_attempt_kind_valid check (attempt_kind in ('initial', 'retry', 'resume')),
    constraint execution_task_attempt_shape_valid check (
        (attempt_kind = 'initial' and previous_task_id is null and human_response_id is null and resume_ref is null)
        or (attempt_kind = 'retry' and previous_task_id is not null and human_response_id is null and resume_ref is null)
        or (attempt_kind = 'resume' and previous_task_id is not null and human_response_id is not null)
    ),
    constraint execution_task_capability_id_not_empty check (length(capability_id) > 0),
    constraint execution_task_capability_version_positive check (capability_version > 0),
    constraint execution_task_provider_id_not_empty check (length(provider_id) > 0),
    constraint execution_task_provider_version_not_empty check (length(provider_version) > 0),
    constraint execution_task_state_valid check (
        state in ('QUEUED', 'LEASED', 'RUNNING', 'AWAITING_INPUT', 'COMPLETED', 'FAILED', 'EXPIRED', 'CANCELED')
    ),
    constraint execution_task_input_digest_sha256 check (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    constraint execution_task_result_digest_sha256 check (
        result_digest is null or result_digest ~ '^sha256:[0-9a-f]{64}$'
    ),
    constraint execution_task_fencing_nonnegative check (fencing_token >= 0),
    constraint execution_task_progress_nonnegative check (progress_sequence >= 0),
    constraint execution_task_assignment_complete check (
        (runtime_id is null and runtime_epoch is null and lease_id is null and lease_expires_at is null)
        or (runtime_id is not null and runtime_epoch is not null and lease_id is not null and lease_expires_at is not null)
    )
);

create index execution_task_dispatch_idx on execution_task (state, created_at, task_id)
where state = 'QUEUED';
create index execution_task_runtime_id_idx on execution_task (runtime_id)
where runtime_id is not null;
