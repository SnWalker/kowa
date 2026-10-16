import { useMemo, useRef, useState } from 'react';
import { ApiError, type ApiClient } from '../api/client';
import type {
  FixedWorkflowView,
  RecoveryKind,
  RecoveryResponse,
  RunDetail,
  WorkItemState,
} from '../api/contracts';
import { newRequestId } from '../api/session';
import { requireCsrf, useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { Field } from '../ui/Field';
import { Select } from '../ui/Select';
import { ErrorNotice, Notice } from '../ui/States';
import { Textarea } from '../ui/textarea';
import { formatTimestamp } from '../runtimes/presentation';
import { nodeCandidates, recoveryCandidates, runCandidates, type RecoveryCandidate, type RecoveryVersionGuard } from './recovery';

type Refusal = { kind: 'conflict'; submittedVersion: number; guard: RecoveryVersionGuard; candidate: RecoveryCandidate } | { kind: 'refused'; error: unknown };

/** The outcome of the last submission, kept next to the command it belongs to. */
type Outcome = { kind: 'response'; response: RecoveryResponse } | Refusal;

/**
 * Failure recovery for one run.
 *
 * `run.recovery` freezes five commands and **no action list**, so this section
 * never claims a command is authorized: every candidate is shown next to the
 * server fact it rests on, and the section says plainly that the server decides.
 * What the page does own is honesty about the outcome —
 *
 * - nothing is written while the view loads or re-reads;
 * - a submission carries one request key per intent (a duplicate click or a
 *   retry after a transport failure reaches the server once);
 * - the server's `applied` flag is shown as it is: `applied: false` is reported
 *   as *not applied*, and its `detail` is quoted rather than paraphrased;
 * - a refusal re-reads the run and keeps what the operator typed; a version
 *   conflict is never resolved by an automatic second attempt.
 */
export function RecoverySection({
  client,
  workspaceId,
  workItemId,
  run,
  workItem,
  workflow,
  workflowStatus,
  onRetryWorkflow,
  onChanged,
  onReloadWorkItem,
}: {
  client: ApiClient;
  workspaceId: string;
  workItemId: string;
  run: RunDetail;
  workItem: { state: WorkItemState; version: number; currentRunId?: string };
  workflow?: FixedWorkflowView;
  workflowStatus: 'loading' | 'ready' | 'error';
  onRetryWorkflow: () => void;
  onChanged: () => void;
  /** Re-reads the work item projection, whose version a `repair` is decided against. */
  onReloadWorkItem: () => void;
}) {
  const { state: sessionState } = useSession();
  const [selectedNode, setSelectedNode] = useState('');
  const [reason, setReason] = useState('');
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [pending, setPending] = useState<RecoveryKind | null>(null);
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });

  const candidates = useMemo(
    () => recoveryCandidates({ run, workItem, workflow }),
    [run, workItem, workflow],
  );

  /**
   * The key names the intent, not the click: the same command for the same
   * versions keeps its key across a refusal (the owner replays the recorded
   * outcome instead of running twice), while a real change is a new intent.
   */
  function requestKey(candidate: RecoveryCandidate, expectedVersion: number, forReason: string): string {
    const fingerprint = JSON.stringify({
      kind: candidate.kind,
      nodeId: candidate.nodeId ?? null,
      expectedVersion,
      reason: candidate.reasonRequired ? forReason : null,
    });
    if (keys.current.fingerprint !== fingerprint) {
      keys.current = { fingerprint, key: newRequestId('recovery') };
    }
    return keys.current.key;
  }

  async function submit(candidate: RecoveryCandidate, expectedVersion: number) {
    setOutcome(null);
    setPending(candidate.kind);
    try {
      const csrf = requireCsrf(sessionState);
      const response = await client.recoverRun(
        workspaceId,
        workItemId,
        run.workflowRunId,
        {
          kind: candidate.kind,
          ...(candidate.nodeId === undefined ? {} : { nodeId: candidate.nodeId }),
          ...(candidate.reasonRequired ? { reason } : {}),
          expectedVersion,
          requestKey: requestKey(candidate, expectedVersion, reason),
        },
        csrf,
      );
      keys.current = { fingerprint: '', key: '' };
      setOutcome({ kind: 'response', response });
      // The command may have changed the run: re-read the projection instead of
      // patching it locally.
      onChanged();
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        // The conflict is about the version the *owner* checks for this command,
        // so both projections are re-read and the notice names the version each
        // guard really holds — never a version taken from the other object.
        setOutcome({ kind: 'conflict', submittedVersion: expectedVersion, guard: candidate.versionGuard, candidate });
        if (candidate.versionGuard === 'workitem') onReloadWorkItem();
        else onChanged();
      } else {
        setOutcome({ kind: 'refused', error });
        onChanged();
      }
    } finally {
      setPending(null);
    }
  }

  /** The version the owner holds for a command's guard, read from the live projections. */
  const currentGuardVersion = (guard: RecoveryVersionGuard): number => {
    if (guard === 'workitem') return workItem.version;
    return run.version;
  };

  const runLevel = runCandidates(candidates);
  const nodeLevel = nodeCandidates(candidates, selectedNode === '' ? undefined : selectedNode);
  const selectableNodes = workflow?.nodes.filter((node) => run.nodes.some((round) => round.nodeId === node.id)) ?? [];

  return (
    <>
      <p className="mb-3 max-w-[80ch] text-sm text-muted-foreground">
        恢复动作只有服务端冻结的五种命令。本页不持有动作权限清单，因此每条命令都以它依据的服务端事实呈现，
        并明确标注<b>是否接受由服务端裁决</b>；提交后的 <code>applied</code> 与 <code>detail</code> 是服务端原文，
        本页不把它们改写成完成结论。
      </p>

      <h3 className="mb-2 text-lg font-semibold">运行级命令</h3>
      {runLevel.map((candidate) => (
        <CandidateRow
          key={candidate.kind}
          candidate={candidate}
          pending={pending === candidate.kind}
          disabled={pending !== null || !candidate.available || (candidate.reasonRequired && reason.trim() === '')}
          onCommand={() => void submit(candidate, candidate.expectedVersion)}
        >
          {candidate.reasonRequired && (
            <Field label="修复运行的原因（repair 必填）" className="mt-2 max-w-[48rem]">
              <Textarea
                aria-label="修复运行的原因"
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                disabled={pending !== null}
              />
            </Field>
          )}
        </CandidateRow>
      ))}

      <h3 className="mt-5 mb-2 text-lg font-semibold">节点级命令</h3>
      {workflowStatus !== 'ready' ? (
        <>
          <p className="text-sm text-muted-foreground" role="status">
            冻结定义{workflowStatus === 'loading' ? '正在读取' : '不可读'}：节点的定义责任不可知，本页因此不提供节点级恢复命令。
          </p>
          {workflowStatus === 'error' && (
            <div className="mt-2">
              <Button type="button" variant="secondary" size="sm" onClick={onRetryWorkflow}>重试读取冻结定义</Button>
            </div>
          )}
        </>
      ) : (
        <>
          <Field label="恢复动作作用的节点" className="max-w-[32rem]">
            <Select
              aria-label="恢复动作作用的节点"
              value={selectedNode}
              onChange={(event) => setSelectedNode(event.target.value)}
              disabled={pending !== null}
            >
              <option value="">（请选择节点）</option>
              {selectableNodes.map((node) => (
                <option key={node.id} value={node.id}>{node.id}</option>
              ))}
            </Select>
          </Field>
          {selectedNode === '' ? (
            <p className="mt-2 text-sm text-muted-foreground">
              先选择一个节点：重试与重新执行都以某个节点当前的轮次与尝试为依据，本页不替你挑一个。
            </p>
          ) : nodeLevel.length === 0 ? (
            <p className="mt-2 text-sm text-muted-foreground">
              该节点在冻结定义中没有对应的轮次事实；本页不提供无依据的命令。
            </p>
          ) : (
            nodeLevel.map((candidate) => (
              <CandidateRow
                key={`${candidate.kind}:${candidate.nodeId}`}
                candidate={candidate}
                pending={pending === candidate.kind}
                disabled={pending !== null || !candidate.available}
                onCommand={() => void submit(candidate, candidate.expectedVersion)}
              />
            ))
          )}
        </>
      )}

      {outcome !== null && outcome.kind === 'response' && <OutcomeNotice response={outcome.response} />}
      {outcome !== null && outcome.kind === 'conflict' && (() => {
        const current = currentGuardVersion(outcome.guard);
        const what = outcome.guard === 'workitem' ? '工作项' : '运行';
        return (
          <Notice tone="warning" title={`版本冲突：你提交时基于 v${outcome.submittedVersion}，服务端${what}当前为 v${current}`}>
            <p>
              服务端没有接受这次命令（该命令按{what}版本校验）。已重新读取{what}投影：请核对最新状态与版本后再决定是否重发，
              本页不会自动重试。等待时间与原因不影响服务端事实。
            </p>
            <div className="mt-2">
              <Button
                type="button"
                disabled={pending !== null || !outcome.candidate.available
                  || (outcome.candidate.reasonRequired && reason.trim() === '')}
                onClick={() => void submit(outcome.candidate, current)}
              >
                按 v{current} 重新提交相同命令
              </Button>
            </div>
          </Notice>
        );
      })()}
      {outcome !== null && outcome.kind === 'refused' && (
        <ErrorNotice title="恢复命令被服务端拒绝" error={outcome.error} />
      )}
    </>
  );
}

function CandidateRow({
  candidate,
  pending,
  disabled,
  onCommand,
  children,
}: {
  candidate: RecoveryCandidate;
  pending: boolean;
  disabled: boolean;
  onCommand: () => void;
  children?: React.ReactNode;
}) {
  return (
    <div className="mt-3 rounded-md border border-border-muted px-3 py-2">
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-foreground">{commandLabel(candidate)}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">{candidate.summary}</p>
          <p className="mt-1 text-xs text-muted-foreground">依据（服务端投影）：{candidate.basis}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            版本：提交时携带 v{candidate.expectedVersion}
            {candidate.versionGuard === 'run'
              ? '（服务端按运行版本校验，必须等于它当前持有的版本）'
              : candidate.versionGuard === 'workitem'
                ? '（服务端按工作项版本校验，必须等于它当前持有的版本）'
                : '（服务端不按版本校验该命令）'}
            。{candidate.authority}
          </p>
        </div>
        {candidate.available ? (
          <Button type="button" variant={candidate.kind === 'cancel' ? 'secondary' : 'default'} disabled={disabled} onClick={onCommand}>
            {pending ? '正在提交…' : commandLabel(candidate)}
          </Button>
        ) : (
          <p className="text-xs text-muted-foreground">当前依据不足：本页不提供该命令（服务端仍会独立裁决）。</p>
        )}
      </div>
      {children}
    </div>
  );
}

function commandLabel(candidate: RecoveryCandidate): string {
  const scope = candidate.nodeId === undefined ? '' : `节点 ${candidate.nodeId} `;
  switch (candidate.kind) {
    case 'retry':
      return `${scope}重试该尝试（retry）`;
    case 'rerun':
      return `${scope}重新执行该节点（rerun）`;
    case 'cancel':
      return '取消运行（cancel）';
    case 'repair':
      return '创建修复运行（repair）';
    default:
      return '重试知识建议（retry-knowledge-suggestion）';
  }
}

/**
 * The server's answer, quoted. `applied: false` is a recorded fact about a
 * command that did not take effect, and it is never presented as recovery.
 */
function OutcomeNotice({ response }: { response: RecoveryResponse }) {
  if (!response.applied) {
    return (
      <Notice tone="warning" title={`命令未被应用（${response.kind}）`}>
        <p>
          服务端记录该命令为未应用。运行状态仍为 <code>{response.state}</code>（版本 v{response.version}），
          记录时间 {formatTimestamp(response.recordedAt)}。
          {response.detail ? `服务端说明：${response.detail}` : '服务端未给出说明。'}
        </p>
      </Notice>
    );
  }
  return (
    <Notice tone="info" title={`服务端已应用该命令（${response.kind}）`}>
      <p>
        运行 <code>{response.workflowRunId}</code> 当前状态 <code>{response.state}</code>（版本 v{response.version}），
        记录时间 {formatTimestamp(response.recordedAt)}。
        {response.detail ? `服务端说明：${response.detail}` : '服务端未给出说明。'}
      </p>
      <p className="mt-1">
        这只是命令被应用的事实：后续轮次、裁决与交付结果仍以服务端后续投影为准，本页不作断言。
      </p>
    </Notice>
  );
}
