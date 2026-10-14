import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ApiError } from '../api/client';
import type { ExecutionConfig, GitRemoteStatus, RuntimeView } from '../api/contracts';
import { newRequestId } from '../api/session';
import { useResource } from '../api/useResource';
import { requireCsrf, useSession } from '../auth/session-context';
import { LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';
import {
  formatCapabilities,
  formatCapabilitiesEditable,
  formatTimestamp,
  parseCapabilities,
  freshnessPresentation,
  gitRemoteSummary,
  machineIdentityPresentation,
  pushPermissionPresentation,
  remoteReadPresentation,
} from './presentation';

/**
 * Runtime, Provider, execution configuration and the read-only Git remote
 * projection. Runtime self-reports never grant visibility, machine GitHub
 * identity and the Web actor stay separate facts, and unknown/stale states are
 * never rendered as usable.
 */
export function RuntimesPage() {
  const { workspaceId = '' } = useParams();
  const { state, client, generation } = useSession();
  const deps = [workspaceId, state.identity?.githubUserId, generation];
  const runtimes = useResource((signal) => client.listRuntimes(workspaceId, signal), deps);
  const config = useResource((signal) => client.getExecutionConfig(workspaceId, signal), deps);
  const gitRemote = useResource((signal) => client.getGitRemoteStatus(workspaceId, signal), deps);

  return (
    <Page title="Runtime 与执行环境">
      <p className="page-intro">
        Workspace <code>{workspaceId}</code>：独立 Runner 健康、Provider 与执行配置、Git remote 可用性与权限缺口。
        这些是相互独立的机器事实，不能合并成单一“健康”结论。
      </p>
      <Section title="Runtime 列表" id="runtime-list">
        {runtimes.status === 'loading' && <LoadingState />}
        {runtimes.status === 'error' && <ErrorNotice title="无法读取 Runtime 列表" error={runtimes.error} />}
        {runtimes.status === 'ready' && runtimes.data && (
          runtimes.data.runtimes.length === 0
            ? <p>当前 Workspace 的执行配置中没有授权任何 Runtime。</p>
            : <ul className="runtime-list">{runtimes.data.runtimes.map((runtime) => <RuntimeCard key={runtime.runtimeId} runtime={runtime} workspaceId={workspaceId} />)}</ul>
        )}
      </Section>
      <ExecutionConfigSection
        config={config}
        runtimes={runtimes.status === 'ready' ? runtimes.data?.runtimes ?? [] : []}
        workspaceId={workspaceId}
        csrfState={state}
      />
      <GitRemoteSection status={gitRemote.status === 'ready' ? gitRemote.data?.status ?? null : null} error={gitRemote.status === 'error' ? gitRemote.error : null} />
    </Page>
  );
}

function RuntimeCard({ runtime, workspaceId }: { runtime: RuntimeView; workspaceId: string }) {
  const freshness = freshnessPresentation(runtime.freshness, runtime.staleReason);
  return (
    <li className="runtime-card">
      <h3><Link to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes/${encodeURIComponent(runtime.runtimeId)}`}>{runtime.runtimeId}</Link></h3>
      <p className="runtime-line">
        <Badge tone={freshness.tone}>{freshness.label}</Badge>{' '}
        <Badge tone={runtime.providerAuthenticated ? 'ok' : 'unknown'}>
          {runtime.providerAuthenticated ? 'Provider 已认证' : 'Provider 认证未知'}
        </Badge>
      </p>
      <p className="page-note">{freshness.detail}</p>
      <DataList
        items={[
          { term: 'Provider', value: `${runtime.providerId} ${runtime.providerVersion}` },
          { term: '能力', value: formatCapabilities(runtime.capabilities) },
          { term: '登记机器 GitHub 用户 ID', value: runtime.gitHubUserId },
          { term: '登记时间', value: formatTimestamp(runtime.registeredAt) },
        ]}
      />
    </li>
  );
}

function ExecutionConfigSection({
  config,
  runtimes,
  workspaceId,
  csrfState,
}: {
  config: ReturnType<typeof useResource<import('../api/contracts').ExecutionConfigResponse>>;
  runtimes: RuntimeView[];
  workspaceId: string;
  csrfState: ReturnType<typeof useSession>['state'];
}) {
  return (
    <Section title="执行配置" id="execution-config">
      {config.status === 'loading' && <LoadingState />}
      {config.status === 'error' && <ErrorNotice title="无法读取执行配置" error={config.error} />}
      {config.status === 'ready' && config.data && (
        <ExecutionConfigBody
          workspaceId={workspaceId}
          initial={config.data.config}
          runtimes={runtimes}
          csrfState={csrfState}
        />
      )}
    </Section>
  );
}

function ExecutionConfigBody({
  workspaceId,
  initial,
  runtimes,
  csrfState,
}: {
  workspaceId: string;
  initial: ExecutionConfig;
  runtimes: RuntimeView[];
  csrfState: ReturnType<typeof useSession>['state'];
}) {
  const { client } = useSession();
  const [config, setConfig] = useState<ExecutionConfig>(initial);
  const [capabilityText, setCapabilityText] = useState(formatCapabilitiesEditable(initial.capabilities));
  const [selected, setSelected] = useState<string[]>(initial.runtimes);
  const [failure, setFailure] = useState<unknown>(null);
  const [conflict, setConflict] = useState<number | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const selectedSet = new Set(selected);

  async function submit(expectedVersion: number) {
    setFailure(null);
    setStatus(null);
    setPending(true);
    try {
      const csrf = requireCsrf(csrfState);
      const capabilities = parseCapabilities(capabilityText);
      const response = await client.updateExecutionConfig(
        workspaceId,
        { requestKey: newRequestId('execution-config'), expectedVersion, capabilities, runtimes: selected },
        csrf,
      );
      setConfig(response.config);
      setCapabilityText(formatCapabilitiesEditable(response.config.capabilities));
      setSelected(response.config.runtimes);
      setConflict(null);
      setStatus(`已保存为版本 v${response.config.version}。`);
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        try {
          const latest = await client.getExecutionConfig(workspaceId);
          setConfig(latest.config);
          setConflict(latest.config.version);
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
    <>
      <p>
        执行配置按版本冻结工作区可用的能力（<code>id@version</code>）与授权 Runtime。运行准入消费一个具体版本，
        不使用“最新”；写入需要管理员、CSRF、<code>requestKey</code> 与 <code>expectedVersion</code>。
      </p>
      <DataList
        items={[
          { term: '当前版本', value: `v${config.version}` },
          { term: '能力', value: formatCapabilities(config.capabilities) },
          { term: '授权 Runtime', value: config.runtimes.length === 0 ? '无' : config.runtimes.join('、') },
          { term: '最后写入者', value: config.updatedByGitHubUserId ?? '未记录' },
          { term: '最后写入时间', value: config.updatedAt ? formatTimestamp(config.updatedAt) : '未记录' },
        ]}
      />
      {failure !== null && <ErrorNotice title="保存执行配置失败" error={failure} />}
      {conflict !== null && (
        <Notice tone="warning" title={`版本冲突：服务端当前为 v${conflict}`}>
          <p>提交基于 v{config.version}；请确认后按 v{conflict} 重新提交，提交内容已保留。</p>
          <div className="actions">
            <button type="button" onClick={() => void submit(conflict)} disabled={pending}>确认并重新提交</button>
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
        <fieldset disabled={pending || conflict !== null}>
          <legend>能力（每行 id@version）</legend>
          <label className="field">
            <span>能力列表</span>
            <textarea
              value={capabilityText}
              onChange={(event) => setCapabilityText(event.target.value)}
              rows={3}
              spellCheck={false}
            />
          </label>
          <legend>授权 Runtime</legend>
          {runtimes.length === 0 ? (
            <p>当前没有可见的 Runtime 可供授权；Runtime 只能通过 Workspace 执行配置获得可见性。</p>
          ) : (
            <ul className="check-list">
              {runtimes.map((runtime) => (
                <li key={runtime.runtimeId}>
                  <label>
                    <input
                      type="checkbox"
                      checked={selectedSet.has(runtime.runtimeId)}
                      onChange={(event) => {
                        setSelected((current) => event.target.checked
                          ? [...current, runtime.runtimeId]
                          : current.filter((value) => value !== runtime.runtimeId));
                      }}
                    />
                    <span>{runtime.runtimeId}</span>
                  </label>
                </li>
              ))}
            </ul>
          )}
        </fieldset>
        <div className="actions">
          <button type="submit" disabled={pending || conflict !== null}>{pending ? '正在保存…' : `保存执行配置（基于 v${config.version}）`}</button>
        </div>
        <p className="page-note">写入需要服务端判定的管理员角色、CSRF 与版本一致；越权会被服务端拒绝，本页不预判授权。</p>
      </form>
    </>
  );
}

function GitRemoteSection({ status, error }: { status: GitRemoteStatus | null; error: unknown }) {
  return (
    <Section title="Git remote 可用性与权限缺口" id="git-remote">
      {error !== null && <ErrorNotice title="无法读取 Git remote 状态" error={error} />}
      {status === null && error === null && <LoadingState />}
      {status !== null && (
        <>
          <p>{gitRemoteSummary(status)}</p>
          <p className="page-note">
            登录、Runtime 注册或 CLI 存在都不构成可用证据；只有真实只读观测才会给出“可读/已授权”。
          </p>
          <DataList
            items={[
              { term: '请求者（Web actor）', value: status.requestedByGitHubUserId },
              { term: '探测结果', value: status.probeOutcome === 'completed' ? '已完成' : `未知${status.probeReason ? `（${status.probeReason}）` : ''}` },
              { term: '观测时间', value: formatTimestamp(status.observedAt) },
              { term: '登记 Runtime', value: status.machineIdentity.runtimeIds.join('、') || '无' },
              {
                term: '登记机器 GitHub 用户 ID',
                value: status.machineIdentity.registeredGitHubUserIds.join('、') || '无',
              },
              {
                term: '机器身份状态',
                value: (() => {
                  const presentation = machineIdentityPresentation(
                    status.machineIdentity.state,
                    status.machineIdentity.registeredGitHubUserIds,
                    status.machineIdentity.observedGitHubUserId,
                  );
                  return <><Badge tone={presentation.tone}>{presentation.label}</Badge>{status.machineIdentity.reason ? ` ${status.machineIdentity.reason}` : ''}</>;
                })(),
              },
            ]}
          />
          {status.repositories.length === 0 ? (
            <p>该 Workspace 尚未登记仓库绑定，因此没有可探测的 remote。</p>
          ) : (
            <table className="member-table">
              <caption>按仓库的只读观测（无写入观测）</caption>
              <thead>
                <tr>
                  <th scope="col">仓库</th>
                  <th scope="col">角色</th>
                  <th scope="col">remote 读取</th>
                  <th scope="col">push 权限</th>
                  <th scope="col">写入观测</th>
                  <th scope="col">观测时间</th>
                </tr>
              </thead>
              <tbody>
                {status.repositories.map((repository) => {
                  const read = remoteReadPresentation(repository.remoteRead.state, repository.remoteRead.transport);
                  const push = pushPermissionPresentation(repository.pushPermission.state);
                  return (
                    <tr key={`${repository.repositoryId}-${repository.role}`}>
                      <td>{repository.repositoryId}</td>
                      <td>{repository.role === 'project' ? '项目仓（可写绑定）' : '知识仓（只读）'}</td>
                      <td>
                        <Badge tone={read.tone}>{read.label}</Badge>
                        {repository.remoteRead.reason ? <p className="page-note">{repository.remoteRead.reason}</p> : null}
                      </td>
                      <td>
                        <Badge tone={push.tone}>{push.label}</Badge>
                        {repository.pushPermission.reason ? <p className="page-note">{repository.pushPermission.reason}</p> : null}
                      </td>
                      <td>无（writeObservation=none）</td>
                      <td>{formatTimestamp(repository.observedAt)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </>
      )}
    </Section>
  );
}
