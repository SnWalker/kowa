create table runtime_observation (
    runtime_id text not null references runtime_registration (runtime_id) on delete cascade,
    runtime_epoch text not null,
    observed_at timestamptz not null,
    received_at timestamptz not null,
    executable boolean not null,
    cli_version text not null,
    version_compatible boolean not null,
    authenticated boolean not null,
    can_execute boolean not null,
    detail text not null default '',
    primary key (runtime_id, runtime_epoch, observed_at),
    constraint runtime_observation_id_not_empty check (length(runtime_id) > 0),
    constraint runtime_observation_epoch_not_empty check (length(runtime_epoch) > 0),
    constraint runtime_observation_cli_version_length check (length(cli_version) <= 256),
    constraint runtime_observation_detail_length check (length(detail) <= 4096)
);

create index runtime_observation_latest_idx on runtime_observation (runtime_id, received_at desc);

create table workspace_execution_config (
    workspace_id text primary key references workspace (workspace_id) on delete cascade,
    version bigint not null,
    capabilities jsonb not null,
    runtimes jsonb not null,
    request_key text not null,
    request_digest text not null,
    updated_by_github_user_id text not null,
    updated_at timestamptz not null,
    constraint workspace_execution_config_version_positive check (version > 0),
    constraint workspace_execution_config_key_length check (length(request_key) <= 128),
    constraint workspace_execution_config_by_decimal check (updated_by_github_user_id ~ '^[1-9][0-9]*$')
);
