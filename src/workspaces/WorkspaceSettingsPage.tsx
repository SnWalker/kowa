import { useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { ApiError, type ApiClient } from '../api/client';
import type { RepositoryInput, WorkspaceConfig, WorkspaceRole } from '../api/contracts';
import { newRequestId } from '../api/session';
import { useResource } from '../api/useResource';
import { requireCsrf, useSession } from '../auth/session-context';
import { LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';

const ROLES: WorkspaceRole[] = ['admin', 'developer', 'viewer'];

/**
 * Workspace settings: members and the dual-repository binding.
 *
 * Every write carries the server-issued version plus an idempotency key. A
 * `VERSION_CONFLICT` never replays silently: the page re-reads the Workspace,
 * keeps the operator's input and requires an explicit confirmation against the
 * new version, which then uses a fresh idempotency key.
 */
export function WorkspaceSettingsPage() {
  const { workspaceId = '' } = useParams();
  const { state, client, generation } = useSession();
  const resource = useResource(
    (signal) => client.getWorkspace(workspaceId, signal),
    [workspaceId, state.identity?.githubUserId, generation],
  );
  const [override, setOverride] = useState<{ base: WorkspaceConfig; value: WorkspaceConfig } | null>(null);
  // A write result replaces the baseline it was derived from; a newer server
  // read always wins, so caches cannot survive an identity/workspace switch.
  const config = resource.data && override && override.base === resource.data ? override.value : resource.data;
  const setConfig = (value: WorkspaceConfig) => {
    if (resource.data) setOverride({ base: resource.data, value });
  };

  if (resource.status === 'loading') return <LoadingState />;
  if (resource.status === 'error') {
    return (
      <Page title="Workspace 设置">
        <ErrorNotice title="无法读取 Workspace" error={resource.error} />
        <div className="actions">
          <button type="button" onClick={resource.reload}>重试</button>
        </div>
      </Page>
    );
  }
  if (!config) return <LoadingState />;

  const activeMembers = config.members.filter((member) => member.revokedAt === undefined);
  const myMembership = activeMembers.find((member) => member.githubUserId === state.identity?.githubUserId);
  const isAdmin = myMembership?.role === 'admin';

  return (
    <Page title={config.name}>
      <p className="page-intro">
        Workspace <code>{config.workspaceId}</code>，当前版本 v{config.version}。所有写入需要管理员授权、
        CSRF 与幂等键；可见动作来自服务端返回的成员角色，服务端在每次请求重新判定。
      </p>
      <Section title="身份与授权" id="identity">
        <DataList
          items={[
            { term: 'Web 登录身份', value: `${state.identity?.login ?? ''} (${state.identity?.githubUserId ?? ''})` },
            {
              term: '我的 Workspace 角色',
              value: myMembership ? myMembership.role : '服务端返回的成员列表中没有当前身份',
            },
            {
              term: 'App 安装',
              value: config.project
                ? `${config.project.displayName} 的 installation ${config.project.installationId}（App 安装不等于登录身份或管理权限）`
                : '尚未登记仓库绑定',
            },
          ]}
        />
      </Section>
      <RepositorySection
        config={config}
        client={client}
        workspaceId={workspaceId}
        canWrite={isAdmin}
        onConfig={setConfig}
        csrfState={state}
      />
      <MembersSection
        config={config}
        client={client}
        workspaceId={workspaceId}
        canWrite={isAdmin}
        onConfig={setConfig}
        csrfState={state}
      />
    </Page>
  );
}

type ConfigWriter = {
  config: WorkspaceConfig;
  client: ApiClient;
  workspaceId: string;
  canWrite: boolean;
  onConfig: (config: WorkspaceConfig) => void;
  csrfState: ReturnType<typeof useSession>['state'];
};

function RepositorySection({ config, client, workspaceId, canWrite, onConfig, csrfState }: ConfigWriter) {
  const [project, setProject] = useState<RepositoryInput>(toInput(config.project));
  const [knowledge, setKnowledge] = useState<RepositoryInput>(toInput(config.knowledge));
  const [failure, setFailure] = useState<unknown>(null);
  const [conflict, setConflict] = useState<number | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });

  function idempotencyKey(command: unknown): string {
    const fingerprint = JSON.stringify(command);
    if (keys.current.fingerprint !== fingerprint) {
      keys.current = { fingerprint, key: newRequestId('repositories') };
    }
    return keys.current.key;
  }

  async function submit(expectedVersion: number) {
    setFailure(null);
    setStatus(null);
    setConflict(null);
    setPending(true);
    try {
      const csrf = requireCsrf(csrfState);
      const command = { expectedVersion, project, knowledge };
      const updated = await client.configureRepositories(workspaceId, command, csrf, idempotencyKey(command));
      onConfig(updated);
      setConflict(null);
      setStatus(`已保存为版本 v${updated.version}。`);
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        // Re-read the Workspace and require explicit confirmation.
        try {
          const latest = await client.getWorkspace(workspaceId);
          onConfig(latest);
          setConflict(latest.version);
        } catch (refreshError) {
          setFailure(refreshError);
        }
      } else {
        setFailure(error);
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <Section title="项目仓与知识仓" id="repositories">
      <p>
        项目仓是唯一可写绑定（<code>read_write</code>），知识仓始终只读（<code>read</code>）。
        第二个可写项目仓超出 MVP，服务端会以 <code>POLICY_BLOCKED</code> 拒绝。
      </p>
      {!canWrite && <Notice tone="info" title="只读视图"><p>当前身份不是管理员，写入控件不可用；服务端仍会独立拒绝越权写入。</p></Notice>}
      {failure !== null && <ErrorNotice title="保存仓库绑定失败" error={failure} />}
      {conflict !== null && (
        <Notice tone="warning" title={`版本冲突：服务端当前为 v${conflict}`}>
          <p>你提交时基于 v{config.version}。输入已保留，请确认后按服务端最新版本重新提交（将使用新的幂等键）。</p>
          <div className="actions">
            <button type="button" onClick={() => void submit(conflict)} disabled={pending}>
              确认并按 v{conflict} 重新提交
            </button>
            <button type="button" onClick={() => setConflict(null)}>取消</button>
          </div>
        </Notice>
      )}
      {status !== null && <Notice tone="info" title="已提交"><p>{status}</p></Notice>}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit(config.version);
        }}
      >
        <fieldset disabled={!canWrite || pending || conflict !== null}>
          <legend>项目仓（可写）</legend>
          <RepositoryFields value={project} onChange={setProject} idPrefix="project" />
          <legend>知识仓（只读）</legend>
          <RepositoryFields value={knowledge} onChange={setKnowledge} idPrefix="knowledge" />
        </fieldset>
        {canWrite && conflict === null && (
          <div className="actions">
            <button type="submit" disabled={pending}>{pending ? '正在提交…' : `保存（基于 v${config.version}）`}</button>
          </div>
        )}
      </form>
    </Section>
  );
}

function RepositoryFields({ value, onChange, idPrefix }: { value: RepositoryInput; onChange: (value: RepositoryInput) => void; idPrefix: string }) {
  return (
    <div className="field-row">
      <label className="field">
        <span>Repository ID</span>
        <input
          id={`${idPrefix}-repository-id`}
          value={value.githubRepositoryId}
          onChange={(event) => onChange({ ...value, githubRepositoryId: event.target.value })}
          required
          inputMode="numeric"
          autoComplete="off"
        />
      </label>
      <label className="field">
        <span>Installation ID</span>
        <input
          id={`${idPrefix}-installation-id`}
          value={value.installationId}
          onChange={(event) => onChange({ ...value, installationId: event.target.value })}
          required
          inputMode="numeric"
          autoComplete="off"
        />
      </label>
      <label className="field">
        <span>显示名</span>
        <input
          id={`${idPrefix}-display-name`}
          value={value.displayName}
          onChange={(event) => onChange({ ...value, displayName: event.target.value })}
          required
          autoComplete="off"
        />
      </label>
    </div>
  );
}

function MembersSection({ config, client, workspaceId, canWrite, onConfig, csrfState }: ConfigWriter) {
  const [githubUserId, setGithubUserId] = useState('');
  const [role, setRole] = useState<WorkspaceRole>('developer');
  const [failure, setFailure] = useState<unknown>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function run(action: (csrf: string, key: string) => Promise<void>, note: string) {
    setFailure(null);
    setStatus(null);
    setPending(true);
    try {
      const csrf = requireCsrf(csrfState);
      await action(csrf, newRequestId('member'));
      const latest = await client.getWorkspace(workspaceId);
      onConfig(latest);
      setStatus(note);
    } catch (error) {
      setFailure(error);
    } finally {
      setPending(false);
    }
  }

  return (
    <Section title="成员" id="members">
      {failure !== null && <ErrorNotice title="成员操作失败" error={failure} />}
      {status !== null && <Notice tone="info" title="已提交"><p>{status}</p></Notice>}
      <div className="table-scroll">
      <table className="member-table">
        <caption>服务端返回的成员与角色（撤销的成员保留审计记录）</caption>
        <thead>
          <tr><th scope="col">GitHub 用户 ID</th><th scope="col">角色</th><th scope="col">状态</th><th scope="col">操作</th></tr>
        </thead>
        <tbody>
          {config.members.map((member) => (
            <tr key={member.githubUserId}>
              <td>{member.githubUserId}</td>
              <td>{member.role}</td>
              <td>{member.revokedAt ? `已撤销（${member.revokedAt}）` : '有效'}</td>
              <td>
                {canWrite && member.revokedAt === undefined && member.role !== 'admin' && (
                  <button
                    type="button"
                    disabled={pending}
                    onClick={() => void run(
                      (csrf, key) => client.revokeMember(workspaceId, member.githubUserId, { expectedVersion: config.version }, csrf, key),
                      `已撤销成员 ${member.githubUserId}。`,
                    )}
                  >
                    撤销
                  </button>
                )}
                {canWrite && member.revokedAt === undefined && member.role === 'admin' && <Badge tone="ok">管理员</Badge>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      </div>
      {canWrite ? (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const target = githubUserId.trim();
            void run(
              (csrf, key) => client.upsertMember(workspaceId, target, { expectedVersion: config.version, role }, csrf, key),
              `已设置 ${target} 的角色为 ${role}。`,
            );
          }}
        >
          <div className="field-row">
            <label className="field">
              <span>GitHub 用户 ID</span>
              <input value={githubUserId} onChange={(event) => setGithubUserId(event.target.value)} required inputMode="numeric" autoComplete="off" />
            </label>
            <label className="field">
              <span>角色</span>
              <select value={role} onChange={(event) => setRole(event.target.value as WorkspaceRole)}>
                {ROLES.map((value) => <option key={value} value={value}>{value}</option>)}
              </select>
            </label>
          </div>
          <div className="actions">
            <button type="submit" disabled={pending || githubUserId.trim() === ''}>{pending ? '正在提交…' : '添加/更新成员'}</button>
          </div>
        </form>
      ) : (
        <Notice tone="info" title="无成员写入权限"><p>成员管理需要服务端判定的管理员角色。</p></Notice>
      )}
    </Section>
  );
}

function toInput(binding: WorkspaceConfig['project']): RepositoryInput {
  return {
    githubRepositoryId: binding?.githubRepositoryId ?? '',
    installationId: binding?.installationId ?? '',
    displayName: binding?.displayName ?? '',
  };
}
