import type { ReactNode } from 'react';
import type {
  ArtifactListResponse,
  DeliveryViewResponse,
  ExternalOperation,
  GitHubObservation,
  RunDetail,
  VerificationRecord,
  WatermarkView,
} from '../api/contracts';
import type { ApiClient } from '../api/client';
import { useResource } from '../api/useResource';
import { formatTimestamp } from '../runtimes/presentation';
import { Button } from '../ui/button';
import { LoadingState } from '../ui/Feedback';
import { Badge, DataList, ErrorNotice, Notice, Section } from '../ui/States';
import { ClosurePanel } from './ClosurePanel';
import { GitHubLink } from './GitHubLink';
import { projectLinkNote, slugOf, type ProjectLink } from './links';
import {
  checksPresentation,
  commitUrl,
  identityConflicts,
  identityLayers,
  lastSuccessfulObservationAt,
  OBSERVATION_SUBJECT_LABELS,
  observationWatermarkPresentation,
  operationKindLabel,
  operationStatePresentation,
  observedPullRequestHead,
  pullRequestUrl,
  reviewApplicability,
  reviewApplicabilityPresentation,
  reviewStatePresentation,
  unknownObservations,
  verificationGroups,
  VERIFICATION_KIND_LABELS,
  verificationResultPresentation,
  type IdentityLayer,
} from './presentation';

const format = formatTimestamp;

/**
 * The delivery evidence of one WorkItem: who did what on GitHub, what the
 * control plane observed, what the quality evidence says, and whether the
 * delivery was closed.
 *
 * Four independent reads, and each one fails on its own: the delivery
 * projection (the carrier), the Run's identity, the published artifacts the
 * evidence points at, and the owner's observation watermarks. A failure in one
 * never blanks the others and none of them is replaced by a local guess.
 *
 * The page owns no command here. There is no merge button, no approval and no
 * closure action: the merge happens on GitHub, and Kowa only reports what it
 * reconciled.
 */
export function DeliverySection({
  client,
  workspaceId,
  workItemId,
  currentRunId,
  project,
  scope,
  generation,
}: {
  client: ApiClient;
  workspaceId: string;
  workItemId: string;
  currentRunId?: string;
  project: ProjectLink;
  scope: string;
  generation: number;
}) {
  // `currentRunId` is a dependency, not a detail: the delivery projection
  // changes the moment a run is admitted, and a stale pre-run read would show
  // "nothing has happened" for a work item that now has a run.
  const delivery = useResource(
    (signal) => client.getDeliveryView(workspaceId, workItemId, signal),
    [workspaceId, workItemId, currentRunId, generation],
    { scope: `${scope}:delivery`, generation },
  );
  const watermarks = useResource(
    (signal) => client.getWatermarks(workspaceId, workItemId, signal),
    [workspaceId, workItemId, currentRunId, generation],
    { scope: `${scope}:watermarks`, generation },
  );
  const artifacts = useResource(
    (signal) => client.listArtifacts(workspaceId, workItemId, signal),
    [workspaceId, workItemId, currentRunId, generation],
    { scope: `${scope}:delivery-artifacts`, generation },
  );
  // The run-level identities live in the run projection, not in the delivery
  // view: it is a separate read, so a run the panel cannot read degrades to
  // "运行身份未读到" instead of an empty claim about who wrote anything.
  const run = useResource(
    (signal) =>
      currentRunId === undefined
        ? Promise.resolve(undefined)
        : client.getRunDetail(workspaceId, workItemId, currentRunId, signal),
    [workspaceId, workItemId, currentRunId, generation],
    { scope: `${scope}:delivery-run`, generation },
  );

  if (delivery.status === 'loading') {
    return <Section title="交付证据与关闭" id="delivery"><LoadingState /></Section>;
  }
  if (delivery.status === 'error' || delivery.data === undefined) {
    return (
      <Section title="交付证据与关闭" id="delivery">
        <ErrorNotice title="无法读取交付证据投影" error={delivery.error} />
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={delivery.reload}>重试</Button>
        </div>
      </Section>
    );
  }

  const view = delivery.data;
  return (
    <Section title="交付证据与关闭" id="delivery">
      <p className="mb-3 max-w-[80ch] text-sm text-muted-foreground">
        本区只读展示服务端已核对的事实：Kowa 内发起运行的人、Runner 机器 gh 账号、GitHub 记录的 PR 作者与 Review、CI 检查、质量证据与关闭记录。
        合并由人<strong>在 GitHub 上</strong>完成；本页没有第二个合并批准按钮，也不会把口头反馈当合并。
      </p>
      <IdentityLayers view={view} run={run.status === 'ready' ? run.data : undefined} runStatus={run.status} project={project} />
      <OperationTimeline operations={view.operations} run={run.status === 'ready' ? run.data : undefined} project={project} />
      <FactSection facts={view.facts} project={project} />
      <WatermarkSection
        facts={view.facts}
        unknownCount={unknownObservations(view.facts).length}
        state={watermarks.status}
        data={watermarks.data}
        error={watermarks.error}
        onRetry={watermarks.reload}
      />
      <EvidenceSection verification={view.verification} artifacts={artifacts.data} artifactsStatus={artifacts.status} project={project} />
      <ClosurePanel view={view} run={run.status === 'ready' ? run.data : undefined} commitLink={commitLinkOf(project)} />
    </Section>
  );
}

function commitLinkOf(project: ProjectLink): { slug: string } | undefined {
  const slug = slugOf(project);
  if (slug === undefined) return undefined;
  return { slug };
}

// ---- identity --------------------------------------------------------------

function IdentityLayers({
  view,
  run,
  runStatus,
  project,
}: {
  view: DeliveryViewResponse;
  run?: RunDetail;
  runStatus: 'loading' | 'ready' | 'error';
  project: ProjectLink;
}) {
  const layers = identityLayers({ workItem: view.workItem, run, operations: view.operations });
  const conflicts = view.operations.flatMap((operation) => identityConflicts(operation, run));

  return (
    <>
      <h3 className="mb-2 text-sm font-semibold text-foreground">署名与身份分层</h3>
      {runStatus === 'error' && (
        <Notice tone="warning" title="运行身份未读到">
          <p>
            运行投影读取失败，因此「Web 发起人」与「Runner 机器 gh 账号」两行只反映工作项与外部操作里已记录的事实；
            本页不据此推断是谁执行了远端写入。
          </p>
        </Notice>
      )}
      {runStatus === 'loading' && <p className="mb-2 text-xs text-muted-foreground" role="status">正在读取运行身份…</p>}
      <DataList items={layers.map((layer) => ({ term: layer.term, value: <IdentityValue layer={layer} /> }))} />
      {conflicts.length > 0 && (
        <Notice tone="warning" title={`身份或版本冲突 ${conflicts.length} 项`}>
          <ul className="list-disc pl-5">
            {conflicts.map((conflict) => (
              <li key={`${conflict.operationId}:${conflict.code}`}>
                外部操作 <code>{conflict.operationId}</code>（{conflict.code}）：{conflict.detail}
              </li>
            ))}
          </ul>
          <p className="mt-1">服务端会以这些事实为准收束；本页不把不一致的写入显示为已按冻结身份完成。</p>
        </Notice>
      )}
      {projectLinkNote(project) !== undefined && (
        <p className="mt-2 text-xs text-muted-foreground">{projectLinkNote(project)}</p>
      )}
    </>
  );
}

function IdentityValue({ layer }: { layer: IdentityLayer }) {
  return (
    <div className="break-anywhere">
      <span>{layer.value}</span>
      <p className="mt-0.5 text-xs text-muted-foreground">来源 <code>{layer.source}</code></p>
      {layer.note !== undefined && <p className="mt-0.5 text-xs text-muted-foreground">{layer.note}</p>}
    </div>
  );
}

// ---- operations ------------------------------------------------------------

function OperationTimeline({
  operations,
  run,
  project,
}: {
  operations: ExternalOperation[];
  run?: RunDetail;
  project: ProjectLink;
}) {
  const slug = slugOf(project);
  return (
    <>
      <h3 className="mt-5 mb-2 text-sm font-semibold text-foreground">外部交付操作</h3>
      {operations.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          服务端还没有为这个工作项登记任何外部交付操作（<code>verify_change</code> / <code>publish_pr</code>）。
          这不是「已通过」，只是还没有发生。
        </p>
      ) : (
        <ul className="flex flex-col gap-3">
          {operations.map((operation) => {
            const state = operationStatePresentation(operation.state);
            return (
              <li key={operation.operationId} className="rounded-md border border-border-muted px-3 py-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge tone={state.tone}>{state.label}</Badge>
                  <span className="text-sm font-medium text-foreground">{operationKindLabel(operation.kind)}</span>
                  <code className="text-xs text-muted-foreground">{operation.operationId}</code>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">{state.note}</p>
                <DataList
                  items={[
                    { term: '运行 / 版本', value: <><code>{operation.workflowRunId}</code> · 版本 v{operation.version}</> },
                    {
                      term: '请求（Runner 受控通道）',
                      value: (
                        <div className="break-anywhere">
                          <p>仓库 <code>{operation.request.repositoryId}</code> · ref <code>{operation.request.ref}</code>{operation.request.baseRef ? <> · 目标 <code>{operation.request.baseRef}</code></> : null}</p>
                          <p>提交 <code>{operation.request.commitOid}</code>{slug !== undefined ? <> · <GitHubLink href={commitUrl(slug, operation.request.commitOid)} label="远端提交">在 GitHub 查看提交</GitHubLink></> : null}</p>
                          <p>期望写入账号 <code>{operation.request.expectedGitHubUserId}</code> · Web 发起（请求方）<code>{operation.request.requestedByGitHubUserId}</code></p>
                        </div>
                      ),
                    },
                    {
                      term: '结果（已核对事实）',
                      value: operation.result === undefined ? (
                        <span>服务端还没有登记结果；{operation.state === 'UNKNOWN' ? '结果未知时先查询对账，不重复创建。' : '操作也还没有结算。'}</span>
                      ) : (
                        <div className="break-anywhere">
                          <p>
                            PR{' '}
                            {operation.result.pullRequestNumber !== undefined ? (
                              slug !== undefined ? (
                                <GitHubLink href={pullRequestUrl(slug, operation.result.pullRequestNumber)} label={`Pull Request ${operation.result.pullRequestNumber}`}>
                                  #{operation.result.pullRequestNumber}
                                </GitHubLink>
                              ) : (
                                <>#{operation.result.pullRequestNumber}（未提供链接）</>
                              )
                            ) : (
                              '尚未创建'
                            )}{' '}
                            · PR 作者 <code>{operation.result.pullRequestAuthorId ?? '服务端未登记'}</code>
                          </p>
                          <p>提交 <code>{operation.result.commitOid}</code> · ref <code>{operation.result.ref}</code> · 对账 reconciled=<code>{operation.result.reconciled ? 'true' : 'false'}</code></p>
                          <p>观察 <code>{operation.result.observationId}</code> · {format(operation.result.observedAt)} · 读取账号 <code>{operation.result.observedByGitHubUserId ?? '服务端未登记'}</code></p>
                        </div>
                      ),
                    },
                    {
                      term: '时间线',
                      value: `登记 ${format(operation.createdAt)} · 更新 ${format(operation.updatedAt)} · 结算 ${operation.settledAt !== undefined ? format(operation.settledAt) : '尚未结算'}`,
                    },
                  ]}
                />
                {operation.unknownReason !== undefined && (
                  <Notice tone="warning" title="服务端记录的未知原因">
                    <p className="break-anywhere">{operation.unknownReason}</p>
                  </Notice>
                )}
                {run !== undefined && run.gitExecutionUserId !== undefined && operation.request.expectedGitHubUserId !== run.gitExecutionUserId && (
                  <p className="mt-1 text-xs text-muted-foreground">
                    本运行冻结的执行账号是 <code>{run.gitExecutionUserId}</code>，与该操作期望的账号不同。
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </>
  );
}

// ---- facts -----------------------------------------------------------------

function FactSection({ facts, project }: { facts: GitHubObservation[]; project: ProjectLink }) {
  const slug = slugOf(project);
  const head = observedPullRequestHead(facts);
  const known = facts.filter((fact) => !fact.unknown);
  const unknown = facts.filter((fact) => fact.unknown);
  // The PR number a review belongs to is read from the PR observation itself,
  // never parsed out of the review's subject key: the key is the owner's own
  // composite and the browser must not depend on its internal format.
  const pullRequestNumber = known.find((fact) => fact.pullRequest !== undefined)?.pullRequest?.number;

  return (
    <>
      <h3 className="mt-5 mb-2 text-sm font-semibold text-foreground">GitHub 事实（PR / Review / CI）</h3>
      <p className="mb-2 text-xs text-muted-foreground">
        这里只显示控制面已核对并记录的观察事实；每条都带观察 ID 与读取时间。PR 的有效 Review、实际合并与合并后验证分开呈现。
      </p>
      {facts.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          服务端还没有记录任何 GitHub 观察：没有已核对的 PR、Review 或 CI 事实。未观察到不等于不存在，也不等于通过。
        </p>
      ) : (
        <>
          <ul className="flex flex-col gap-3">
            {known.map((fact) => (
              <li key={fact.observationId} className="rounded-md border border-border-muted px-3 py-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium text-foreground">{OBSERVATION_SUBJECT_LABELS[fact.subjectKind]}</span>
                  <code className="text-xs text-muted-foreground">{fact.subjectKey}</code>
                  <span className="text-xs text-muted-foreground">观察 <code>{fact.observationId}</code> · {format(fact.observedAt)}</span>
                </div>
                <FactBody fact={fact} head={head} slug={slug} pullRequestNumber={pullRequestNumber} />
              </li>
            ))}
          </ul>
          {unknown.length > 0 && (
            <Notice tone="warning" title={`未成功读取的 GitHub 观察 ${unknown.length} 条`}>
              <ul className="list-disc pl-5">
                {unknown.map((fact) => (
                  <li key={fact.observationId}>
                    {OBSERVATION_SUBJECT_LABELS[fact.subjectKind]} <code>{fact.subjectKey}</code>：尝试于 {format(fact.observedAt)}，
                    {fact.unknownReason !== undefined ? `原因“${fact.unknownReason}”` : '服务端未给出原因'}。
                    <span className="block text-muted-foreground">这次读取没有给出事实，因此本页既不显示为通过，也不显示为不存在。</span>
                  </li>
                ))}
              </ul>
            </Notice>
          )}
        </>
      )}
    </>
  );
}

function FactBody({
  fact,
  head,
  slug,
  pullRequestNumber,
}: {
  fact: GitHubObservation;
  head?: string;
  slug?: string;
  pullRequestNumber?: string;
}) {
  if (fact.pullRequest !== undefined) {
    const pr = fact.pullRequest;
    return (
      <DataList
        items={[
          {
            term: 'PR 概况',
            value: (
              <div className="break-anywhere">
                <p>
                  #{pr.number}
                  {slug !== undefined ? <> · <GitHubLink href={pullRequestUrl(slug, pr.number)} label={`Pull Request ${pr.number}`}>在 GitHub 查看 PR</GitHubLink></> : null}
                  {' '}· 状态 <code>{pr.state}</code> · head <code>{pr.headOid}</code>
                </p>
                <p>源 <code>{pr.headRef ?? '服务端未登记'}</code> · 目标 <code>{pr.baseRef ?? '服务端未登记'}</code> · 作者 <code>{pr.authorId}</code></p>
              </div>
            ),
          },
          {
            term: '合并事实',
            value: pr.merged ? (
              <div className="break-anywhere">
                <Badge tone="ok">已合并</Badge>
                <p className="mt-1">
                  合并提交 <code>{pr.mergeCommitOid ?? '服务端未登记'}</code>
                  {slug !== undefined && pr.mergeCommitOid !== undefined ? <> · <GitHubLink href={commitUrl(slug, pr.mergeCommitOid)} label="合并提交">在 GitHub 查看合并提交</GitHubLink></> : null}
                </p>
                <p>合并账号 <code>{pr.mergedById ?? '服务端未登记'}</code> · 账号类型 <code>{pr.mergedByType ?? '服务端未登记'}</code> · {pr.mergedAt !== undefined ? format(pr.mergedAt) : '合并时间未登记'}</p>
                <p className="text-muted-foreground">合并由人在 GitHub 完成；账号类型是 GitHub 的事实，不证明人亲自操作，Kowa 也不推定。</p>
              </div>
            ) : (
              <span>GitHub 尚未报告该 PR 已合并。</span>
            ),
          },
        ]}
      />
    );
  }
  if (fact.review !== undefined) {
    const review = fact.review;
    const applicability = reviewApplicability(review, head);
    const applicabilityView = reviewApplicabilityPresentation(applicability, head);
    const verdict = reviewStatePresentation(review.state);
    return (
      <DataList
        items={[
          {
            term: '处理人与结论',
            value: (
              <div className="break-anywhere">
                <p>
                  <Badge tone={verdict.tone}>{verdict.label}</Badge>{' '}
                  处理人 <code>{review.reviewerLogin ?? review.reviewerId}</code>（GitHub 用户 <code>{review.reviewerId}</code>）
                </p>
                <p>
                  账号类型 <code>{review.reviewerType}</code> · 仓库权限 <code>{review.reviewerPermission ?? '服务端未登记'}</code> · 关联 <code>{review.authorAssociation ?? '服务端未登记'}</code>
                  {!verdict.recognised && <span className="block text-muted-foreground">本页不认识这个结论，原样显示，不按通过处理。</span>}
                </p>
                <p>提交时间 {review.submittedAt !== undefined ? format(review.submittedAt) : '服务端未登记'}</p>
                {slug !== undefined && pullRequestNumber !== undefined && (
                  <p>
                    <GitHubLink href={pullRequestUrl(slug, pullRequestNumber)} label={`Pull Request ${pullRequestNumber}`}>在 GitHub 查看该 PR 的 Review</GitHubLink>
                  </p>
                )}
              </div>
            ),
          },
          {
            term: '适用提交',
            value: (
              <div className="break-anywhere">
                <Badge tone={applicabilityView.tone}>{applicabilityView.label}</Badge>
                <p className="mt-1">Review 提交 <code>{review.commitOid ?? '服务端未登记'}</code> · PR 当前 head <code>{head ?? '服务端未登记'}</code></p>
                <p className="text-muted-foreground">{applicabilityView.note}</p>
              </div>
            ),
          },
        ]}
      />
    );
  }
  if (fact.checks !== undefined) {
    const checks = checksPresentation(fact.checks);
    return (
      <DataList
        items={[
          {
            term: 'CI 检查',
            value: (
              <div className="break-anywhere">
                <Badge tone={checks.tone}>{checks.label}</Badge>
                <p className="mt-1">{checks.detail}</p>
                <p>提交 <code>{fact.checks.commitOid}</code></p>
              </div>
            ),
          },
        ]}
      />
    );
  }
  if (fact.ref !== undefined) {
    return (
      <DataList
        items={[
          { term: '远端 ref HEAD', value: <code className="break-anywhere">{fact.ref.commitOid}</code> },
        ]}
      />
    );
  }
  return null;
}

// ---- watermarks ------------------------------------------------------------

function WatermarkSection({
  facts,
  unknownCount,
  state,
  data,
  error,
  onRetry,
}: {
  facts: GitHubObservation[];
  unknownCount: number;
  state: 'loading' | 'ready' | 'error';
  data?: WatermarkView;
  error?: unknown;
  onRetry: () => void;
}) {
  const lastSuccess = lastSuccessfulObservationAt(facts);
  return (
    <>
      <h3 className="mt-5 mb-2 text-sm font-semibold text-foreground">最后成功观察与水位</h3>
      <DataList
        items={[
          {
            term: '最后一次成功观察（本投影）',
            value: lastSuccess !== undefined ? (
              <span className="break-anywhere">
                <time dateTime={lastSuccess}>{format(lastSuccess)}</time>
                <span className="block text-muted-foreground">这是本投影里最新的一条成功读取的观察时间；界面重连或 SSE 保持连接都不代表 GitHub 状态更新，只有这条时间会前进。</span>
              </span>
            ) : (
              <span>本投影里没有任何成功的 GitHub 读取；最后一次尝试是否成功见下面的水位。</span>
            ),
          },
          {
            term: '未成功读取',
            value: unknownCount === 0
              ? '本投影里没有未成功读取的观察。'
              : `${unknownCount} 条观察是失败的读取，它们没有给出事实，单独列在上面。`,
          },
        ]}
      />
      {state === 'loading' && <LoadingState />}
      {state === 'error' && (
        <>
          <ErrorNotice title="无法读取服务端观察水位" error={error} />
          <div className="mt-3">
            <Button type="button" variant="secondary" onClick={onRetry}>重试</Button>
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            水位不可用时，上面的事实仍然按它们各自的观察时间显示；本页不会因此声称 GitHub 状态是新鲜的。
          </p>
        </>
      )}
      {state === 'ready' && data !== undefined && (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            水位由服务端在 {format(data.observedAt)} 生成，新鲜窗口 {data.freshnessWindowSeconds} 秒（由服务端冻结，本页不另设阈值）。
            最后成功读取时间只随成功读取前进；失败的读取会立即降级新鲜度，不会让旧事实看起来是新的。
          </p>
          {data.observations.length === 0 ? (
            <p className="text-sm text-muted-foreground">服务端还没有为这个工作项登记任何外部观察水位。</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {data.observations.map((watermark) => {
                const view = observationWatermarkPresentation(watermark);
                return (
                  <li key={`${watermark.subjectKind}/${watermark.subjectKey}`} className="rounded-md border border-border-muted px-3 py-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge tone={view.tone}>{view.label}</Badge>
                      <code className="text-xs">{watermark.subjectKind}/{watermark.subjectKey}</code>
                      <span className="text-xs text-muted-foreground">新鲜度 <code>{watermark.freshness}</code>{watermark.freshnessReason !== undefined ? <>（<code>{watermark.freshnessReason}</code>）</> : null}</span>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground break-anywhere">{view.note}</p>
                  </li>
                );
              })}
            </ul>
          )}
        </>
      )}
    </>
  );
}

// ---- verification evidence -------------------------------------------------

function EvidenceSection({
  verification,
  artifacts,
  artifactsStatus,
  project,
}: {
  verification: VerificationRecord[];
  artifacts?: ArtifactListResponse;
  artifactsStatus: 'loading' | 'ready' | 'error';
  project: ProjectLink;
}) {
  const { gates, auxiliary } = verificationGroups(verification);
  const artifactIndex = new Map((artifacts?.artifacts ?? []).map((artifact) => [artifact.artifactId, artifact]));

  function reportOf(record: VerificationRecord): ReactNode {
    if (record.reportArtifactId === undefined) return <span>该证据没有登记报告产物。</span>;
    const artifact = artifactIndex.get(record.reportArtifactId);
    return (
      <span className="break-anywhere">
        报告产物 <code>{record.reportArtifactId}</code> · 摘要 <code>{record.reportDigest ?? '服务端未登记'}</code>
        {artifactsStatus === 'error' ? (
          <span className="block text-muted-foreground">产物清单读取失败，无法核对这份报告在已发布产物中的状态。</span>
        ) : artifact === undefined ? (
          <span className="block text-muted-foreground">已读到的产物清单里没有这条产物；本页不推断内容，逐字引用。</span>
        ) : (
          <span className="block text-muted-foreground">
            产物清单中：种类 <code>{artifact.kind}</code> · 媒体类型 <code>{artifact.mediaType}</code> · 状态 <code>{artifact.state}</code>（{artifact.state === 'PUBLISHED' ? '已发布' : '尚未处于已发布状态'}）
          </span>
        )}
        <span className="block text-muted-foreground">契约不提供浏览器端产物内容读取，因此本页只给引用，不展示内容。</span>
      </span>
    );
  }

  return (
    <>
      <h3 className="mt-5 mb-2 text-sm font-semibold text-foreground">质量证据（只读）</h3>
      {verification.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          服务端还没有为这个工作项登记任何验证证据；缺失不是通过。
        </p>
      ) : (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            必需测试、独立评审、安全评审与合并后集成验证属同步门禁；知识建议与离线评测是辅助事实，失败不阻塞交付关闭。
            每条都显示来源与生产者，模拟来源不满足门禁。
          </p>
          <ul className="flex flex-col gap-3">
            {[...gates, ...auxiliary].map((record) => {
              const result = verificationResultPresentation(record);
              return (
                <li key={record.verificationId} className="rounded-md border border-border-muted px-3 py-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge tone={result.tone}>{result.label}</Badge>
                    <span className="text-sm font-medium text-foreground">{VERIFICATION_KIND_LABELS[record.kind]}</span>
                    <code className="text-xs text-muted-foreground">{record.verificationId}</code>
                    <span className="text-xs text-muted-foreground">{format(record.observedAt)}</span>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">{result.note}</p>
                  <DataList
                    items={[
                      {
                        term: '被验证对象',
                        value: (
                          <span className="break-anywhere">
                            {record.subjectKind === 'commit' ? (
                              <>提交 <code>{record.commitOid ?? '服务端未登记'}</code> · 仓库 <code>{record.repositoryId ?? '服务端未登记'}</code></>
                            ) : (
                              <>产物 <code>{record.artifactId ?? '服务端未登记'}</code> · 摘要 <code>{record.artifactDigest ?? '服务端未登记'}</code></>
                            )}
                          </span>
                        ),
                      },
                      {
                        term: '来源与生产者',
                        value: (
                          <span className="break-anywhere">
                            来源 <code>{record.source}</code> · 生产者 <code>{record.producerKind}</code>/<code>{record.producerId}</code> · 配置版本 <code>{record.configurationVersion}</code>
                          </span>
                        ),
                      },
                      { term: '报告产物', value: reportOf(record) },
                    ]}
                  />
                </li>
              );
            })}
          </ul>
          {auxiliary.some((record) => record.result === 'failed') && (
            <Notice tone="info" title="辅助事实失败不阻塞关闭">
              <p>
                知识建议或离线评测失败只影响它们自己：业务交付的关闭不因它们失败而失败。
                重新尝试这类辅助分支的入口在运行视图的「失败与恢复」区（<code>retry-knowledge-suggestion</code>），由服务端裁决是否接受。
              </p>
            </Notice>
          )}
        </>
      )}
      {projectLinkNote(project) !== undefined && <p className="mt-2 text-xs text-muted-foreground">{projectLinkNote(project)}</p>}
    </>
  );
}
