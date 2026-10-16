import type { ClosureRecord, DeliveryViewResponse, RunDetail } from '../api/contracts';
import { GitHubLink } from './GitHubLink';
import { Badge, DataList, Notice } from '../ui/States';
import {
  closureChecklist,
  commitUrl,
  fixRunBindings,
  observedMergeCommit,
  pullRequestUrl,
  type ChecklistItem,
  type ChecklistResolution,
} from './presentation';

const RESOLUTION: Record<ChecklistResolution, { label: string; tone: 'ok' | 'warn' | 'bad' | 'unknown' }> = {
  yes: { label: '重读一致', tone: 'ok' },
  no: { label: '重读不一致', tone: 'bad' },
  unknown: { label: '本投影无法重读', tone: 'unknown' },
};

/**
 * The closure record, the run lineage it continues, and the two things the
 * operator must never see folded together: an observed merge and a closed
 * delivery. A merged PR with no closure record stays visibly open; a closure
 * record with a mismatching binding stays visibly unverified item by item.
 */
export function ClosurePanel({
  view,
  run,
  commitLink,
}: {
  view: DeliveryViewResponse;
  run?: RunDetail;
  commitLink?: { slug: string };
}) {
  const { closure, workItem } = view;
  const merged = observedMergeCommit(view.facts);
  const repairs = fixRunBindings(view.runBindings, workItem.currentRunId);
  const currentIsRepair = view.runBindings.find((binding) => binding.workflowRunId === workItem.currentRunId)?.kind === 'repair';

  return (
    <>
      <h3 className="mt-5 mb-2 text-sm font-semibold text-foreground">关闭事实</h3>
      {merged !== undefined && workItem.state !== 'CLOSED' && (
        <Notice tone="warning" title="GitHub 已报告合并，但工作项未关闭">
          <p>
            PR 已被合并（合并提交 <code className="break-anywhere">{merged}</code>），但服务端的工作项状态是{' '}
            <code>{workItem.state}</code>，并且没有关闭记录。合并事实与未关闭同时可见：交付尚未完成，
            本页不把它显示成已关闭。
          </p>
        </Notice>
      )}
      {closure === undefined && workItem.state === 'CLOSED' && (
        <Notice tone="warning" title="工作项已关闭，但没有关闭记录">
          <p>服务端把工作项标为 CLOSED，本投影里却没有对应的关闭记录；以服务端状态为准，并请核对关闭用例的对账结果。</p>
        </Notice>
      )}
      {closure !== undefined && workItem.state !== 'CLOSED' && (
        <Notice tone="warning" title="有关闭记录，但工作项状态不是 CLOSED">
          <p>关闭记录与工作项状态不一致（服务端状态 <code>{workItem.state}</code>）；本页以服务端的工作项状态为准，不据关闭记录断言已关闭。</p>
        </Notice>
      )}
      {closure === undefined ? (
        workItem.state === 'CLOSED' ? null : (
          <p className="text-sm text-muted-foreground">
            服务端还没有为该工作项提交关闭记录。关闭清单逐项绑定的证据（需求摘要、冻结定义、运行、必需测试、独立评审、有效 Review、实际合并、合并后验证）
            都由关闭用例在服务端重读；本页不代替它做决定，也不提供任何关闭或合并按钮。
          </p>
        )
      ) : (
        <ClosureFacts closure={closure} items={closureChecklist(closure, {
          workItem,
          run,
          operations: view.operations,
          facts: view.facts,
          verification: view.verification,
        })} commitLink={commitLink} prNumber={closure.pullRequestNumber} />
      )}
      <div className="mt-4">
        {/* Every heading in this panel stays at h3: the shared `Notice` renders
            its title as an h2, and an h4 after one of those notices would be a
            heading-order jump. */}
        <h3 className="mb-1 text-sm font-semibold text-foreground">运行与修复关联</h3>
        <p className="mb-2 text-xs text-muted-foreground">
          一个工作项同时只有一条拥有推进权的交付运行；修复运行由服务端在人工确认后关联到同一工作项，并绑定它修复的实际合并提交。
          本页不创建新运行，也不回滚任何已发生的远端事实。
        </p>
        <DataList
          items={view.runBindings.map((binding) => ({
            term: binding.kind === 'repair' ? '修复运行（repair）' : '交付运行（initial）',
            value: (
              <span className="break-anywhere">
                <code>{binding.workflowRunId}</code>
                {binding.workflowRunId === workItem.currentRunId ? ' · 当前推进运行' : ''}
                {binding.sourceRunId !== undefined ? <> · 来源运行 <code>{binding.sourceRunId}</code></> : null}
                <span className="block text-muted-foreground">
                  关联人 <code>{binding.boundByGitHubUserId}</code> · {binding.boundAt}
                  {binding.mergedBaselineOid !== undefined ? <> · 修复基线（实际合并提交）<code>{binding.mergedBaselineOid}</code></> : null}
                </span>
                {binding.reason !== undefined && <span className="block text-muted-foreground">原因：{binding.reason}</span>}
              </span>
            ),
          }))}
        />
        {repairs.length > 0 && (
          <p className="mt-2 text-xs text-muted-foreground">
            该工作项已经由 {repairs.length} 个修复运行继续过；{currentIsRepair ? '当前推进运行就是其中一个修复运行。' : '当前推进运行不是修复运行。'}
          </p>
        )}
      </div>
    </>
  );
}

function ClosureFacts({
  closure,
  items,
  commitLink,
  prNumber,
}: {
  closure: ClosureRecord;
  items: ChecklistItem[];
  commitLink?: { slug: string };
  prNumber: string;
}) {
  const unresolved = items.filter((item) => item.resolved !== 'yes');
  return (
    <>
      <DataList
        items={[
          { term: '关闭记录', value: <><code>{closure.closureId}</code> · {closure.createdAt} · 记录人 <code>{closure.createdByGitHubUserId}</code></> },
          {
            term: '冻结定义与策略',
            value: (
              <span className="break-anywhere">
                <code>{closure.definitionName}@{closure.definitionVersion}</code> · 摘要 <code>{closure.definitionDigest}</code>
                <span className="block">策略版本 <code>{closure.policyVersion}</code> · 清单摘要 <code>{closure.checklistDigest}</code></span>
                <span className="block text-muted-foreground">策略版本在冻结投影里只记录一次：它是关闭用例据以判定的版本，不是本页的比较对象。</span>
              </span>
            ),
          },
          {
            term: '合并与 Review 观察',
            value: (
              <span className="break-anywhere">
                合并观察 <code>{closure.mergeObservationId}</code> · 合并提交 <code>{closure.mergeCommitOid}</code>
                {commitLink !== undefined ? <> · <GitHubLink href={commitUrl(commitLink.slug, closure.mergeCommitOid)} label="合并提交">在 GitHub 查看合并提交</GitHubLink></> : null}
                <span className="block">
                  Review 观察 <code>{closure.reviewObservationId}</code> · 处理人 <code>{closure.reviewerGitHubUserId}</code> · 适用提交 <code>{closure.reviewHeadOid}</code>
                  {commitLink !== undefined ? <> · <GitHubLink href={pullRequestUrl(commitLink.slug, prNumber)} label={`Pull Request ${prNumber}`}>在 GitHub 查看 PR #{prNumber}</GitHubLink></> : null}
                </span>
                <span className="block">合并账号 <code>{closure.mergedByGitHubUserId}</code> · 账号类型 <code>{closure.mergedByType}</code>（人不等于账号类型，本页不据类型推定任何人）</span>
              </span>
            ),
          },
          {
            term: '知识绑定',
            value: (
              <span className="break-anywhere">
                {closure.knowledgeSnapshotId !== undefined || closure.knowledgeBindingEventId !== undefined ? (
                  <>快照 <code>{closure.knowledgeSnapshotId ?? '服务端未登记'}</code> · 绑定事件 <code>{closure.knowledgeBindingEventId ?? '服务端未登记'}</code></>
                ) : (
                  '关闭记录没有登记知识快照与绑定事件；本投影也未提供读取知识绑定的操作，因此无法在此重读该绑定，本页不对它作任何断言。'
                )}
              </span>
            ),
          },
        ]}
      />
      <h3 className="mt-4 mb-1 text-sm font-semibold text-foreground">关闭清单逐项重读</h3>
      <p className="mb-2 text-xs text-muted-foreground">
        下面的每一行都是本页用同一份投影里的另一个事实，去对照关闭记录里绑定的值（即 owner 冻结的绑定项）。
        「重读不一致」表示两个服务端事实互相矛盾，请核对服务端；它不代表本页已经替关闭用例作了判定。
      </p>
      <ul className="flex flex-col gap-2">
        {items.map((item) => {
          const view = RESOLUTION[item.resolved];
          return (
            <li key={item.key} className="rounded-md border border-border-muted px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <Badge tone={view.tone}>{view.label}</Badge>
                <span className="text-sm font-medium text-foreground">{item.term}</span>
              </div>
              <p className="mt-1 text-xs text-muted-foreground break-anywhere">{item.value}</p>
              <p className="mt-0.5 text-xs text-muted-foreground break-anywhere">{item.note}</p>
            </li>
          );
        })}
      </ul>
      {unresolved.length > 0 && (
        <p className="mt-2 text-xs text-muted-foreground" role="status">
          {unresolved.length} 项没有重读一致：{unresolved.map((item) => item.term).join('、')}。
        </p>
      )}
    </>
  );
}
