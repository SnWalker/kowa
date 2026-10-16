import { Rocket } from 'lucide-react';
import { useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { ApiError, type ApiClient } from '../api/client';
import type { WorkspaceRole } from '../api/contracts';
import { useResource } from '../api/useResource';
import { newRequestId } from '../api/session';
import { requireCsrf, useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';
import type { VersionConflict } from '../ui/version-conflict';
import { formatTimestamp } from '../runtimes/presentation';
import { RunView } from '../runs/RunView';
import { workItemStatePresentation } from './presentation';
import type { WorkItem, WorkItemResponse } from '../api/contracts';

const WRITER_ROLES: WorkspaceRole[] = ['admin', 'developer'];

/**
 * Work item detail: the requirement/acceptance the server froze, the fixed
 * workflow entry, and the knowledge binding of the admitted run.
 *
 * The DAG, node rounds and recovery actions belong to frontend:S04/S05; this
 * page shows the run identity and the knowledge binding and stops there.
 */
export function WorkItemDetailPage() {
  const { workspaceId = '', workItemId = '' } = useParams();
  const { state, client, cacheEpoch, generation } = useSession();
  const scope = `${workspaceId}:${workItemId}:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`;
  const deps = [workspaceId, workItemId, state.identity?.githubUserId, cacheEpoch];
  const item = useResource(
    (signal) => client.getWorkItem(workspaceId, workItemId, signal),
    deps,
    { scope, generation },
  );
  // A refused start is reported above the run projection as well: re-reading the
  // work item can replace the start entry with the run that really exists (the
  // server admits one advancement run per work item), and the operator must
  // still see why the start was refused.
  const [startRefusal, setStartRefusal] = useState<
    { kind: 'conflict'; submittedVersion: number; serverVersion: number } | { kind: 'refused'; error: unknown } | null
  >(null);
  const workspace = useResource(
    (signal) => client.getWorkspace(workspaceId, signal),
    [workspaceId, state.identity?.githubUserId, cacheEpoch],
    { scope, generation },
  );
  // Writes never fabricate a projection: every command re-reads the server
  // outcome, so no local copy can survive an identity/Workspace switch.
  const current: WorkItemResponse['workItem'] | undefined = item.data?.workItem;

  if (item.status === 'loading') return <LoadingState />;
  if (item.status === 'error') {
    return (
      <Page title="工作项">
        <ErrorNotice title="无法读取工作项" error={item.error} />
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={item.reload}>重试</Button>
        </div>
      </Page>
    );
  }
  if (!current) return <LoadingState />;

  const presentation = workItemStatePresentation(current.state, current.currentRunId !== undefined);
  const myRole = workspace.data
    ? workspace.data.members.find((member) => member.githubUserId === state.identity?.githubUserId && member.revokedAt === undefined)?.role
    : undefined;
  const canWrite = myRole !== undefined && WRITER_ROLES.includes(myRole);

  return (
    <Page
      title={current.title}
      meta={<>工作项 <code>{current.workItemId}</code> · 版本 v{current.version}</>}
    >
      <Section title="需求与验收" id="intake">
        <DataList
          items={[
            { term: '状态', value: <Badge tone={presentation.tone}>{presentation.label}</Badge> },
            { term: '目标仓库（服务端绑定）', value: <code>{current.targetRepositoryId}</code> },
            { term: '我的 Workspace 角色', value: myRole ?? '（Workspace 投影不可用或当前身份不是成员）' },
            {
              term: '需求',
              value: (
                <>
                  <p className="whitespace-pre-wrap">{current.requirement.content}</p>
                  <p className="mt-1 text-xs text-muted-foreground">版本 {current.requirement.version} · 摘要 {current.requirement.digest}</p>
                </>
              ),
            },
            {
              term: '验收标准',
              value: (
                <>
                  <p className="whitespace-pre-wrap">{current.acceptance.content}</p>
                  <p className="mt-1 text-xs text-muted-foreground">版本 {current.acceptance.version} · 摘要 {current.acceptance.digest}</p>
                </>
              ),
            },
            { term: '创建者 / 创建时间', value: `${current.createdByGitHubUserId} · ${formatTimestamp(current.createdAt)}` },
          ]}
        />
        <p className="mt-2 text-xs text-muted-foreground">{presentation.detail}</p>
      </Section>
      {startRefusal?.kind === 'conflict' && (
        <Notice
          tone="warning"
          title={`启动被拒绝：你提交时基于 v${startRefusal.submittedVersion}，服务端当前为 v${startRefusal.serverVersion}`}
        >
          <p>
            已重新读取服务端投影。MVP 中一个工作项同时只有一条推进运行；如果下方已出现交付运行，说明该推进运行
            已由其他操作创建，启动入口因此不再可用（服务端以 <code>POLICY_BLOCKED</code> 拒绝第二次准入）。
          </p>
        </Notice>
      )}
      {startRefusal?.kind === 'refused' && <ErrorNotice title="启动固定流程失败" error={startRefusal.error} />}
      {current.currentRunId ? (
        <RunView
          key={`${scope}:${current.currentRunId}`}
          client={client}
          workspaceId={workspaceId}
          workItemId={workItemId}
          runId={current.currentRunId}
          workItem={current}
          onReloadWorkItem={item.reload}
          scope={scope}
          generation={generation}
        />
      ) : (
        <StartRunSection
          key={scope}
          client={client}
          workspaceId={workspaceId}
          workItemId={workItemId}
          workItem={current}
          canWrite={canWrite}
          csrfState={state}
          onStarted={() => item.reload()}
          onRefused={setStartRefusal}
        />
      )}
    </Page>
  );
}

type StartRefusal =
  | { kind: 'conflict'; submittedVersion: number; serverVersion: number }
  | { kind: 'refused'; error: unknown };

type StartRunSectionProps = {
  client: ApiClient;
  workspaceId: string;
  workItemId: string;
  workItem: WorkItem;
  canWrite: boolean;
  csrfState: ReturnType<typeof useSession>['state'];
  onStarted: () => void;
  onRefused: (refusal: StartRefusal | null) => void;
};

/**
 * The single fixed workflow entry. The start freezes the project/knowledge
 * baselines and opens the knowledge confirmation; it is not an approval and it
 * never picks a template (the MVP has exactly one fixed definition).
 */
function StartRunSection({ client, workspaceId, workItemId, workItem, canWrite, csrfState, onStarted, onRefused }: StartRunSectionProps) {
  const [status, setStatus] = useState<string | null>(null);
  const [conflict, setConflict] = useState<VersionConflict | null>(null);
  const [pending, setPending] = useState(false);
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });

  function requestKey(expectedVersion: number): string {
    const fingerprint = JSON.stringify({ workItemId, expectedVersion });
    if (keys.current.fingerprint !== fingerprint) {
      keys.current = { fingerprint, key: newRequestId('start') };
    }
    return keys.current.key;
  }

  async function start(expectedVersion: number) {
    setStatus(null);
    setPending(true);
    onRefused(null);
    try {
      const csrf = requireCsrf(csrfState);
      const result = await client.startRun(workspaceId, workItemId, { requestKey: requestKey(expectedVersion), expectedVersion }, csrf);
      keys.current = { fingerprint: '', key: '' };
      setConflict(null);
      setStatus(
        result.replayed
          ? '该启动请求由服务端判定为幂等重放：没有创建新的运行，展示的是既有准入事实。'
          : `已启动固定流程，运行 ${result.admission.workflowRunId}；知识绑定等待人工确认。`,
      );
      onStarted();
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        // The WorkItem moved underneath the submission: re-read the version the
        // server really holds, keep the intent visible and require an explicit
        // confirmation before resubmitting under a fresh request key.
        try {
          const latest = await client.getWorkItem(workspaceId, workItemId);
          setConflict({ submittedVersion: expectedVersion, serverVersion: latest.workItem.version });
          onRefused({ kind: 'conflict', submittedVersion: expectedVersion, serverVersion: latest.workItem.version });
        } catch (refreshError) {
          onRefused({ kind: 'refused', error: refreshError });
        }
        onStarted();
      } else if (error instanceof ApiError && (error.category === 'POLICY_BLOCKED' || error.category === 'RESOURCE_UNAVAILABLE')) {
        // A refused or unavailable admission can still have produced facts (for
        // example a run whose knowledge baseline failed): re-read the work item
        // instead of asserting a local outcome, and never show an unavailable
        // knowledge source as "no knowledge".
        onRefused({ kind: 'refused', error });
        onStarted();
      } else {
        // Any other failure is reported by this section alone: no local copy
        // replaces the server outcome.
        onRefused({ kind: 'refused', error });
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <Section title="启动固定流程" id="start-run">
      <p className="mb-3 max-w-[80ch] text-sm text-muted-foreground">
        MVP 只有一条固定、内置、版本化的工作流。启动会冻结项目仓与知识仓的默认分支基线、创建交付运行，
        并生成知识推荐等待人工确认；启动本身不是批准，确认前的运行不会派发任何任务。
      </p>
      {conflict !== null && (
        <Notice tone="warning" title={`版本冲突：你提交时基于 v${conflict.submittedVersion}，服务端当前为 v${conflict.serverVersion}`}>
          <p>已重新读取工作项；请确认后按服务端最新版本重新提交（将使用新的请求键）。</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button type="button" onClick={() => void start(conflict.serverVersion)} disabled={pending}>
              确认并按 v{conflict.serverVersion} 重新提交
            </Button>
            <Button type="button" variant="secondary" onClick={() => setConflict(null)}>取消</Button>
          </div>
        </Notice>
      )}
      {status !== null && <Notice tone="info" title="已提交"><p>{status}</p></Notice>}
      {canWrite ? (
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" onClick={() => void start(workItem.version)} disabled={pending || conflict !== null}>
            <Rocket aria-hidden="true" />
            {pending ? '正在启动…' : `启动固定流程（基于 v${workItem.version}）`}
          </Button>
        </div>
      ) : (
        <Notice tone="info" title="只读视图">
          <p>启动运行需要服务端判定的 developer/admin 角色；当前身份没有该角色。服务端仍会独立拒绝越权启动。</p>
        </Notice>
      )}
    </Section>
  );
}
