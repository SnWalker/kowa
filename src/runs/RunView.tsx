import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { ArtifactListResponse, FixedWorkflowView, RunDetail } from '../api/contracts';
import type { ApiClient } from '../api/client';
import { useResource } from '../api/useResource';
import { formatTimestamp } from '../runtimes/presentation';
import { Button } from '../ui/button';
import { LoadingState } from '../ui/Feedback';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';
import { KnowledgeBindingSection } from '../workitems/KnowledgeBinding';
import { ArtifactSection } from './ArtifactSection';
import { DagLayoutError, layoutDag, type DagLayout } from './dag';
import { LiveStatus } from './LiveStatus';
import { NodePanel, RoundsSection } from './NodePanel';
import { groupRoundsByNode, runStatePresentation, summariseRounds } from './presentation';
import { useRunLive } from './useRunLive';
import { WorkflowGraph } from './WorkflowGraph';

/**
 * One delivery run: its identity, the frozen workflow it is executing, every
 * node round it produced, and the artifacts it published.
 *
 * Three independent reads, and each one fails on its own: the run projection
 * (live), the frozen definition (per Workspace) and the published artifacts
 * (per Work Item). A failure in one never blanks the others, and none of them
 * is replaced by a local guess.
 *
 * The selected node lives in the URL, so a view is a link the operator can send
 * on — and a reload restores exactly the node that was open.
 */
export function RunView({
  client,
  workspaceId,
  workItemId,
  runId,
  scope,
  generation,
}: {
  client: ApiClient;
  workspaceId: string;
  workItemId: string;
  runId: string;
  scope: string;
  generation: number;
}) {
  const live = useRunLive({
    load: (signal) => client.getRunDetail(workspaceId, workItemId, runId, signal),
    scope: `${scope}:run:${runId}`,
    generation,
  });
  const workflow = useResource(
    (signal) => client.getFixedWorkflow(workspaceId, signal),
    [workspaceId, generation],
    { scope: `${scope}:workflow`, generation },
  );
  const artifacts = useResource(
    (signal) => client.listArtifacts(workspaceId, workItemId, signal),
    [workspaceId, workItemId, generation],
    { scope: `${scope}:artifacts`, generation },
  );
  const [params, setParams] = useSearchParams();
  const selectedNodeId = params.get('node') ?? undefined;

  function selectNode(nodeId: string) {
    const next = new URLSearchParams(params);
    next.set('node', nodeId);
    // The selection is a view, not a navigation step: replacing keeps the back
    // button on the page the operator came from.
    setParams(next, { replace: true });
  }

  const detail = live.detail;

  if (live.status === 'loading') return <Section title="交付运行" id="run"><LoadingState /></Section>;
  if (live.status === 'error' || !detail) {
    return (
      <Section title="交付运行" id="run">
        <ErrorNotice title="无法读取运行投影" error={live.error} />
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={live.reconnect}>重试</Button>
        </div>
      </Section>
    );
  }

  return (
    <>
      <Section title="交付运行" id="run">
        <RunIdentity detail={detail} />
        <LiveStatus disconnected={live.disconnected} lastObservedAt={live.lastObservedAt} onRetry={live.reconnect} />
      </Section>
      <WorkflowSection
        workflow={workflow.data}
        status={workflow.status}
        error={workflow.error}
        onRetry={workflow.reload}
        detail={detail}
        selectedNodeId={selectedNodeId}
        onSelect={selectNode}
      />
      <RoundStats detail={detail} />
      <NodeDetailSection
        workflow={workflow.data}
        detail={detail}
        artifacts={artifacts.data}
        selectedNodeId={selectedNodeId}
        onSelect={selectNode}
      />
      <Section title="运行轮次与历史" id="run-rounds">
        {workflow.data ? (
          <RoundsSection nodes={workflow.data.nodes} roundsByNode={groupRoundsByNode(detail.nodes)} />
        ) : (
          <p className="text-sm text-muted-foreground">
            冻结定义不可用，无法把节点轮次对应到定义中的节点；下方运行投影仍按服务端返回原样展示。
          </p>
        )}
      </Section>
      <Section title="产物与证据" id="run-artifacts">
        <ArtifactRead state={artifacts.status} data={artifacts.data} error={artifacts.error} onRetry={artifacts.reload} />
      </Section>
      <KnowledgeBindingSection
        client={client}
        workspaceId={workspaceId}
        run={detail}
        generation={generation}
        onChanged={live.reconnect}
      />
    </>
  );
}

function RunIdentity({ detail }: { detail: RunDetail }) {
  const state = runStatePresentation(detail.state);
  return (
    <DataList
      items={[
        { term: '运行', value: <code>{detail.workflowRunId}</code> },
        { term: '运行状态', value: <Badge tone={state.tone}>{state.label}</Badge> },
        {
          term: '冻结定义',
          value: (
            <code>
              {detail.definitionName}@{detail.definitionVersion} · {detail.definitionDigest}
            </code>
          ),
        },
        {
          term: 'Web 发起人 / Runner 机器身份',
          value: `发起 ${detail.startedByGitHubUserId} · 执行 ${detail.gitExecutionUserId ?? '（未登记）'}`,
        },
        {
          term: '运行最后一次状态变更',
          value: detail.watermark.runUpdatedAt ? formatTimestamp(detail.watermark.runUpdatedAt) : '服务端未登记',
        },
      ]}
    />
  );
}

function WorkflowSection({
  workflow,
  status,
  error,
  onRetry,
  detail,
  selectedNodeId,
  onSelect,
}: {
  workflow?: FixedWorkflowView;
  status: 'loading' | 'ready' | 'error';
  error?: unknown;
  onRetry: () => void;
  detail: RunDetail;
  selectedNodeId?: string;
  onSelect: (nodeId: string) => void;
}) {
  const roundsByNode = useMemo(() => groupRoundsByNode(detail.nodes), [detail.nodes]);
  const layout = useMemo<{ value: DagLayout } | { failure: unknown } | undefined>(() => {
    if (!workflow) return undefined;
    try {
      return { value: layoutDag(workflow.nodes, workflow.order) };
    } catch (failure) {
      return { failure };
    }
  }, [workflow]);

  return (
    <Section
      title="固定流程 DAG"
      id="dag"
      actions={
        workflow ? (
          <span className="text-xs text-muted-foreground">
            <code>
              {workflow.name}@{workflow.version}
            </code>
          </span>
        ) : undefined
      }
    >
      {status === 'loading' && <LoadingState />}
      {status === 'error' && (
        <>
          <ErrorNotice title="无法读取冻结定义" error={error} />
          <div className="mt-3">
            <Button type="button" variant="secondary" onClick={onRetry}>重试</Button>
          </div>
        </>
      )}
      {workflow && layout && 'failure' in layout && (
        <Notice tone="error" title="冻结定义无法绘制">
          <p>
            服务端返回的定义视图无法被精确绘制，因此这里不画近似图：
            {layout.failure instanceof DagLayoutError ? layout.failure.message : '定义视图与拓扑序不一致'}。
          </p>
        </Notice>
      )}
      {workflow && layout && 'value' in layout && (
        <>
          <WorkflowGraph
            nodes={workflow.nodes}
            layout={layout.value}
            roundsByNode={roundsByNode}
            selectedNodeId={selectedNodeId}
            onSelect={onSelect}
          />
          <p className="mt-2 text-xs text-muted-foreground">
            定义摘要 <code>{workflow.definitionDigest}</code>（运行准入时冻结为{' '}
            <code>{detail.definitionDigest}</code>
            ）{workflow.definitionDigest === detail.definitionDigest
              ? '：与运行冻结的定义一致。'
              : '：与运行冻结的定义不一致，请以运行冻结的版本为准并核对服务端。'}
          </p>
        </>
      )}
    </Section>
  );
}

function RoundStats({ detail }: { detail: RunDetail }) {
  const summary = summariseRounds(detail.nodes);
  return (
    <Section title="本轮状态统计" id="run-stats">
      <DataList
        items={[
          { term: '已物化节点轮次', value: `${summary.materialised} 条` },
          { term: '已决（DECIDED）', value: `${summary.decided} 条` },
          { term: '执行失败（EXECUTION_FAILED）', value: `${summary.executionFailed} 条` },
          { term: '跳过（SKIPPED）', value: `${summary.skipped} 条` },
          { term: '等待人工（WAITING_HUMAN）', value: `${summary.waitingHuman} 条` },
          { term: '执行中（RUNNING）', value: `${summary.running} 条` },
          { term: '未就绪 / 可执行 / 已取消', value: `${summary.pending + summary.canceled} 条` },
        ]}
      />
      <p className="mt-2 text-xs text-muted-foreground">
        这些是服务端已记录轮次的计数，不是完成度：它们不推进 DAG，也不代表任何节点已获业务裁决。跳过与执行失败始终单独列出。
      </p>
    </Section>
  );
}

function NodeDetailSection({
  workflow,
  detail,
  artifacts,
  selectedNodeId,
  onSelect,
}: {
  workflow?: FixedWorkflowView;
  detail: RunDetail;
  artifacts?: ArtifactListResponse;
  selectedNodeId?: string;
  onSelect: (nodeId: string) => void;
}) {
  const node = workflow?.nodes.find((candidate) => candidate.id === selectedNodeId);
  const rounds = selectedNodeId ? (groupRoundsByNode(detail.nodes).get(selectedNodeId) ?? []) : [];
  const artifactIndex = new Map((artifacts?.artifacts ?? []).map((artifact) => [artifact.artifactId, artifact]));

  return (
    <Section title="节点详情" id="run-node">
      {selectedNodeId === undefined ? (
        <p className="text-sm text-muted-foreground">
          在上方 DAG 中选择一个节点查看它的定义责任、当前轮次与历史轮次。选择会写入 URL 的 <code>node</code> 参数，可直接分享或刷新恢复。
        </p>
      ) : node === undefined ? (
        <Notice tone="warning" title="所选节点不在冻结定义中">
          <p>
            URL 指定的节点 <code>{selectedNodeId}</code> 不在服务端返回的冻结定义里。
          </p>
          <div className="mt-2">
            <Button type="button" variant="secondary" onClick={() => onSelect(workflow?.order[0] ?? '')}>
              回到定义中的第一个节点
            </Button>
          </div>
        </Notice>
      ) : (
        <NodePanel node={node} rounds={rounds} artifacts={artifactIndex} />
      )}
    </Section>
  );
}

function ArtifactRead({
  state,
  data,
  error,
  onRetry,
}: {
  state: 'loading' | 'ready' | 'error';
  data?: ArtifactListResponse;
  error?: unknown;
  onRetry: () => void;
}) {
  if (state === 'loading') return <LoadingState />;
  if (state === 'error' || !data) {
    return (
      <>
        <ErrorNotice title="无法读取产物清单" error={error} />
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={onRetry}>重试</Button>
        </div>
      </>
    );
  }
  return <ArtifactSection artifacts={data.artifacts} runBindings={data.runBindings} />;
}
