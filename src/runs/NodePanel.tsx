import type { ArtifactRecord, FixedWorkflowNode, NodeAttempt, NodeHistory } from '../api/contracts';
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '../ui/table';
import { Badge, DataList, Notice } from '../ui/States';
import {
  attemptKindPresentation,
  attemptStatePresentation,
  currentRound,
  historicalRounds,
  nodeResponsibility,
  nodeStatePresentation,
  nodeTypePresentation,
  nodeVerdictPresentation,
} from './presentation';

/**
 * One node, read in full: what the frozen definition says it is, and every
 * round the run produced for it.
 *
 * The current round and the earlier ones are printed with the same fields and
 * are told apart by their labels alone — an earlier round is never given the
 * current round's heading, and its verdict stays visible instead of being
 * replaced by the later one.
 */
export function NodePanel({
  node,
  rounds,
  artifacts,
}: {
  node: FixedWorkflowNode;
  rounds: NodeHistory[];
  artifacts: Map<string, ArtifactRecord>;
}) {
  const round = currentRound(rounds);
  const history = historicalRounds(rounds);
  const state = nodeStatePresentation(round?.state);
  const verdict = nodeVerdictPresentation(round?.verdict);
  const type = nodeTypePresentation(node.type);
  const responsibility = nodeResponsibility(node);

  return (
    <>
      <DataList
        items={[
          { term: '节点', value: <code>{node.id}</code> },
          { term: '类型', value: `${type.label}（${node.type}）` },
          { term: '责任', value: responsibility ? <code>{responsibility}</code> : '（定义未声明操作、能力或人工任务）' },
          { term: '必需性', value: node.mandatory ? '必需节点（mandatory）' : '非必需节点' },
          {
            term: '依赖',
            value: node.dependsOn.length === 0 ? '无（起始节点）' : <code>{node.dependsOn.join('、')}</code>,
          },
          {
            term: '上游输入绑定',
            value:
              node.inputs.length === 0 ? (
                '无'
              ) : (
                <ul className="space-y-0.5">
                  {node.inputs.map((input) => (
                    <li key={input.key}>
                      <code>{input.key}</code> ← <code>{input.producerNodeId}.{input.output}</code>（{input.type}
                      {input.required ? '，必需' : '，可选'}）
                    </li>
                  ))}
                </ul>
              ),
          },
          {
            term: '自动返修预算',
            value: node.revision ? (
              <>
                退回 <code>{node.revision.targetNodeId}</code>，最多 <code>{node.revision.maxAutoRevisions}</code> 轮自动修订
              </>
            ) : (
              '该节点不设自动返修'
            ),
          },
          { term: '本轮当前状态', value: <Badge tone={state.tone}>{state.label}</Badge> },
          { term: '本轮当前裁决', value: verdict ? <code>{verdict.label}</code> : '服务端尚未记录裁决' },
        ]}
      />
      <p className="mt-2 text-xs text-muted-foreground">{type.detail}</p>
      <p className="mt-1 text-xs text-muted-foreground">{state.detail}</p>

      {history.length > 0 && (
        <Notice tone="info" title={`该节点有 ${history.length} 条历史轮次`}>
          <p>历史轮次只读保留：它们的裁决是各自轮次的事实，不代表当前轮次的结果。完整列表见下方「运行轮次与历史」。</p>
        </Notice>
      )}

      {round ? (
        <div className="mt-4">
          <h3 className="mb-2 text-sm font-semibold text-foreground">
            当前轮次（第 {round.iteration} 轮）的 Task 尝试
          </h3>
          <RoundFacts round={round} artifacts={artifacts} />
        </div>
      ) : (
        <p className="mt-4 text-sm text-muted-foreground">本次运行尚未为该节点生成节点轮次。</p>
      )}
    </>
  );
}

/** The rounds and attempts of one node round, current or historical. */
export function RoundFacts({
  round,
  artifacts,
}: {
  round: NodeHistory;
  artifacts: Map<string, ArtifactRecord>;
}) {
  const state = nodeStatePresentation(round.state);
  const verdict = nodeVerdictPresentation(round.verdict);
  return (
    <>
      <DataList
        items={[
          { term: '执行状态', value: <Badge tone={state.tone}>{state.label}</Badge> },
          { term: '业务裁决', value: verdict ? <code>{verdict.label}</code> : '服务端未记录裁决（不代表任一方向）' },
          { term: '冻结输入摘要', value: <code>{round.inputDigest}</code> },
          {
            term: '输出',
            value:
              round.output === undefined ? (
                '服务端未发布输出'
              ) : (
                <code className="whitespace-pre-wrap">{JSON.stringify(round.output)}</code>
              ),
          },
        ]}
      />
      {round.attempts.length === 0 ? (
        <p className="mt-2 text-sm text-muted-foreground">
          该轮次没有 Task 尝试：它要么由控制面自身完成，要么尚未派发。
        </p>
      ) : (
        <div className="mt-2 overflow-x-auto">
          <Table>
            <TableCaption className="caption-top mb-2 mt-0 text-left text-xs text-muted-foreground">
              第 {round.iteration} 轮的 Task 尝试（执行事实；COMPLETED 不等于业务裁决通过）
            </TableCaption>
            <TableHeader className="bg-muted">
              <TableRow>
                <TableHead>尝试</TableHead>
                <TableHead>Task</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>输入摘要</TableHead>
                <TableHead>结果摘要</TableHead>
                <TableHead>Runtime</TableHead>
                <TableHead>租约</TableHead>
                <TableHead>产物引用</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {round.attempts.map((attempt) => (
                <AttemptRow key={attempt.taskId} attempt={attempt} artifacts={artifacts} />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}

function AttemptRow({ attempt, artifacts }: { attempt: NodeAttempt; artifacts: Map<string, ArtifactRecord> }) {
  const state = attemptStatePresentation(attempt.state);
  const kind = attemptKindPresentation(attempt.attemptKind);
  return (
    <TableRow>
      <TableCell className="whitespace-normal">
        第 {attempt.attempt} 次
        <span className="block text-xs text-muted-foreground">{kind.label}</span>
      </TableCell>
      <TableCell className="whitespace-normal">
        <code>{attempt.taskId}</code>
      </TableCell>
      <TableCell className="whitespace-normal">
        <Badge tone={state.tone}>{state.label}</Badge>
      </TableCell>
      <TableCell className="whitespace-normal">
        <code className="block max-w-[16rem] truncate">{attempt.inputDigest}</code>
      </TableCell>
      <TableCell className="whitespace-normal">
        {attempt.resultDigest ? <code className="block max-w-[16rem] truncate">{attempt.resultDigest}</code> : '—'}
      </TableCell>
      <TableCell className="whitespace-normal">
        {attempt.runtimeId ? (
          <>
            <code>{attempt.runtimeId}</code>
            <span className="block text-xs text-muted-foreground">
              世代 {attempt.runtimeEpoch ?? '—'} · fencing {attempt.fencingToken ?? '—'}
            </span>
          </>
        ) : (
          '—'
        )}
      </TableCell>
      <TableCell className="whitespace-normal">{attempt.leaseId ? <code>{attempt.leaseId}</code> : '—'}</TableCell>
      <TableCell className="whitespace-normal">
        {attempt.artifactIds.length === 0 ? (
          '无'
        ) : (
          <ul className="space-y-1">
            {attempt.artifactIds.map((artifactId) => {
              const artifact = artifacts.get(artifactId);
              return (
                <li key={artifactId}>
                  <code>{artifactId}</code>
                  <span className="block text-xs text-muted-foreground">
                    {artifact
                      ? `${artifact.kind} · ${artifact.state} · 摘要 ${artifact.digest}`
                      : '该引用未出现在已发布清单中（服务端未发布或已被回收）'}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </TableCell>
    </TableRow>
  );
}

/** Every node the run materialised, current round first, earlier ones below. */
export function RoundsSection({
  nodes,
  roundsByNode,
}: {
  nodes: readonly FixedWorkflowNode[];
  roundsByNode: Map<string, NodeHistory[]>;
}) {
  const materialised = nodes.filter((node) => (roundsByNode.get(node.id) ?? []).length > 0);
  const unmaterialised = nodes.filter((node) => (roundsByNode.get(node.id) ?? []).length === 0);

  if (materialised.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        本次运行尚未物化任何节点轮次。冻结定义共 {nodes.length} 个节点，它们全部处于「未物化」状态。
      </p>
    );
  }

  return (
    <div className="space-y-5">
      {materialised.map((node) => {
        const rounds = roundsByNode.get(node.id) ?? [];
        const round = currentRound(rounds);
        const history = historicalRounds(rounds);
        return (
          <div key={node.id}>
            <h3 className="mb-1 text-sm font-semibold text-foreground">
              <code>{node.id}</code>
            </h3>
            {round && (
              <RoundBlock label={`当前轮次（第 ${round.iteration} 轮）`} round={round} />
            )}
            {history.map((entry) => (
              <RoundBlock key={entry.iteration} label={`历史轮次（第 ${entry.iteration} 轮）`} round={entry} historical />
            ))}
          </div>
        );
      })}
      {unmaterialised.length > 0 && (
        <p className="text-xs text-muted-foreground">
          另有 {unmaterialised.length} 个冻结定义节点尚未物化，因此不在此列：
          <code className="ml-1">{unmaterialised.map((node) => node.id).join('、')}</code>
        </p>
      )}
    </div>
  );
}

function RoundBlock({ label, round, historical = false }: { label: string; round: NodeHistory; historical?: boolean }) {
  const state = nodeStatePresentation(round.state);
  const verdict = nodeVerdictPresentation(round.verdict);
  return (
    <div className={`mb-2 rounded-md border px-3 py-2 ${historical ? 'border-dashed border-border-muted' : 'border-border'}`}>
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="mt-1 flex flex-wrap items-center gap-2 text-sm">
        <Badge tone={state.tone}>{state.label}</Badge>
        <span className="text-muted-foreground">业务裁决</span>
        {verdict ? <code>{verdict.label}</code> : <span className="text-muted-foreground">未记录</span>}
      </p>
      <p className="mt-1 text-xs text-muted-foreground">
        冻结输入摘要 <code>{round.inputDigest}</code> · Task 尝试 {round.attempts.length} 次
      </p>
    </div>
  );
}
