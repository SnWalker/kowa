create table web_identity (
    github_user_id text primary key,
    login text not null,
    authorized_at timestamptz not null,
    revoked_at timestamptz,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    constraint web_identity_github_user_id_decimal check (github_user_id ~ '^[1-9][0-9]*$')
);

create table github_oauth_flow (
    state_digest bytea primary key,
    pkce_verifier text not null,
    return_to text not null,
    expires_at timestamptz not null,
    created_at timestamptz not null,
    constraint github_oauth_flow_state_digest_length check (octet_length(state_digest) = 32),
    constraint github_oauth_flow_pkce_verifier_not_empty check (length(pkce_verifier) > 0)
);

create table web_session (
    token_digest bytea primary key,
    csrf_digest bytea not null,
    github_user_id text not null references web_identity (github_user_id),
    expires_at timestamptz not null,
    revoked_at timestamptz,
    created_at timestamptz not null,
    constraint web_session_token_digest_length check (octet_length(token_digest) = 32),
    constraint web_session_csrf_digest_length check (octet_length(csrf_digest) = 32)
);

create index web_session_github_user_id_idx on web_session (github_user_id);

create table workspace (
    workspace_id text primary key,
    name text not null,
    version bigint not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    constraint workspace_id_not_empty check (length(workspace_id) > 0),
    constraint workspace_name_not_empty check (length(btrim(name)) > 0),
    constraint workspace_version_positive check (version > 0)
);

create table workspace_member (
    workspace_id text not null references workspace (workspace_id) on delete cascade,
    github_user_id text not null,
    role text not null,
    revoked_at timestamptz,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    primary key (workspace_id, github_user_id),
    constraint workspace_member_github_user_id_decimal check (github_user_id ~ '^[1-9][0-9]*$'),
    constraint workspace_member_role_valid check (role in ('admin', 'developer', 'viewer'))
);

create index workspace_member_github_user_id_idx on workspace_member (github_user_id);

create table repository_binding (
    workspace_id text not null references workspace (workspace_id) on delete cascade,
    role text not null,
    github_repository_id text not null,
    installation_id text not null,
    display_name text not null,
    access_mode text not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    primary key (workspace_id, role),
    unique (workspace_id, github_repository_id),
    constraint repository_binding_role_valid check (role in ('project', 'knowledge')),
    constraint repository_binding_access_valid check (
        (role = 'project' and access_mode = 'read_write')
        or (role = 'knowledge' and access_mode = 'read')
    ),
    constraint repository_binding_repository_id_decimal check (github_repository_id ~ '^[1-9][0-9]*$'),
    constraint repository_binding_installation_id_decimal check (installation_id ~ '^[1-9][0-9]*$')
);

create table command_idempotency (
    id bigint generated always as identity primary key,
    actor_github_user_id text not null,
    command_type text not null,
    target_id text not null,
    request_key text not null,
    request_digest text not null,
    response jsonb not null,
    created_at timestamptz not null,
    unique (actor_github_user_id, command_type, target_id, request_key),
    constraint command_idempotency_actor_decimal check (actor_github_user_id ~ '^[1-9][0-9]*$'),
    constraint command_idempotency_request_key_not_empty check (length(request_key) > 0),
    constraint command_idempotency_request_digest_sha256 check (request_digest ~ '^[0-9a-f]{64}$')
);

create table audit_event (
    id bigint generated always as identity primary key,
    workspace_id text references workspace (workspace_id) on delete restrict,
    actor_github_user_id text not null,
    action text not null,
    object_version bigint,
    details jsonb not null,
    created_at timestamptz not null,
    constraint audit_event_actor_decimal check (actor_github_user_id ~ '^[1-9][0-9]*$'),
    constraint audit_event_action_not_empty check (length(action) > 0)
);

create index audit_event_workspace_id_idx on audit_event (workspace_id);
