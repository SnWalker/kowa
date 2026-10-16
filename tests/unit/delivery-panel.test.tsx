/**
 * The delivery panel at the level the operator reads it.
 *
 * The unit tests above this one own the semantics; these own the page: that a
 * failed read does not blank its neighbours, that an unknown read never reads
 * as a fact, that a merged-but-unclosed delivery says so, and that the page
 * offers no second approval, merge or close.
 */
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import {
  FIXTURE_DELIVERY_COMMIT,
  FIXTURE_MACHINE_GITHUB_ID,
  FIXTURE_MERGE_COMMIT,
  FIXTURE_PR_NUMBER,
  FIXTURE_REVIEWER_GITHUB_ID,
  FIXTURE_RUN_ID,
  FIXTURE_WORK_ITEM_ID,
  artifactListFixture,
  checksObservationFixture,
  closureRecordFixture,
  deliveryViewFixture,
  fixedWorkflowFixture,
  pullRequestObservationFixture,
  reviewObservationFixture,
  runDetailFixture,
  verificationFixture,
  watermarkViewFixture,
  workItemFixture,
  workItemResponseFixture,
  workspaceConfigFixture,
} from '../fixtures/contracts';
import { createFakeFetch, renderApp, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
const CONFIG_PATH = `/api/v1/workspaces/${WORKSPACE}`;
const ITEM_PATH = `${CONFIG_PATH}/work-items/${FIXTURE_WORK_ITEM_ID}`;
const RUN_PATH = `${ITEM_PATH}/runs/${FIXTURE_RUN_ID}`;
const ARTIFACTS_PATH = `${ITEM_PATH}/artifacts`;
const DELIVERY_PATH = `${ITEM_PATH}/delivery`;
const WATERMARKS_PATH = `${ITEM_PATH}/observability/watermarks`;
const WORKFLOW_PATH = `${CONFIG_PATH}/fixed-workflow`;

const CONFIG = `GET ${CONFIG_PATH}`;
const ITEM = `GET ${ITEM_PATH}`;
const RUN = `GET ${RUN_PATH}`;
const ARTIFACTS = `GET ${ARTIFACTS_PATH}`;
const DELIVERY = `GET ${DELIVERY_PATH}`;
const WATERMARKS = `GET ${WATERMARKS_PATH}`;
const WORKFLOW = `GET ${WORKFLOW_PATH}`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function pageRoutes(overrides: Routes = {}): Routes {
  return {
    'POST /api/v2/auth/session': sessionRoute(),
    [CONFIG]: { status: 200, body: workspaceConfigFixture() },
    [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ state: 'CLOSED', version: 4, currentRunId: FIXTURE_RUN_ID })) },
    [RUN]: { status: 200, body: runDetailFixture({ state: 'SUCCEEDED' }) },
    [WORKFLOW]: { status: 200, body: fixedWorkflowFixture() },
    [ARTIFACTS]: { status: 200, body: artifactListFixture() },
    [DELIVERY]: { status: 200, body: deliveryViewFixture() },
    [WATERMARKS]: { status: 200, body: watermarkViewFixture() },
    ...overrides,
  };
}

function renderPage(routes: Routes) {
  const { fetchImpl, calls } = createFakeFetch(routes);
  const user = userEvent.setup({ delay: null });
  const view = renderApp({ fetch: fetchImpl, initialEntries: [`/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`] });
  return { calls, user, ...view };
}

/**
 * Waits for the panel to reach the state under test and only then binds the
 * region. Binding while the panel is still loading would hold a detached
 * subtree: the loading branch and the loaded branch are different elements.
 */
async function delivery(marker: RegExp = /本区只读展示服务端已核对的事实/) {
  await screen.findByText(marker, undefined, { timeout: 5000 });
  return within(screen.getByRole('region', { name: '交付证据与关闭' }));
}

describe('the delivery panel keeps the five identities apart on the page', () => {
  it('shows the Web initiator, the machine account and the PR author as three separate rows', async () => {
    renderPage(pageRoutes());
    const panel = await delivery();
    // Each label is its own term, and the values are the owner's own fields.
    expect(await panel.findByText('Web 发起人（当前 WorkflowRun）')).toBeInTheDocument();
    expect(panel.getByText('Runner 机器 gh 账号（运行准入时冻结）')).toBeInTheDocument();
    expect(panel.getByText('GitHub PR 实际创建者')).toBeInTheDocument();
    expect(panel.getByText('远端 commit author / committer')).toBeInTheDocument();
    expect(panel.getByText(/未在任何已冻结的浏览器消费合同里携带/)).toBeInTheDocument();
    // The machine account appears as the PR author fact, not as the initiator.
    expect(panel.getAllByText(new RegExp(FIXTURE_MACHINE_GITHUB_ID)).length).toBeGreaterThan(0);
  });

  it('never renders a merge, approve or close control of its own', async () => {
    renderPage(pageRoutes());
    const panel = await delivery();
    for (const name of [/合并/, /批准/, /关闭工作项/, /我已/]) {
      expect(panel.queryByRole('button', { name })).not.toBeInTheDocument();
    }
    expect(panel.getByText(/没有第二个合并批准按钮/)).toBeInTheDocument();
  });
});

describe('observed facts, unknown reads and closures stay distinct', () => {
  it('states a merged PR with no closure record as merged and not closed', async () => {
    renderPage(
      pageRoutes({
        [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ state: 'OPEN', version: 3, currentRunId: FIXTURE_RUN_ID })) },
        [DELIVERY]: {
          status: 200,
          body: deliveryViewFixture({
            workItem: workItemFixture({ state: 'OPEN', version: 3, currentRunId: FIXTURE_RUN_ID }),
            closure: undefined,
          }),
        },
      }),
    );
    const panel = await delivery();
    expect(await panel.findByText('GitHub 已报告合并，但工作项未关闭')).toBeInTheDocument();
    // The merge fact stays visible next to the missing closure.
    expect(panel.getAllByText(FIXTURE_MERGE_COMMIT).length).toBeGreaterThan(0);
    expect(panel.queryByText('关闭记录')).not.toBeInTheDocument();
  });

  it('shows an unknown read as its own attempt, never as a fact and never as a pass', async () => {
    renderPage(
      pageRoutes({
        [DELIVERY]: {
          status: 200,
          body: deliveryViewFixture({
            facts: [
              pullRequestObservationFixture(),
              reviewObservationFixture(),
              checksObservationFixture({
                observationId: 'obs_checks_unknown',
                unknown: true,
                checks: undefined,
                unknownReason: 'gh exited non-zero',
                observedAt: '2026-10-16T10:20:00Z',
              }),
            ],
          }),
        },
      }),
    );
    const panel = await delivery();
    const unknownNotice = await panel.findByText(/未成功读取的 GitHub 观察 1 条/);
    expect(unknownNotice).toBeInTheDocument();
    const notice = unknownNotice.closest('[role="status"]') as HTMLElement;
    expect(within(notice).getByText(/原因“gh exited non-zero”/)).toBeInTheDocument();
    expect(within(notice).getByText(/既不显示为通过，也不显示为不存在/)).toBeInTheDocument();
    // The last successful read is the PR observation, not the failed attempt.
    expect(panel.getByText('2026-10-16T10:00:00.000Z')).toBeInTheDocument();
  });

  it('separates the synchronous gates from the auxiliary facts and says the auxiliary failure does not block', async () => {
    renderPage(pageRoutes());
    const panel = await delivery();
    expect(panel.getAllByText('必需测试').length).toBeGreaterThan(0);
    expect(panel.getAllByText('独立评审').length).toBeGreaterThan(0);
    expect(panel.getAllByText('合并后集成验证').length).toBeGreaterThan(0);
    expect(panel.getByText('辅助事实失败不阻塞关闭')).toBeInTheDocument();
    expect(panel.getByText('知识更新建议（辅助）')).toBeInTheDocument();
  });

  it('reports a review on another head as not applying to the current head', async () => {
    renderPage(
      pageRoutes({
        [DELIVERY]: {
          status: 200,
          body: deliveryViewFixture({
            facts: [
              pullRequestObservationFixture({
                observedAt: '2026-10-16T10:10:00Z',
                pullRequest: { ...pullRequestObservationFixture().pullRequest!, headOid: FIXTURE_MERGE_COMMIT },
              }),
              reviewObservationFixture(),
              checksObservationFixture(),
            ],
          }),
        },
        [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ state: 'OPEN', currentRunId: FIXTURE_RUN_ID })) },
      }),
    );
    const panel = await delivery();
    // The head moved after the review: the same review now reads as superseded.
    expect(await panel.findByText('不适用于当前 head')).toBeInTheDocument();
  });

  it('re-reads the closure checklist and names the item that disagrees', async () => {
    renderPage(
      pageRoutes({
        [DELIVERY]: {
          status: 200,
          body: deliveryViewFixture({ closure: closureRecordFixture({ mergeCommitOid: FIXTURE_DELIVERY_COMMIT }) }),
        },
      }),
    );
    const panel = await delivery();
    expect(await panel.findByText('实际合并')).toBeInTheDocument();
    expect(panel.getAllByText('重读不一致').length).toBeGreaterThan(0);
    // Both the merge row and the post-merge row bind the merge commit, so both
    // disagree with the same wrong closure value — and the page names them.
    expect(panel.getByText('2 项没有重读一致：实际合并、合并后验证。')).toBeInTheDocument();
  });
});

describe('independent reads fail on their own', () => {
  it('keeps the facts visible when the watermark read fails', async () => {
    renderPage(pageRoutes({ [WATERMARKS]: { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } } }));
    const panel = await delivery();
    expect(await panel.findByText('无法读取服务端观察水位')).toBeInTheDocument();
    // The facts are still there, and the page states the watermarks are not.
    expect(panel.getAllByText(new RegExp(`#${FIXTURE_PR_NUMBER}`)).length).toBeGreaterThan(0);
    expect(panel.getByText(/本页不会因此声称 GitHub 状态是新鲜的/)).toBeInTheDocument();
  });

  it('reports an unreadable delivery projection with a retry and does not claim it is empty', async () => {
    renderPage(pageRoutes({ [DELIVERY]: { status: 500, body: { error: 'INTERNAL_ERROR' } } }));
    const panel = await delivery(/无法读取交付证据投影/);
    expect(await panel.findByText('无法读取交付证据投影')).toBeInTheDocument();
    expect(panel.getByRole('button', { name: '重试' })).toBeInTheDocument();
    expect(panel.queryByText(/服务端还没有为这个工作项登记/)).not.toBeInTheDocument();
  });

  it('says the report artifact status cannot be re-read when the artifact list fails', async () => {
    renderPage(pageRoutes({ [ARTIFACTS]: { status: 500, body: { error: 'INTERNAL_ERROR' } } }));
    const panel = await delivery();
    expect(panel.getAllByText(/产物清单读取失败，无法核对这份报告在已发布产物中的状态/).length).toBeGreaterThan(0);
  });
});

describe('external links are built only from a validated repository slug', () => {
  it('links a PR and a commit to github.com, opening in a new tab', async () => {
    renderPage(pageRoutes());
    const panel = await delivery();
    const links = await panel.findAllByRole('link', { name: new RegExp(`Pull Request ${FIXTURE_PR_NUMBER}`) });
    const link = links[0]!;
    expect(link).toHaveAttribute('href', `https://github.com/team/project/pull/${FIXTURE_PR_NUMBER}`);
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noreferrer noopener');
    expect(link.getAttribute('aria-label')).toContain('在 GitHub 新标签页打开');
  });

  it('offers no GitHub link when the registered display name is not an owner/repo slug', async () => {
    renderPage(
      pageRoutes({
        [CONFIG]: {
          status: 200,
          body: workspaceConfigFixture({
            project: { githubRepositoryId: '3001', installationId: '4001', displayName: 'team project（未按 owner/repo 填写）', role: 'project', access: 'read_write' },
          }),
        },
      }),
    );
    const panel = await delivery();
    expect(panel.getAllByText(/不是 GitHub 的 owner\/repo 形式/).length).toBeGreaterThan(0);
    expect(panel.queryAllByRole('link').filter((anchor) => anchor.getAttribute('href')?.startsWith('https://github.com/'))).toHaveLength(0);
  });

  it('offers no GitHub link when the workspace project binding is another repository', async () => {
    renderPage(
      pageRoutes({
        [CONFIG]: {
          status: 200,
          body: workspaceConfigFixture({
            project: { githubRepositoryId: '9999', installationId: '4001', displayName: 'team/project', role: 'project', access: 'read_write' },
          }),
        },
      }),
    );
    const panel = await delivery();
    expect(panel.getAllByText(/与本工作项的目标仓库/).length).toBeGreaterThan(0);
  });
});

describe('the identity layers carry the machine account through the whole journey', () => {
  it('shows the reconciled PR author and the reading account', async () => {
    renderPage(pageRoutes());
    const panel = await delivery();
    expect(await panel.findByText('GitHub 对账读取身份')).toBeInTheDocument();
    expect(panel.getByText('外部操作期望的机器账号')).toBeInTheDocument();
    expect(panel.getAllByText(new RegExp(FIXTURE_REVIEWER_GITHUB_ID)).length).toBeGreaterThan(0);
  });

  it('states that no external delivery operation has happened yet instead of showing a pass', async () => {
    renderPage(
      pageRoutes({
        [DELIVERY]: { status: 200, body: deliveryViewFixture({ operations: [], facts: [], verification: [], closure: undefined }) },
        [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ state: 'OPEN', currentRunId: FIXTURE_RUN_ID })) },
      }),
    );
    const panel = await delivery();
    expect(await panel.findByText(/服务端还没有为这个工作项登记任何外部交付操作/)).toBeInTheDocument();
    expect(panel.getByText(/没有已核对的 PR、Review 或 CI 事实/)).toBeInTheDocument();
    expect(panel.getByText(/服务端还没有为这个工作项登记任何验证证据/)).toBeInTheDocument();
    expect(panel.getByText(/这不是「已通过」/)).toBeInTheDocument();
  });

  it('reports a simulated evidence row as not satisfying the gate', async () => {
    renderPage(
      pageRoutes({
        [DELIVERY]: { status: 200, body: deliveryViewFixture({ verification: [verificationFixture({ source: 'simulated' })] }) },
      }),
    );
    const panel = await delivery();
    expect(await panel.findByText('simulated 模拟来源')).toBeInTheDocument();
    expect(panel.getByText('模拟来源不满足门禁，无论结果字段是什么。')).toBeInTheDocument();
  });
});
