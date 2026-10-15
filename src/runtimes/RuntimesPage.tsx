import { Plus } from 'lucide-react';
import { useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ApiError } from '../api/client';
import type { Capability, ExecutionConfig, GitRemoteStatus, RuntimeView } from '../api/contracts';
import { newRequestId } from '../api/session';
import { useResource } from '../api/useResource';
import { requireCsrf, useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { Field, FieldRow, CheckboxField } from '../ui/Field';
import { LoadingState } from '../ui/Feedback';
import { Input } from '../ui/input';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../ui/table';
import { Textarea } from '../ui/textarea';
import type { VersionConflict } from '../ui/version-conflict';
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
  const { state, client, cacheEpoch, generation } = useSession();
  // The scope binds each projection to one identity/Workspace; the read
  // generation re-reads after a focus return and discards late answers from the
  // superseded read, without unmounting the execution-configuration form.
  const deps = [workspaceId, state.identity?.githubUserId, cacheEpoch];
  const scope = `${workspaceId}:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`;
  const options = { scope, generation };
  const runtimes = useResource((signal) => client.listRuntimes(workspaceId, signal), deps, options);
  const config = useResource((signal) => client.getExecutionConfig(workspaceId, signal), deps, options);
  const gitRemote = useResource((signal) => client.getGitRemoteStatus(workspaceId, signal), deps, options);

  return (
    <Page
      title="Runtime 与执行环境"
      meta={<>Workspace <code>{workspaceId}</code></>}
    >
      <p className="mb-4 max-w-[80ch] text-sm text-muted-foreground">
        独立 Runner 健康、Provider 与执行配置、Git remote 可用性与权限缺口。
        这些是相互独立的机器事实，不能合并成单一“健康”结论。
      </p>
      <Section title="Runtime 列表" id="runtime-list">
        {runtimes.status === 'loading' && <LoadingState />}
        {runtimes.status === 'error' && <ErrorNotice title="无法读取 Runtime 列表" error={runtimes.error} />}
        {runtimes.status === 'ready' && runtimes.data && (
          runtimes.data.runtimes.length === 0
            ? <p className="text-sm text-muted-foreground">当前 Workspace 的执行配置中没有授权任何 Runtime。</p>
            : (
              <ul className="grid list-none grid-cols-1 gap-3 p-0 xl:grid-cols-2">
                {runtimes.data.runtimes.map((runtime) => (
                  <RuntimeCard key={runtime.runtimeId} runtime={runtime} workspaceId={workspaceId} />
                ))}
              </ul>
            )
        )}
      </Section>
      <ExecutionConfigSection
        key={scope}
        config={config}
        runtimes={runtimes.status === 'ready' ? runtimes.data?.runtimes ?? [] : []}
        workspaceId={workspaceId}
        csrfState={state}
        // Authorizing a runtime changes which runtimes this workspace may see,
        // so a successful write re-reads the authorized projection.
        onSaved={runtimes.reload}
      />
      <GitRemoteSection status={gitRemote.status === 'ready' ? gitRemote.data?.status ?? null : null} error={gitRemote.status === 'error' ? gitRemote.error : null} />
    </Page>
  );
}

function RuntimeCard({ runtime, workspaceId }: { runtime: RuntimeView; workspaceId: string }) {
  const freshness = freshnessPresentation(runtime.freshness, runtime.staleReason);
  return (
    <li className="min-w-0 rounded-md border border-border bg-card px-3 py-3">
      <h3 className="text-lg font-semibold">
        <Link className="font-mono text-primary-fg no-underline" to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes/${encodeURIComponent(runtime.runtimeId)}`}>
          {runtime.runtimeId}
        </Link>
      </h3>
      <p className="mt-1 flex flex-wrap items-center gap-2">
        <Badge tone={freshness.tone}>{freshness.label}</Badge>
        <Badge tone={runtime.providerAuthenticated ? 'ok' : 'unknown'}>
          {runtime.providerAuthenticated ? 'Provider 已认证' : 'Provider 认证未知'}
        </Badge>
      </p>
      <p className="mt-1 text-xs text-muted-foreground">{freshness.detail}</p>
      <div className="mt-2">
        <DataList
          items={[
            { term: 'Provider', value: <span className="font-mono">{runtime.providerId} {runtime.providerVersion}</span> },
            { term: '能力', value: <span className="font-mono">{formatCapabilities(runtime.capabilities)}</span> },
            { term: '登记机器 GitHub 用户 ID', value: <span className="font-mono">{runtime.gitHubUserId}</span> },
            { term: '登记时间', value: formatTimestamp(runtime.registeredAt) },
          ]}
        />
      </div>
    </li>
  );
}

function ExecutionConfigSection({
  config,
  runtimes,
  workspaceId,
  csrfState,
  onSaved,
}: {
  config: ReturnType<typeof useResource<import('../api/contracts').ExecutionConfigResponse>>;
  runtimes: RuntimeView[];
  workspaceId: string;
  csrfState: ReturnType<typeof useSession>['state'];
  onSaved: () => void;
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
          onSaved={onSaved}
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
  onSaved,
}: {
  workspaceId: string;
  initial: ExecutionConfig;
  runtimes: RuntimeView[];
  csrfState: ReturnType<typeof useSession>['state'];
  onSaved: () => void;
}) {
  const { client } = useSession();
  const [config, setConfig] = useState<ExecutionConfig>(initial);
  const [capabilityText, setCapabilityText] = useState(formatCapabilitiesEditable(initial.capabilities));
  const [selected, setSelected] = useState<string[]>(initial.runtimes);
  const [runtimeIdInput, setRuntimeIdInput] = useState('');
  const [runtimeIdError, setRuntimeIdError] = useState<string | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  const [conflict, setConflict] = useState<VersionConflict | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });

  /**
   * One intent keeps one `requestKey` across a retry: the same content against
   * the same baseline is the same delivery. Changing the content or the
   * baseline — including confirming after a conflict — is a new intent and gets
   * a new key; a settled intent is closed out.
   */
  function requestKeyFor(command: { expectedVersion: number; capabilities: Capability[]; runtimes: string[] }): string {
    const fingerprint = JSON.stringify(command);
    if (keys.current.fingerprint !== fingerprint) {
      keys.current = { fingerprint, key: newRequestId('execution-config') };
    }
    return keys.current.key;
  }

  const selectedSet = new Set(selected);
  const selectedNotVisible = selected.filter(
    (runtimeId) => !runtimes.some((runtime) => runtime.runtimeId === runtimeId),
  );

  /** Authorizing a not-yet-authorized runtime needs its ID: registration alone
   * never grants visibility, so the admin names the runtime and the server
   * still decides whether the frozen configuration accepts it. */
  function addRuntimeById() {
    const candidate = runtimeIdInput.trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(candidate)) {
      setRuntimeIdError('Runtime ID 必须匹配契约的 ID 形态（字母或数字开头，最多 128 个字母、数字、_ 或 -）。');
      return;
    }
    if (selectedSet.has(candidate)) {
      setRuntimeIdError(`${candidate} 已在授权列表中。`);
      return;
    }
    setRuntimeIdError(null);
    setSelected((current) => [...current, candidate]);
    setRuntimeIdInput('');
  }

  async function submit(expectedVersion: number) {
    setFailure(null);
    setStatus(null);
    setPending(true);
    try {
      const csrf = requireCsrf(csrfState);
      const capabilities = parseCapabilities(capabilityText);
      const command = { expectedVersion, capabilities, runtimes: selected };
      const response = await client.updateExecutionConfig(
        workspaceId,
        { requestKey: requestKeyFor(command), ...command },
        csrf,
      );
      setConfig(response.config);
      setCapabilityText(formatCapabilitiesEditable(response.config.capabilities));
      setSelected(response.config.runtimes);
      keys.current = { fingerprint: '', key: '' }; // the intent is settled
      setConflict(null);
      setStatus(`已保存为版本 v${response.config.version}。`);
      onSaved();
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        try {
          const latest = await client.getExecutionConfig(workspaceId);
          setConfig(latest.config);
          setConflict({ submittedVersion: expectedVersion, serverVersion: latest.config.version });
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
      <p className="mb-3 max-w-[80ch] text-sm text-muted-foreground">
        执行配置按版本冻结工作区可用的能力（<code>id@version</code>）与授权 Runtime。运行准入消费一个具体版本，
        不使用“最新”；写入需要管理员、CSRF、<code>requestKey</code> 与 <code>expectedVersion</code>。
      </p>
      <DataList
        items={[
          { term: '当前版本', value: `v${config.version}` },
          { term: '能力', value: <span className="font-mono">{formatCapabilities(config.capabilities)}</span> },
          { term: '授权 Runtime', value: <span className="font-mono">{config.runtimes.length === 0 ? '无' : config.runtimes.join('、')}</span> },
          { term: '最后写入者', value: config.updatedByGitHubUserId ?? '未记录' },
          { term: '最后写入时间', value: config.updatedAt ? formatTimestamp(config.updatedAt) : '未记录' },
        ]}
      />
      {failure !== null && <ErrorNotice title="保存执行配置失败" error={failure} />}
      {conflict !== null && (
        <Notice tone="warning" title={`版本冲突：你提交时基于 v${conflict.submittedVersion}，服务端当前为 v${conflict.serverVersion}`}>
          <p>提交内容已保留；请确认后按服务端最新版本重新提交（将使用新的 requestKey）。</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button type="button" onClick={() => void submit(conflict.serverVersion)} disabled={pending}>
              确认并按 v{conflict.serverVersion} 重新提交
            </Button>
            <Button type="button" variant="secondary" onClick={() => setConflict(null)}>取消</Button>
          </div>
        </Notice>
      )}
      {status !== null && <Notice tone="info" title="已提交"><p>{status}</p></Notice>}
      <form
        className="mt-3"
        onSubmit={(event) => {
          event.preventDefault();
          void submit(config.version);
        }}
      >
        <fieldset
          disabled={pending || conflict !== null}
          className="mb-4 rounded-md border border-border-muted px-3 pb-3 pt-1"
        >
          <legend className="px-1 text-xs font-semibold text-muted-foreground">能力（每行 id@version）</legend>
          <Field label="能力列表">
            <Textarea
              className="font-mono"
              value={capabilityText}
              onChange={(event) => setCapabilityText(event.target.value)}
              rows={3}
              spellCheck={false}
            />
          </Field>
        </fieldset>
        <fieldset
          disabled={pending || conflict !== null}
          className="mb-4 rounded-md border border-border-muted px-3 pb-3 pt-1"
        >
          <legend className="px-1 text-xs font-semibold text-muted-foreground">授权 Runtime</legend>
          {runtimes.length === 0 ? (
            <p className="text-sm text-muted-foreground">当前没有可见的 Runtime 可供勾选；Runtime 只能通过 Workspace 执行配置获得可见性。</p>
          ) : (
            <ul className="m-0 flex list-none flex-col gap-1 p-0">
              {runtimes.map((runtime) => (
                <li key={runtime.runtimeId}>
                  <CheckboxField
                    id={`authorize-${runtime.runtimeId}`}
                    checked={selectedSet.has(runtime.runtimeId)}
                    onCheckedChange={(checked) => {
                      setSelected((current) => checked
                        ? [...current, runtime.runtimeId]
                        : current.filter((value) => value !== runtime.runtimeId));
                    }}
                  >
                    {runtime.runtimeId}
                  </CheckboxField>
                </li>
              ))}
            </ul>
          )}
          {selectedNotVisible.length > 0 && (
            <ul className="m-0 flex list-none flex-col gap-1 p-0">
              {selectedNotVisible.map((runtimeId) => (
                <li key={runtimeId}>
                  <CheckboxField
                    id={`authorize-${runtimeId}`}
                    checked
                    onCheckedChange={() => setSelected((current) => current.filter((value) => value !== runtimeId))}
                  >
                    {runtimeId}（已按 ID 授权；当前没有对应可见 Runtime）
                  </CheckboxField>
                </li>
              ))}
            </ul>
          )}
          <FieldRow className="mt-3">
            <Field label="按 ID 授权 Runtime" className="min-w-0 flex-1 basis-56">
              <Input
                value={runtimeIdInput}
                onChange={(event) => setRuntimeIdInput(event.target.value)}
                placeholder="runtime-id"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Button type="button" variant="secondary" onClick={addRuntimeById} disabled={runtimeIdInput.trim() === ''}>
              <Plus aria-hidden="true" />
              添加 Runtime
            </Button>
          </FieldRow>
          {runtimeIdError !== null && (
            <p role="alert" className="mt-2 text-xs font-semibold text-danger">{runtimeIdError}</p>
          )}
        </fieldset>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={pending || conflict !== null}>{pending ? '正在保存…' : `保存执行配置（基于 v${config.version}）`}</Button>
        </div>
        <p className="mt-2 text-xs text-muted-foreground">写入需要服务端判定的管理员角色、CSRF 与版本一致；越权会被服务端拒绝，本页不预判授权。</p>
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
          <p className="text-sm">{gitRemoteSummary(status)}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            登录、Runtime 注册或 CLI 存在都不构成可用证据；只有真实只读观测才会给出“可读/已授权”。
          </p>
          <div className="mt-3">
            <DataList
              items={[
                { term: '请求者（Web actor）', value: <span className="font-mono">{status.requestedByGitHubUserId}</span> },
                { term: '探测结果', value: status.probeOutcome === 'completed' ? '已完成' : `未知${status.probeReason ? `（${status.probeReason}）` : ''}` },
                { term: '观测时间', value: formatTimestamp(status.observedAt) },
                { term: '登记 Runtime', value: <span className="font-mono">{status.machineIdentity.runtimeIds.join('、') || '无'}</span> },
                {
                  term: '登记机器 GitHub 用户 ID',
                  value: <span className="font-mono">{status.machineIdentity.registeredGitHubUserIds.join('、') || '无'}</span>,
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
          </div>
          {status.repositories.length === 0 ? (
            <p className="mt-3 text-sm text-muted-foreground">该 Workspace 尚未登记仓库绑定，因此没有可探测的 remote。</p>
          ) : (
            <Table className="mt-3">
              <TableCaption className="caption-top mb-2 mt-0 text-left text-xs text-muted-foreground">按仓库的只读观测（无写入观测）</TableCaption>
              <TableHeader className="bg-muted">
                <TableRow>
                  <TableHead scope="col">仓库</TableHead>
                  <TableHead scope="col">角色</TableHead>
                  <TableHead scope="col">remote 读取</TableHead>
                  <TableHead scope="col">push 权限</TableHead>
                  <TableHead scope="col">写入观测</TableHead>
                  <TableHead scope="col">观测时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {status.repositories.map((repository) => {
                  const read = remoteReadPresentation(repository.remoteRead.state, repository.remoteRead.transport);
                  const push = pushPermissionPresentation(repository.pushPermission.state);
                  return (
                    <TableRow key={`${repository.repositoryId}-${repository.role}`}>
                      <TableCell className="whitespace-nowrap font-mono">{repository.repositoryId}</TableCell>
                      <TableCell className="whitespace-normal">{repository.role === 'project' ? '项目仓（可写绑定）' : '知识仓（只读）'}</TableCell>
                      <TableCell className="whitespace-normal">
                        <Badge tone={read.tone}>{read.label}</Badge>
                        {repository.remoteRead.reason ? <p className="mt-1 text-xs text-muted-foreground">{repository.remoteRead.reason}</p> : null}
                      </TableCell>
                      <TableCell className="whitespace-normal">
                        <Badge tone={push.tone}>{push.label}</Badge>
                        {repository.pushPermission.reason ? <p className="mt-1 text-xs text-muted-foreground">{repository.pushPermission.reason}</p> : null}
                      </TableCell>
                      <TableCell className="whitespace-normal">无（writeObservation=none）</TableCell>
                      <TableCell className="whitespace-normal">{formatTimestamp(repository.observedAt)}</TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </>
      )}
    </Section>
  );
}
