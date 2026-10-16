import type {
  ArtifactListResponse,
  FixedWorkflowView,
  HumanTask,
  NodeHistory,
  RunDetail,
} from '../api/contracts';
import { formatTimestamp } from '../runtimes/presentation';
import { Button } from '../ui/button';
import { DataList, Notice } from '../ui/States';
import {
  escalationRegion,
  reviewedArtifact,
  reviewedVersionCheck,
  revisionBudget,
  versionCheckPresentation,
} from './presentation';

export type ReadState = { status: 'loading' | 'ready' | 'error'; error?: unknown };
type Problem = { state: ReadState; what: string; onRetry?: () => void };

/**
 * What the human is being asked to decide *on*, read from the projections the
 * contract really serves.
 *
 * The section never assembles a document: for a knowledge confirmation the
 * frozen index and its digest come from the run projection, for a plan approval
 * the reviewed version is looked up in the published artifact list by its
 * digest, and for a revision escalation the budget comes from the frozen
 * definition. Where the contract serves no content read (an artifact itself, a
 * pull request), the page says exactly that instead of pretending to show it.
 */
export function HumanTaskBasis({
  task,
  run,
  workflow,
  artifacts,
  runState,
  workflowState,
  artifactState,
  onRetryRun,
  onRetryWorkflow,
  onRetryArtifacts,
}: {
  task: HumanTask;
  run?: RunDetail;
  workflow?: FixedWorkflowView;
  artifacts?: ArtifactListResponse;
  runState: ReadState;
  workflowState: ReadState;
  artifactState: ReadState;
  onRetryRun: () => void;
  onRetryWorkflow: () => void;
  onRetryArtifacts: () => void;
}) {
  const object = task.request.object;
  const round = roundOf(task, run);

  return (
    <>
      <p className="mb-2 max-w-[80ch] text-sm text-muted-foreground">
        下面只列出服务端投影中与该请求直接相关的事实。请求本身不携带依据正文，本页也不从别处拼装依据：
        读不到的部分按“不可读”说明，不写成空结果。
      </p>

      <h3 className="mt-2 mb-2 text-lg font-semibold">请求所在的节点轮次</h3>
      {round.status === 'described' ? (
        <DataList
          items={[
            { term: '节点', value: <code>{round.nodeId}</code> },
            { term: '轮次', value: `第 ${round.iteration} 轮（${task.nodeRunId}）` },
            { term: '已记录轮次状态', value: <code>{round.round?.state ?? '（运行投影中没有该轮次）'}</code> },
            { term: '裁决', value: <code>{round.round?.verdict ?? '未记录'}</code> },
            { term: '尝试次数', value: `${round.round?.attempts.length ?? 0} 次` },
          ]}
        />
      ) : (
        <p className="text-sm text-muted-foreground">{round.detail}</p>
      )}
      {runState.status === 'error' && (
        <ProblemLine state={runState} what="运行投影" onRetry={onRetryRun} />
      )}

      <h3 className="mt-4 mb-2 text-lg font-semibold">所审对象的依据</h3>
      {object.kind === 'knowledge_recommendation' && (
        <KnowledgeBasis task={task} run={run} runState={runState} onRetry={onRetryRun} />
      )}
      {object.kind === 'plan' && (
        <PlanBasis task={task} artifacts={artifacts} artifactState={artifactState} onRetry={onRetryArtifacts} />
      )}
      {object.kind === 'revision_escalation' && (
        <EscalationBasis task={task} run={run} workflow={workflow} runState={runState} workflowState={workflowState} onRetryWorkflow={onRetryWorkflow} />
      )}
      {object.kind !== 'knowledge_recommendation' && object.kind !== 'plan' && object.kind !== 'revision_escalation' && (
        <Notice tone="info" title="该对象类别没有面向浏览器的依据读取操作">
          <p>
            对象 <code>{object.kind}</code>：冻结合同没有为浏览器提供该对象的读取或内容操作，
            因此本页只呈现它与请求上冻结的版本标识（<code>{object.id}@{object.version}</code>），不构造任何替代视图。
          </p>
        </Notice>
      )}
    </>
  );
}

function ProblemLine({ state, what, onRetry }: Problem & { onRetry?: () => void }) {
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <p className="text-sm text-muted-foreground" role="status">
        {what}当前不可读（{state.status === 'loading' ? '正在读取' : '读取失败'}）：这不表示该事实不存在，也不表示它为空。
      </p>
      {onRetry && state.status === 'error' && (
        <Button type="button" variant="secondary" size="sm" onClick={onRetry}>重试</Button>
      )}
    </div>
  );
}

function KnowledgeBasis({
  task,
  run,
  runState,
  onRetry,
}: { task: HumanTask; run?: RunDetail; runState: ReadState; onRetry: () => void }) {
  const recommendation = run?.knowledgeRecommendation;
  const check = reviewedVersionCheck(task.request.object, recommendation?.indexDigest);
  const presentation = versionCheckPresentation(check, '该运行上的冻结索引');
  return (
    <>
      <DataList
        items={[
          { term: '冻结索引', value: recommendation ? <code>{recommendation.indexPath} · {recommendation.indexDigest}</code> : '（不可读）' },
          { term: '所审版本核对', value: presentation.detail },
          { term: '推荐时间', value: recommendation ? formatTimestamp(recommendation.recommendedAt) : '（不可读）' },
        ]}
      />
      {recommendation ? (
        <>
          <p className="mt-3 mb-2 text-sm text-muted-foreground">
            冻结索引条目共 {recommendation.candidates.length} 条（required{' '}
            {recommendation.candidates.filter((candidate) => candidate.required).length} 条）：
          </p>
          <ul className="list-disc pl-5 text-sm">
            {recommendation.candidates.map((candidate) => (
              <li key={candidate.path} className="break-anywhere py-0.5">
                <code>{candidate.path}</code> · {candidate.scope} · {candidate.digest}
                {candidate.required ? ' · required' : ''}
              </li>
            ))}
            {recommendation.candidates.length === 0 && <li>索引内没有条目（成功读取后的空索引）。</li>}
          </ul>
        </>
      ) : (
        <ProblemLine state={runState} what="知识推荐投影" onRetry={onRetry} />
      )}
    </>
  );
}

function PlanBasis({
  task,
  artifacts,
  artifactState,
  onRetry,
}: { task: HumanTask; artifacts?: ArtifactListResponse; artifactState: ReadState; onRetry: () => void }) {
  const reviewed = reviewedArtifact(artifacts?.artifacts ?? [], task.request.object.version);
  return (
    <>
      {reviewed ? (
        <DataList
          items={[
            { term: '所审交付物', value: <code>{reviewed.artifactId}</code> },
            { term: '内容摘要', value: <code>{reviewed.digest}</code> },
            { term: '种类 / 类型', value: `${reviewed.kind} · ${reviewed.mediaType} · ${reviewed.size} 字节` },
            { term: '产出者', value: <code>{reviewed.producerKind} · {reviewed.producerId}</code> },
            { term: '发布时间', value: formatTimestamp(reviewed.createdAt) },
          ]}
        />
      ) : (
        <Notice tone="info" title="所审版本未出现在已发布产物清单中">
          <p>
            已发布产物清单里没有摘要等于所审版本 <code>{task.request.object.version}</code> 的条目。
            这不等于该版本不存在，也不等于它已被替换：本页只按清单核对，不臆造内容。
          </p>
        </Notice>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        冻结合同按引用展示交付物（身份 + 摘要 + 生命周期），不提供浏览器端的内容读取操作，因此这里没有正文，也没有下载链接。
      </p>
      {artifactState.status !== 'ready' && <ProblemLine state={artifactState} what="产物清单" onRetry={onRetry} />}
    </>
  );
}

function EscalationBasis({
  task,
  run,
  workflow,
  runState,
  workflowState,
  onRetryWorkflow,
}: {
  task: HumanTask;
  run?: RunDetail;
  workflow?: FixedWorkflowView;
  runState: ReadState;
  workflowState: ReadState;
  onRetryWorkflow: () => void;
}) {
  const region = escalationRegion(task.workflowRunId ?? '', task.request.object.id);
  const budget = revisionBudget(workflow?.nodes, region.nodeId ?? '', run?.nodes ?? []);
  return (
    <>
      <DataList
        items={[
          { term: '区域', value: <code>{task.request.object.id}</code> },
          { term: '区域解析', value: region.detail },
          { term: '触发轮次', value: `第 ${task.request.object.version} 轮` },
          { term: '自动返修预算', value: budget.maxAutoRevisions === undefined ? '（未登记或不可读）' : `${budget.maxAutoRevisions} 次` },
          { term: '预算目标节点', value: budget.targetNodeId ? <code>{budget.targetNodeId}</code> : '（未登记或不可读）' },
          { term: '该目标节点已记录轮次', value: `${budget.recordedRounds} 轮` },
        ]}
      />
      <p className="mt-2 text-xs text-muted-foreground">{budget.detail}</p>
      {workflowState.status !== 'ready' && <ProblemLine state={workflowState} what="冻结定义" onRetry={onRetryWorkflow} />}
      {runState.status !== 'ready' && <ProblemLine state={runState} what="运行投影" onRetry={undefined} />}
    </>
  );
}

type RoundDescription =
  | { status: 'described'; nodeId: string; iteration: number; round?: NodeHistory }
  | { status: 'unknown'; detail: string };

/**
 * The node round a request belongs to, read out of the owner's own
 * `nodeRunId` shape `<runId>:node:<nodeId>:<iteration>`. An identifier that
 * does not carry that shape is reported as unlocatable rather than guessed at.
 */
function roundOf(task: HumanTask, run: RunDetail | undefined): RoundDescription {
  const id = task.nodeRunId;
  if (id === undefined) {
    return { status: 'unknown', detail: '该请求没有携带节点轮次标识（例如由关闭用例创建的任务），因此没有对应的轮次事实。' };
  }
  const prefix = task.workflowRunId === undefined ? undefined : `${task.workflowRunId}:node:`;
  if (prefix === undefined || !id.startsWith(prefix)) {
    return {
      status: 'unknown',
      detail: `节点轮次标识 ${id} 不携带本运行的五段式前缀，本页无从定位其轮次，不猜测。`,
    };
  }
  const rest = id.slice(prefix.length);
  const separator = rest.lastIndexOf(':');
  const nodeId = separator === -1 ? '' : rest.slice(0, separator);
  const iteration = separator === -1 ? Number.NaN : Number(rest.slice(separator + 1));
  if (nodeId === '' || !Number.isInteger(iteration)) {
    return { status: 'unknown', detail: `节点轮次标识 ${id} 的形状无法解析，本页不猜测其节点与轮次。` };
  }
  const round = run?.nodes.find((candidate) => candidate.nodeId === nodeId && candidate.iteration === iteration);
  return { status: 'described', nodeId, iteration, round };
}
