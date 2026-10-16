import { useRef, useState } from 'react';
import { ApiError, type ApiClient } from '../api/client';
import type {
  HumanTask,
  KnowledgeCandidate,
  KnowledgeRecommendation,
  RunDetail,
} from '../api/contracts';
import { newRequestId } from '../api/session';
import { useResource } from '../api/useResource';
import { requireCsrf, useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { CheckboxField, Field } from '../ui/Field';
import { EmptyState, LoadingState } from '../ui/Feedback';
import { Input } from '../ui/input';
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
import type { VersionConflict } from '../ui/version-conflict';
import { formatTimestamp } from '../runtimes/presentation';
import {
  canRespond,
  formatSelection,
  humanTaskStatePresentation,
  orderedSelection,
  parseSelection,
  recommendationStatePresentation,
  toggledSelection,
} from './presentation';

/**
 * The versioned knowledge binding of one admitted run.
 *
 * The frozen index is the whole candidate set (`knowledgeRecommendation`); the
 * page filters it locally, never invents entries outside it and never hides a
 * read failure as an empty result. Confirmation goes through the HumanTask the
 * server created, using its request version and object binding — never a
 * rebuilt one.
 */
export function KnowledgeBindingSection({
  client,
  workspaceId,
  run,
  generation,
  onChanged,
}: {
  client: ApiClient;
  workspaceId: string;
  run: RunDetail;
  generation: number;
  onChanged: () => void;
}) {
  const recommendation = run.knowledgeRecommendation;
  const scope = `${workspaceId}:${run.workflowRunId}:${run.workItemId}`;

  if (!recommendation) {
    return (
      <Section title="知识绑定" id="knowledge">
        <Notice tone="warning" title="知识推荐尚未生成">
          <p>
            该运行当前没有知识推荐投影（例如冻结索引读取失败，或运行尚未推进到知识步骤）。
            读取失败不会被显示成“无知识”；请重新读取运行投影后再判断。
          </p>
        </Notice>
      </Section>
    );
  }
  const state = recommendationStatePresentation(recommendation.state);
  return (
    <Section title="知识绑定" id="knowledge">
      <DataList
        items={[
          { term: '确认状态', value: <Badge tone={state.tone}>{state.label}</Badge> },
          { term: '冻结知识提交', value: <code>{recommendation.knowledgeRepositoryId}@{recommendation.knowledgeCommitOid}</code> },
          { term: '冻结索引', value: <code>{recommendation.indexPath} · {recommendation.indexDigest}</code> },
          { term: '推荐时间', value: formatTimestamp(recommendation.recommendedAt) },
        ]}
      />
      <p className="mt-2 text-xs text-muted-foreground">{state.detail}</p>
      {recommendation.state === 'CONFIRMED' ? (
        <ConfirmedBinding client={client} workspaceId={workspaceId} recommendation={recommendation} scope={scope} generation={generation} />
      ) : recommendation.humanTaskId ? (
        <KnowledgeConfirmation
          key={scope}
          client={client}
          workspaceId={workspaceId}
          recommendation={recommendation}
          humanTaskId={recommendation.humanTaskId}
          scope={scope}
          generation={generation}
          onChanged={onChanged}
        />
      ) : (
        <Notice tone="warning" title="缺少确认请求">
          <p>推荐未携带确认任务标识；请重新读取运行投影，等待服务端补建确认请求后再确认。</p>
        </Notice>
      )}
    </Section>
  );
}

/** The confirmed binding: the binding identity plus the accepted human action. */
function ConfirmedBinding({
  client,
  workspaceId,
  recommendation,
  scope,
  generation,
}: {
  client: ApiClient;
  workspaceId: string;
  recommendation: KnowledgeRecommendation;
  scope: string;
  generation: number;
}) {
  const humanTaskId = recommendation.humanTaskId;
  const task = useResource(
    (signal) => (humanTaskId ? client.getHumanTask(workspaceId, humanTaskId, signal) : Promise.resolve(undefined)),
    [workspaceId, humanTaskId, scope],
    { scope, generation },
  );
  const response = task.data?.humanTask.response;
  return (
    <>
      <h3 className="mt-4 mb-2 text-lg font-semibold">冻结结果</h3>
      <DataList
        items={[
          { term: '快照', value: recommendation.snapshotId ?? '（未返回）' },
          { term: '绑定事件', value: recommendation.bindingEventId ?? '（未返回）' },
          { term: '有效输入', value: recommendation.effectiveInputId ?? '（未返回）' },
          { term: '确认时间', value: recommendation.confirmedAt ? formatTimestamp(recommendation.confirmedAt) : '（未返回）' },
        ]}
      />
      <h3 className="mt-4 mb-2 text-lg font-semibold">人工动作</h3>
      {task.status === 'loading' && <LoadingState />}
      {task.status === 'error' && <ErrorNotice title="无法读取确认请求" error={task.error} />}
      {task.status === 'ready' && task.data === undefined && <p className="text-sm text-muted-foreground">推荐未携带确认任务标识；仅显示绑定事实。</p>}
      {task.status === 'ready' && task.data !== undefined && (
        <>
          <DataList
            items={[
              { term: '确认请求', value: `${task.data.humanTask.humanTaskId} · ${humanTaskStatePresentation(task.data.humanTask.state).label}` },
              {
                term: '已接受选择',
                value: response
                  ? (() => {
                      const paths = parseSelection(response.values.selection ?? '');
                      return paths.length === 0 ? '（空选择：索引内没有条目）' : paths.join('、');
                    })()
                  : '（没有已接受的答复）',
              },
              {
                term: '答复记录',
                value: response
                  ? `${response.humanResponseId} · ${response.decision} · 由 ${response.githubUserId} 于 ${formatTimestamp(response.createdAt)} 提交 · ${response.payloadDigest}`
                  : '（没有已接受的答复）',
              },
            ]}
          />
          {response && (
            <p className="mt-2 text-xs text-muted-foreground">
              同一内容再次确认会产生新的人工动作记录，不会覆盖这次答复；绑定事实由服务端持有。
            </p>
          )}
        </>
      )}
      <h3 className="mt-4 mb-2 text-lg font-semibold">冻结索引条目</h3>
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
  );
}

/**
 * The explicit confirmation. Removal is limited to the frozen index and a
 * `required` entry has no removal control at all; the payload therefore cannot
 * even express a selection the owner refuses.
 */
function KnowledgeConfirmation({
  client,
  workspaceId,
  recommendation,
  humanTaskId,
  scope,
  generation,
  onChanged,
}: {
  client: ApiClient;
  workspaceId: string;
  recommendation: KnowledgeRecommendation;
  humanTaskId: string;
  scope: string;
  generation: number;
  onChanged: () => void;
}) {
  const { state: sessionState } = useSession();
  const task = useResource(
    (signal) => client.getHumanTask(workspaceId, humanTaskId, signal),
    [workspaceId, humanTaskId, scope],
    { scope, generation },
  );
  // A conflict re-read replaces the baseline the refused answer was built on;
  // a newer server read always wins over a stale local copy.
  const [override, setOverride] = useState<{ base: HumanTask; value: HumanTask } | null>(null);
  const current: HumanTask | undefined =
    task.data && override && override.base === task.data.humanTask ? override.value : task.data?.humanTask;
  const [selected, setSelected] = useState<ReadonlySet<string>>(
    () => new Set(recommendation.candidates.map((candidate) => candidate.path)),
  );
  const [filter, setFilter] = useState('');
  const [failure, setFailure] = useState<unknown>(null);
  const [conflict, setConflict] = useState<VersionConflict | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });

  function requestKey(fingerprint: unknown): string {
    const serialized = JSON.stringify(fingerprint);
    if (keys.current.fingerprint !== serialized) {
      keys.current = { fingerprint: serialized, key: newRequestId('knowledge') };
    }
    return keys.current.key;
  }

  if (task.status === 'loading') return <LoadingState />;
  if (task.status === 'error') {
    return (
      <>
        <ErrorNotice title="无法读取确认请求" error={task.error} />
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={task.reload}>重试</Button>
        </div>
      </>
    );
  }
  if (!current) return <LoadingState />;

  // The answer targets exactly this frozen request: version and object binding
  // come from `human.detail`, never from the recommendation.
  const request = current.request;
  const paths = orderedSelection(recommendation.candidates, selected);
  const visible = recommendation.candidates.filter((candidate) => {
    const needle = filter.trim().toLowerCase();
    if (needle === '') return true;
    return candidate.path.toLowerCase().includes(needle) || candidate.scope.toLowerCase().includes(needle);
  });
  const requiredCount = recommendation.candidates.filter((candidate) => candidate.required).length;
  const taskState = humanTaskStatePresentation(current.state);
  const actionable = canRespond(current);

  /**
   * The accepted answer only records the human action; the control plane then
   * consumes the continuation and freezes the binding. Observing that real
   * transition means re-reading the run projection a few times — bounded, and
   * never rendered as a progress the server did not report.
   */
  async function awaitBinding(): Promise<'confirmed' | 'unchanged'> {
    for (let attempt = 0; attempt < 8; attempt += 1) {
      await new Promise((resolve) => setTimeout(resolve, 400));
      try {
        const detail = await client.getRunDetail(workspaceId, recommendation.workItemId, recommendation.workflowRunId);
        if (detail.knowledgeRecommendation?.state === 'CONFIRMED') return 'confirmed';
      } catch {
        // A failed re-read is not proof of anything: keep the accepted answer
        // and let the operator retry the read.
      }
    }
    return 'unchanged';
  }

  async function answer(expectedVersion: number) {
    setFailure(null);
    setStatus(null);
    setPending(true);
    const values: Record<string, string> = { decision: 'approved' };
    const selection = formatSelection(orderedSelection(recommendation.candidates, selected));
    if (selection !== '') values.selection = selection;
    try {
      const csrf = requireCsrf(sessionState);
      const resolved = await client.answerHumanTask(
        workspaceId,
        humanTaskId,
        {
          requestKey: requestKey({ expectedVersion, values }),
          requestVersion: expectedVersion,
          object: request.object,
          decision: 'approved',
          values,
        },
        csrf,
      );
      keys.current = { fingerprint: '', key: '' };
      setConflict(null);
      setOverride({ base: task.data?.humanTask ?? resolved.humanTask, value: resolved.humanTask });
      setStatus(
        `确认已提交（${resolved.humanTask.response?.humanResponseId ?? resolved.humanTask.humanTaskId}）；` +
          '服务端接受答复后由控制面固化冻结绑定，正在重新读取运行投影…',
      );
      const outcome = await awaitBinding();
      onChanged();
      if (outcome === 'confirmed') {
        setStatus(`确认已提交（${resolved.humanTask.response?.humanResponseId ?? resolved.humanTask.humanTaskId}）；冻结绑定已由服务端建立。`);
      } else {
        // The answer is accepted, but the page never reports a binding the
        // server did not freeze: an unconsumed or refused confirmation stays
        // visible as such (recovering the run belongs to frontend:S05).
        setFailure(new Error('服务端已接受答复（任务已解决），但冻结绑定尚未建立：请重新读取运行投影后再判断；未确认期间不会有任何任务派发。'));
      }
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        // The request moved on (expired confirmation or a newer round): re-read
        // the current request, keep the selected entries and require an explicit
        // confirmation against the request version the server really holds.
        try {
          const latest = await client.getHumanTask(workspaceId, humanTaskId);
          setOverride({ base: task.data?.humanTask ?? latest.humanTask, value: latest.humanTask });
          setConflict({ submittedVersion: expectedVersion, serverVersion: latest.humanTask.request.version });
        } catch (refreshError) {
          setFailure(refreshError);
        }
        onChanged();
      } else if (error instanceof ApiError && error.category === 'POLICY_BLOCKED') {
        // The owner refused the selection (required entry dropped, entry
        // outside the frozen index, or the run no longer holds the authority).
        setFailure(error);
        onChanged();
      } else {
        setFailure(error);
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <>
      <h3 className="mt-4 mb-2 text-lg font-semibold">确认请求</h3>
      <DataList
        items={[
          { term: '请求', value: `${current.request.title}（${current.request.kind}）` },
          { term: '请求状态', value: <Badge tone={taskState.tone}>{taskState.label}</Badge> },
          { term: '请求版本', value: <code>v{current.request.version} · 摘要 {current.requestDigest}</code> },
          {
            term: '确认对象',
            value: <code>{current.request.object.kind} · {current.request.object.id}@{current.request.object.version}</code>,
          },
          { term: '完成方式', value: <code>{current.request.completionMode}</code> },
        ]}
      />
      {current.state === 'EXPIRED' && (
        <Notice tone="warning" title="确认请求已过期">
          <p>
            该请求针对的版本已不再适用（{current.expiredReason ?? '服务端未给出原因'}）。
            旧答复不会被应用到新的版本或轮次；请重新读取运行投影，等待新的确认请求后再确认。
          </p>
        </Notice>
      )}
      {failure !== null && <ErrorNotice title="确认失败" error={failure} />}
      {failure instanceof ApiError && failure.category === 'POLICY_BLOCKED' && (
        <Notice tone="info" title="如何恢复">
          <p>服务端未接受该选择（例如必需条目被删除或选择含索引外条目）。选择已保留；请重新读取运行与请求后再确认。</p>
        </Notice>
      )}
      {conflict !== null && (
        <Notice tone="warning" title={`请求版本冲突：你提交时基于 v${conflict.submittedVersion}，服务端当前为 v${conflict.serverVersion}`}>
          <p>已重新读取确认请求；选择已保留，请确认后按服务端最新请求版本重新提交（将使用新的请求键）。</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button type="button" onClick={() => void answer(conflict.serverVersion)} disabled={pending}>
              确认并按 v{conflict.serverVersion} 重新提交
            </Button>
            <Button type="button" variant="secondary" onClick={() => setConflict(null)}>取消</Button>
          </div>
        </Notice>
      )}
      {status !== null && <Notice tone="info" title="已提交"><p>{status}</p></Notice>}

      <h3 className="mt-4 mb-2 text-lg font-semibold">冻结索引条目</h3>
      <Field label="在冻结索引内筛选（只过滤本页已加载的条目，不发起检索）" className="mb-3 max-w-[32rem]">
        <Input
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      {recommendation.candidates.length === 0 ? (
        <EmptyState title="冻结索引内没有条目">
          <p>
            这是成功读取后的空结果，不是读取失败；确认后绑定空知识集，流程仍需这次显式确认。
          </p>
        </EmptyState>
      ) : visible.length === 0 ? (
        <EmptyState title="筛选没有匹配的索引条目">
          <p>已选条目不受筛选影响；清空筛选即可查看全部条目。</p>
        </EmptyState>
      ) : (
        <Table>
          <TableCaption className="caption-top mb-2 mt-0 text-left text-xs text-muted-foreground">
            冻结索引共 {recommendation.candidates.length} 条，其中 required {requiredCount} 条不可移除；已选 {paths.length} 条
          </TableCaption>
          <TableHeader className="bg-muted">
            <TableRow>
              <TableHead scope="col">选择</TableHead>
              <TableHead scope="col">路径</TableHead>
              <TableHead scope="col">范围</TableHead>
              <TableHead scope="col">内容摘要</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.map((candidate) => (
              <CandidateRow
                key={candidate.path}
                candidate={candidate}
                checked={selected.has(candidate.path) || candidate.required}
                disabled={!actionable || pending || candidate.required || conflict !== null}
                onToggle={() => setSelected((currentSelection) => toggledSelection(currentSelection, candidate))}
              />
            ))}
          </TableBody>
        </Table>
      )}
      {actionable ? (
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button
            type="button"
            disabled={pending || conflict !== null || current.state !== 'OPEN'}
            onClick={() => void answer(current.request.version)}
          >
            {pending ? '正在提交…' : `确认冻结知识选择（基于请求 v${current.request.version}）`}
          </Button>
        </div>
      ) : (
        <Notice tone="info" title="当前身份无法答复该请求">
          <p>
            服务端没有为当前身份返回 <code>respond</code> 动作（请求状态：{current.state}）。是否可答复由服务端判定，
            浏览器不会代它放行。
          </p>
        </Notice>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        未确认期间服务端不会派发任何任务；下方 DAG 只呈现服务端已记录的节点轮次，尚未物化的节点保持「未物化」，不做任何本地推演。
      </p>
    </>
  );
}

const candidateCheckboxId = (path: string) => `knowledge-select-${path.replace(/[^A-Za-z0-9_-]/g, '-')}`;

function CandidateRow({
  candidate,
  checked,
  disabled,
  onToggle,
}: {
  candidate: KnowledgeCandidate;
  checked: boolean;
  disabled: boolean;
  onToggle: () => void;
}) {
  return (
    <TableRow>
      <TableCell className="whitespace-normal">
        <CheckboxField
          id={candidateCheckboxId(candidate.path)}
          ariaLabel={`${candidate.required ? '必需条目' : '选择'} ${candidate.path}`}
          checked={checked}
          disabled={disabled}
          onCheckedChange={() => onToggle()}
        >
          {candidate.required ? '不可移除' : checked ? '已选' : '未选'}
        </CheckboxField>
      </TableCell>
      <TableCell className="whitespace-normal"><code>{candidate.path}</code></TableCell>
      <TableCell className="whitespace-normal font-mono text-xs">{candidate.scope}</TableCell>
      <TableCell className="whitespace-normal font-mono text-xs">{candidate.digest}</TableCell>
    </TableRow>
  );
}
