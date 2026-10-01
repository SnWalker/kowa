create table artifact (
    artifact_id text primary key,
    workspace_id text not null references workspace(workspace_id) on delete restrict,
    subject_id text not null check (length(subject_id)>0),
    kind text not null check (length(kind)>0),
    producer_kind text not null check (producer_kind in ('operation','task')),
    producer_id text not null check (length(producer_id)>0),
    digest text not null check (digest ~ '^sha256:[0-9a-f]{64}$'),
    size bigint not null check (size between 0 and 16777216),
    requires_approval boolean not null,
    media_type text not null check (length(media_type)>0),
    state text not null check (state in ('STAGING','UPLOADED','PUBLISHED','DELETING','DELETED')),
    created_at timestamptz not null,
    unique(workspace_id,artifact_id)
);
create index artifact_orphan_scan_idx on artifact(workspace_id,created_at) where state <> 'DELETED';
create table artifact_reference (
    workspace_id text not null,
    artifact_id text not null,
    owner_kind text not null check (owner_kind in ('run','closure','input','snapshot','approval')),
    owner_id text not null check (length(owner_id)>0),
    primary key(workspace_id,artifact_id,owner_kind,owner_id),
    foreign key(workspace_id,artifact_id) references artifact(workspace_id,artifact_id) on delete restrict
);
create index artifact_reference_owner_idx on artifact_reference(workspace_id,owner_kind,owner_id);
create table knowledge_snapshot (
    workspace_id text not null references workspace(workspace_id) on delete restrict,
    snapshot_id text not null,
    repository_id text not null check (repository_id ~ '^[1-9][0-9]*$'),
    commit_oid text not null check (commit_oid ~ '^[0-9a-f]{40}$'),
    digest text not null check (digest ~ '^sha256:[0-9a-f]{64}$'),
    canonical bytea not null check (octet_length(canonical) between 1 and 16777216),
    primary key(workspace_id,snapshot_id),
    unique(workspace_id,digest)
);
create table knowledge_binding_event (
    workspace_id text not null,
    event_id text not null,
    subject_id text not null check (length(subject_id)>0),
    snapshot_id text not null,
    human_response_id text not null check (length(human_response_id)>0),
    previous_event_id text,
    actor_id text not null check (actor_id ~ '^[1-9][0-9]*$'),
    version bigint not null check (version>0),
    request_digest text not null check (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    confirmed_at timestamptz not null,
    primary key(workspace_id,event_id),
    unique(workspace_id,subject_id,event_id),
    unique(workspace_id,subject_id,version),
    unique(workspace_id,subject_id,actor_id,human_response_id),
    foreign key(workspace_id,snapshot_id) references knowledge_snapshot(workspace_id,snapshot_id) on delete restrict,
    foreign key(workspace_id,subject_id,previous_event_id) references knowledge_binding_event(workspace_id,subject_id,event_id) on delete restrict
);
create index knowledge_event_snapshot_idx on knowledge_binding_event(workspace_id,snapshot_id);
create index knowledge_event_previous_idx on knowledge_binding_event(workspace_id,subject_id,previous_event_id);
create table knowledge_binding (
    workspace_id text not null,
    subject_id text not null,
    version bigint not null check (version>0),
    event_id text not null,
    primary key(workspace_id,subject_id),
    foreign key(workspace_id,subject_id,event_id) references knowledge_binding_event(workspace_id,subject_id,event_id) on delete restrict
);
create table effective_input (
    workspace_id text not null,
    input_id text not null,
    subject_id text not null,
    snapshot_id text not null,
    event_id text not null,
    digest text not null check (digest ~ '^sha256:[0-9a-f]{64}$'),
    canonical bytea not null check (octet_length(canonical) between 1 and 16777216),
    primary key(workspace_id,input_id),
    foreign key(workspace_id,snapshot_id) references knowledge_snapshot(workspace_id,snapshot_id) on delete restrict,
    foreign key(workspace_id,subject_id,event_id) references knowledge_binding_event(workspace_id,subject_id,event_id) on delete restrict
);
create index effective_input_snapshot_idx on effective_input(workspace_id,snapshot_id);
create index effective_input_event_idx on effective_input(workspace_id,subject_id,event_id);
