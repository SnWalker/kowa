-- backend:S06 owns run admission, the frozen task input binding and the
-- knowledge recommendation of the single built-in workflow. This migration is
-- additive: no table, column or constraint of 000001—000005 changes.

create table run_admission (
    workflow_run_id text primary key references workflow_run (workflow_run_id) on delete restrict,
    workspace_id text not null references workspace (workspace_id) on delete restrict,
    work_item_id text not null check (length(work_item_id) > 0),
    definition_name text not null check (length(definition_name) > 0),
    definition_version integer not null check (definition_version > 0),
    definition_digest text not null check (definition_digest ~ '^sha256:[0-9a-f]{64}$'),
    project_repository_id text not null check (project_repository_id ~ '^[1-9][0-9]{0,19}$'),
    knowledge_repository_id text not null check (knowledge_repository_id ~ '^[1-9][0-9]{0,19}$'),
    project_commit_oid text not null check (project_commit_oid ~ '^[0-9a-f]{40}$'),
    knowledge_commit_oid text not null check (knowledge_commit_oid ~ '^[0-9a-f]{40}$'),
    project_branch_ref text not null check (project_branch_ref ~ '^refs/heads/[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$'),
    working_branch_ref text not null check (working_branch_ref ~ '^refs/heads/task/[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$'),
    provider_id text not null check (length(provider_id) > 0),
    provider_version text not null check (length(provider_version) > 0),
    policy_version text not null check (length(policy_version) > 0),
    admitted_by_github_user_id text not null check (admitted_by_github_user_id ~ '^[1-9][0-9]{0,19}$'),
    admitted_at timestamptz not null,
    -- Composite key so every child row proves its workspace and run together.
    unique (workspace_id, workflow_run_id),
    foreign key (definition_name, definition_version)
        references workflow_definition (name, version) on delete restrict
);
create index run_admission_work_item_idx on run_admission (workspace_id, work_item_id);

create table knowledge_recommendation (
    workflow_run_id text primary key,
    workspace_id text not null,
    work_item_id text not null,
    knowledge_repository_id text not null check (knowledge_repository_id ~ '^[1-9][0-9]{0,19}$'),
    knowledge_commit_oid text not null check (knowledge_commit_oid ~ '^[0-9a-f]{40}$'),
    index_path text not null check (length(index_path) > 0),
    index_digest text not null check (index_digest ~ '^sha256:[0-9a-f]{64}$'),
    state text not null check (state in ('RECOMMENDED', 'CONFIRMED')),
    -- The confirmation request is attached once it exists; a confirmed
    -- recommendation always names the task whose accepted answer confirmed it.
    human_task_id text check (human_task_id is null or length(human_task_id) > 0),
    snapshot_id text,
    binding_event_id text,
    effective_input_id text,
    recommended_at timestamptz not null,
    confirmed_at timestamptz,
    foreign key (workspace_id, workflow_run_id)
        references run_admission (workspace_id, workflow_run_id) on delete restrict,
    -- The confirmation facts exist exactly when the recommendation is
    -- confirmed; a half-written confirmation can never be read as one.
    constraint knowledge_recommendation_state_shape check (
        (state = 'RECOMMENDED' and confirmed_at is null and snapshot_id is null
            and binding_event_id is null and effective_input_id is null)
        or (state = 'CONFIRMED' and confirmed_at is not null and snapshot_id is not null
            and binding_event_id is not null and effective_input_id is not null
            and human_task_id is not null)
    )
);

create table knowledge_candidate (
    workflow_run_id text not null,
    position integer not null check (position >= 0),
    path text not null check (length(path) between 1 and 1000),
    digest text not null check (digest ~ '^sha256:[0-9a-f]{64}$'),
    scope text not null check (length(scope) > 0),
    required boolean not null,
    primary key (workflow_run_id, position),
    unique (workflow_run_id, path),
    foreign key (workflow_run_id) references knowledge_recommendation (workflow_run_id) on delete cascade
);

-- The frozen input binding of one dispatched attempt. It records the complete
-- dispatch intent, so a restart or retry reuses the same identity, digest and
-- deadline instead of materialising a second attempt. Rows are immutable and
-- are written before the attempt exists: a binding may exist without a task
-- (an interrupted dispatch), but a dispatched task can never exist without its
-- binding.
create table task_input_binding (
    task_id text primary key check (length(task_id) > 0),
    workspace_id text not null,
    workflow_run_id text not null,
    node_id text not null check (length(node_id) > 0),
    node_run_id text not null check (length(node_run_id) > 0),
    input_id text not null check (length(input_id) > 0),
    input_digest text not null check (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    project_repository_id text not null check (project_repository_id ~ '^[1-9][0-9]{0,19}$'),
    project_commit_oid text not null check (project_commit_oid ~ '^[0-9a-f]{40}$'),
    knowledge_repository_id text not null check (knowledge_repository_id ~ '^[1-9][0-9]{0,19}$'),
    knowledge_commit_oid text not null check (knowledge_commit_oid ~ '^[0-9a-f]{40}$'),
    capability_id text not null check (length(capability_id) > 0),
    capability_version integer not null check (capability_version > 0),
    provider_id text not null check (length(provider_id) > 0),
    provider_version text not null check (length(provider_version) > 0),
    git_scopes jsonb not null check (jsonb_typeof(git_scopes) = 'array'
        and jsonb_array_length(git_scopes) between 1 and 8),
    deadline_at timestamptz not null,
    bound_at timestamptz not null,
    unique (task_id, node_run_id),
    foreign key (workspace_id, workflow_run_id)
        references run_admission (workspace_id, workflow_run_id) on delete restrict,
    foreign key (workspace_id, input_id)
        references effective_input (workspace_id, input_id) on delete restrict
);
create index task_input_binding_node_run_idx on task_input_binding (node_run_id);
create index task_input_binding_run_idx on task_input_binding (workspace_id, workflow_run_id);
create index task_input_binding_input_idx on task_input_binding (workspace_id, input_id);

create table task_input_artifact (
    task_id text not null references task_input_binding (task_id) on delete cascade,
    position integer not null check (position >= 0),
    workspace_id text not null,
    artifact_id text not null,
    primary key (task_id, position),
    unique (task_id, artifact_id),
    foreign key (workspace_id, artifact_id)
        references artifact (workspace_id, artifact_id) on delete restrict
);
create index task_input_artifact_artifact_idx on task_input_artifact (workspace_id, artifact_id);

-- The auxiliary knowledge-suggestion branch of one admitted run. It never
-- blocks the delivery closure, so every attempt is its own row: the attempt
-- history is the audit trail and the newest row is the current state. A lease
-- fences the attempt against a competing or restarted process, so a long or
-- failed upload can neither duplicate the published artifact nor overwrite a
-- published result with a late failure.
create table knowledge_suggestion (
    workflow_run_id text not null,
    attempt integer not null check (attempt >= 1),
    workspace_id text not null,
    work_item_id text not null check (length(work_item_id) > 0),
    attempt_id text not null unique check (attempt_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$'),
    state text not null check (state in ('PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED')),
    input_digest text not null check (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    claimed_until timestamptz,
    artifact_id text check (artifact_id is null or length(artifact_id) > 0),
    artifact_digest text check (artifact_digest is null or artifact_digest ~ '^sha256:[0-9a-f]{64}$'),
    failure_reason text check (failure_reason is null or length(failure_reason) between 1 and 500),
    requested_at timestamptz not null,
    updated_at timestamptz not null,
    version bigint not null,
    primary key (workflow_run_id, attempt),
    foreign key (workspace_id, workflow_run_id)
        references run_admission (workspace_id, workflow_run_id) on delete restrict,
    -- A claim lease exists exactly while the attempt is being processed.
    constraint knowledge_suggestion_claim_shape check (
        (state = 'PROCESSING' and claimed_until is not null)
        or (state <> 'PROCESSING' and claimed_until is null)
    ),
    -- A published attempt names the artifact it published, and a late failure
    -- can never decorate it.
    constraint knowledge_suggestion_published_shape check (
        (state = 'PUBLISHED' and artifact_id is not null and artifact_digest is not null
            and failure_reason is null)
        or (state <> 'PUBLISHED' and artifact_id is null and artifact_digest is null)
    ),
    constraint knowledge_suggestion_failed_shape check (
        (state = 'FAILED' and failure_reason is not null)
        or (state <> 'FAILED' and failure_reason is null)
    )
);
-- At most one open (PENDING or PROCESSING) attempt per run.
create unique index knowledge_suggestion_open_attempt_idx
    on knowledge_suggestion (workflow_run_id) where state in ('PENDING', 'PROCESSING');
create index knowledge_suggestion_claim_idx on knowledge_suggestion (state, claimed_until);
