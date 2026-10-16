import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useSession } from '../auth/session-context';
import { useResource } from '../api/useResource';
import type { HumanTask } from '../api/contracts';
import { Button } from '../ui/button';
import { EmptyState, LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';
import { humanTaskStatePresentation } from '../workitems/presentation';
import { formatTimestamp } from '../runtimes/presentation';
import { HumanTaskAnswer } from './HumanTaskAnswer';
import { HumanTaskBasis } from './HumanTaskBasis';
import {
  actionPresentation,
  completionModePresentation,
  decisionPresentation,
  objectBindingPresentation,
  reviewedVersionCheck,
  versionCheckPresentation,
} from './presentation';

/**
 * One versioned human request, in full: what is being asked, the exact object
 * version the answer binds to, the basis the projections serve for it, and —
 * only when the server authorized it for this member — the answer form.
 *
 * The page keeps its own reads (run, frozen definition, published artifacts)
 * separate from the request: a projection that cannot be read is reported as
 * unreadable, never as "nothing to review", and none of them is used to rebuild
 * the request's fields, version or object binding.
 */
export function HumanTaskDetailPage() {
  const { workspaceId = '', humanTaskId = '' } = useParams();
  const { state, client, cacheEpoch, generation } = useSession();
  const scope = `${workspaceId}:human-task:${humanTaskId}:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`;
  const deps = [workspaceId, humanTaskId, state.identity?.githubUserId, cacheEpoch];
  const task = useResource(
    (signal) => client.getHumanTask(workspaceId, humanTaskId, signal),
    deps,
    { scope, generation },
  );
  // A conflict or a fresh answer re-read replaces the baseline a refused answer
  // was built on; a newer server read always wins over a stale local copy.
  const [override, setOverride] = useState<{ base: HumanTask; value: HumanTask } | null>(null);
  const current: HumanTask | undefined =
    task.data && override && override.base === task.data.humanTask ? override.value : task.data?.humanTask;

  const runId = current?.workflowRunId;
  const workItemId = current?.workItemId;
  const run = useResource(
    (signal) => (runId && workItemId ? client.getRunDetail(workspaceId, workItemId, runId, signal) : Promise.resolve(undefined)),
    [workspaceId, workItemId, runId, ...deps],
    { scope, generation },
  );
  const workflow = useResource(
    (signal) => client.getFixedWorkflow(workspaceId, signal),
    [workspaceId, state.identity?.githubUserId, cacheEpoch],
    { scope: `${scope}:workflow`, generation },
  );
  const artifacts = useResource(
    (signal) => (workItemId ? client.listArtifacts(workspaceId, workItemId, signal) : Promise.resolve(undefined)),
    [workspaceId, workItemId, ...deps],
    { scope: `${scope}:artifacts`, generation },
  );

  if (task.status === 'loading') return <LoadingState />;
  if (task.status === 'error' || current === undefined) {
    return (
      <Page title="人工待办">
        <ErrorNotice title="无法读取人工待办" error={task.error} />
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={task.reload}>重试</Button>
        </div>
      </Page>
    );
  }

  const mode = completionModePresentation(current.request.completionMode);
  const action = actionPresentation(current);

  return (
    <Page
      title={current.request.title}
      meta={<>人工待办 <code>{current.humanTaskId}</code> · 任务版本 v{current.version}</>}
      actions={
        <Button asChild variant="secondary" size="sm">
          <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/human-tasks`}>返回待办清单</Link>
        </Button>
      }
    >
      <Section title="人工待办" id="human-task">
        <DataList
          items={[
            { term: '任务状态', value: <Badge tone={humanTaskStatePresentation(current.state).tone}>{humanTaskStatePresentation(current.state).label}</Badge> },
            { term: '请求种类', value: <code>{current.request.kind}</code> },
            { term: '完成方式', value: `${mode.label}：${mode.detail}` },
            { term: '请求版本', value: <code>v{current.request.version} · 摘要 {current.requestDigest}</code> },
            { term: '工作项', value: <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(current.workItemId)}`}><code>{current.workItemId}</code></Link> },
            {
              term: '运行 / 节点',
              value: current.workflowRunId ? (
                <>
                  <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(current.workItemId)}`}>
                    <code>{current.workflowRunId}</code>
                  </Link>
                  {' · '}
                  <code>{current.nodeRunId ?? '（未登记）'}</code>
                </>
              ) : (
                <code>（该请求没有关联运行，例如关闭用例产生的任务）</code>
              ),
            },
            { term: '候选 / 角色（服务端登记）', value: `${(current.request.candidates ?? []).join('、') || '（未登记候选）'} · ${(current.request.roles ?? []).join('、') || '（未登记角色）'}` },
            { term: '服务端动作判定', value: action.detail },
            { term: '创建 / 更新', value: `${formatTimestamp(current.createdAt)} · ${formatTimestamp(current.updatedAt)}` },
          ]}
        />
      </Section>

      <Section title="所审版本" id="human-task-object">
        <ObjectSection task={current} runIndexDigest={run.data?.knowledgeRecommendation?.indexDigest} />
      </Section>

      <Section title="依据" id="human-task-basis">
        <HumanTaskBasis
          task={current}
          run={run.data}
          workflow={workflow.data}
          artifacts={artifacts.data}
          runState={run}
          workflowState={workflow}
          artifactState={artifacts}
          onRetryRun={run.reload}
          onRetryWorkflow={workflow.reload}
          onRetryArtifacts={artifacts.reload}
        />
      </Section>

      <Section title="答复" id="human-task-answer">
        <AnswerSection
          client={client}
          workspaceId={workspaceId}
          task={current}
          canAnswer={action.canAnswer}
          answerable={mode.answerable}
          modeDetail={mode.detail}
          onAccepted={(resolved) => setOverride({ base: task.data?.humanTask ?? resolved, value: resolved })}
        />
      </Section>

      {current.response && (
        <Section title="答复记录" id="human-task-response">
          <AcceptedAnswer task={current} />
        </Section>
      )}
    </Page>
  );
}

/**
 * The frozen object version, and — for the one object kind whose digest the
 * page also reads from a projection — whether the two agree. A page that cannot
 * read the projection reports `unknown`, never `match`.
 */
function ObjectSection({ task, runIndexDigest }: { task: HumanTask; runIndexDigest?: string }) {
  const binding = objectBindingPresentation(task.request.object);
  const check = reviewedVersionCheck(task.request.object, runIndexDigest);
  const presentation = versionCheckPresentation(check, '该运行上的冻结索引');
  return (
    <>
      <DataList
        items={[
          { term: '对象类别', value: <code>{binding.kind}</code> },
          { term: '对象标识', value: <code>{task.request.object.id}</code> },
          { term: '所审版本', value: <code>{binding.version}</code> },
          { term: '对象摘要', value: <code>{binding.digest ?? '（请求未登记摘要）'}</code> },
          { term: '绑定语义', value: binding.detail },
        ]}
      />
      {task.request.object.kind === 'knowledge_recommendation' && (
        <p className="mt-2 text-xs text-muted-foreground">
          <Badge tone={presentation.tone}>{presentation.label}</Badge> {presentation.detail}
        </p>
      )}
    </>
  );
}

function AnswerSection({
  client,
  workspaceId,
  task,
  canAnswer,
  answerable,
  modeDetail,
  onAccepted,
}: {
  client: ReturnType<typeof useSession>['client'];
  workspaceId: string;
  task: HumanTask;
  canAnswer: boolean;
  answerable: boolean;
  modeDetail: string;
  onAccepted: (resolved: HumanTask) => void;
}) {
  if (task.state === 'EXPIRED') {
    return (
      <Notice tone="warning" title="该请求已过期">
        <p>
          过期原因（服务端）：{task.expiredReason ?? '（服务端未给出原因）'}。服务端已判定这条请求的所审对象或请求版本不再适用，
          因此它不会再被应用：请重新审阅服务端当前的版本并等待新的请求后再答复；
          本页不会自动重试这次决定，也不会把答复改绑到新版本。
        </p>
      </Notice>
    );
  }
  if (task.state === 'CANCELED') {
    return (
      <Notice tone="info" title="该请求已被取消">
        <p>目标已被取消，服务端关闭了这条请求；不会产生答复记录。</p>
      </Notice>
    );
  }
  if (!answerable) {
    return (
      <Notice tone="info" title="这类请求不接受 Kowa 内答复">
        <p>{modeDetail}</p>
        <p className="mt-1">
          请求上冻结的对象版本是 <code>{task.request.object.id}@{task.request.object.version}</code>
          ：外部事实只有在核对到该对象上已结算的事实后才收束这条请求，浏览器没有「我已批准 / 已合并」的替代动作。
        </p>
      </Notice>
    );
  }
  if (task.state === 'RESOLVED') {
    return (
      <Notice tone="info" title="该请求已解决">
        <p>服务端已接受答复（见下方答复记录）；同一请求不会再有第二次答复。</p>
      </Notice>
    );
  }
  if (!canAnswer) {
    return (
      <Notice tone="info" title="当前身份无法答复该请求">
        <p>服务端没有为当前身份返回 respond 动作。是否可答复由服务端判定，浏览器不会代它放行。</p>
      </Notice>
    );
  }
  return <HumanTaskAnswer client={client} workspaceId={workspaceId} task={task} onAccepted={onAccepted} />;
}

function AcceptedAnswer({ task }: { task: HumanTask }) {
  const response = task.response;
  if (!response) {
    return <EmptyState title="没有答复记录"><p>该请求由外部事实收束，不产生 Kowa 内的答复。</p></EmptyState>;
  }
  const decision = decisionPresentation(response.decision);
  return (
    <>
      <DataList
        items={[
          { term: '答复', value: <code>{response.humanResponseId}</code> },
          { term: '决定', value: `${decision.label}：${decision.detail}` },
          { term: '答复所对的版本', value: <code>请求 v{response.requestVersion} · {response.object.kind} {response.object.id}@{response.object.version}</code> },
          { term: '提交人 / 时间', value: `${response.githubUserId} · ${formatTimestamp(response.createdAt)}` },
          { term: '答复载荷摘要', value: <code>{response.payloadDigest}</code> },
          {
            term: '答复内容',
            value: Object.keys(response.values).length === 0
              ? '（没有字段值）'
              : (
                <ul className="list-disc pl-5">
                  {Object.entries(response.values).map(([name, value]) => (
                    <li key={name} className="break-anywhere"><code>{name}</code> = {value}</li>
                  ))}
                </ul>
              ),
          },
          {
            term: '续接意图',
            value: task.continuation
              ? <code>{task.continuation.intent} · {task.continuation.state} · {task.continuation.continuationId}</code>
              : '（服务端未登记续接意图）',
          },
          { term: '解决方式', value: <code>{task.resolutionKind ?? '（未登记）'}</code> },
        ]}
      />
      <p className="mt-2 text-xs text-muted-foreground">
        PENDING 表示服务端已记录续接意图、等待流程消费；它不代表节点已推进，也不代表任何下游结果。
      </p>
    </>
  );
}
