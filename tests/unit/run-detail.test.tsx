/**
 * The four red lines of the run view, at the level the operator actually reads
 * them:
 *
 * - progress does not advance the DAG, and a completed Task is never drawn as a
 *   passed business verdict;
 * - an earlier iteration is always told apart from the current one;
 * - a skipped node stays visible instead of being folded into "done";
 * - the page does not decide anything the server did not say.
 */
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { createApiClient } from '../../src/api/client';
import { RunView } from '../../src/runs/RunView';
import {
  DIGEST_A,
  FIXTURE_RUN_ID,
  FIXTURE_WORK_ITEM_ID,
  artifactFixture,
  artifactListFixture,
  deliveryViewFixture,
  fixedWorkflowFixture,
  humanTaskResponseFixture,
  knowledgeRecommendationFixture,
  nodeHistoryFixture,
  runDetailFixture,
  sessionBootstrapFixture,
  watermarkViewFixture,
  workItemFixture,
  workItemResponseFixture,
  workspaceConfigFixture,
} from '../fixtures/contracts';
import { createFakeFetch, renderApp, renderWithProviders, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
const CONFIG_PATH = `/api/v1/workspaces/${WORKSPACE}`;
const ITEM_PATH = `${CONFIG_PATH}/work-items/${FIXTURE_WORK_ITEM_ID}`;
const RUN_PATH = `${ITEM_PATH}/runs/${FIXTURE_RUN_ID}`;
const WORKFLOW_PATH = `${CONFIG_PATH}/fixed-workflow`;
const ARTIFACTS_PATH = `${ITEM_PATH}/artifacts`;
// The work item page carries the delivery evidence panel next to the run; its
// two reads are answered here so the run assertions stay about the run.
const DELIVERY_PATH = `${ITEM_PATH}/delivery`;
const WATERMARKS_PATH = `${ITEM_PATH}/observability/watermarks`;
const TASK_PATH = `${CONFIG_PATH}/human-tasks/ht_5d2a`;

const CONFIG = `GET ${CONFIG_PATH}`;
const ITEM = `GET ${ITEM_PATH}`;
const RUN = `GET ${RUN_PATH}`;
const WORKFLOW = `GET ${WORKFLOW_PATH}`;
const ARTIFACTS = `GET ${ARTIFACTS_PATH}`;
const DELIVERY = `GET ${DELIVERY_PATH}`;
const WATERMARKS = `GET ${WATERMARKS_PATH}`;
const TASK = `GET ${TASK_PATH}`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function pageRoutes(overrides: Routes = {}): Routes {
  return {
    'POST /api/v2/auth/session': sessionRoute(),
    [CONFIG]: { status: 200, body: workspaceConfigFixture() },
    [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ version: 2, currentRunId: FIXTURE_RUN_ID })) },
    [RUN]: { status: 200, body: runDetailFixture() },
    [WORKFLOW]: { status: 200, body: fixedWorkflowFixture() },
    [ARTIFACTS]: { status: 200, body: artifactListFixture() },
    [TASK]: { status: 200, body: humanTaskResponseFixture() },
    [DELIVERY]: { status: 200, body: deliveryViewFixture() },
    [WATERMARKS]: { status: 200, body: watermarkViewFixture() },
    ...overrides,
  };
}

function renderPage(routes: Routes, path = `/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`) {
  const { fetchImpl, calls } = createFakeFetch(routes);
  const user = userEvent.setup({ delay: null });
  const view = renderApp({ fetch: fetchImpl, initialEntries: [path] });
  return { calls, user, ...view };
}

async function dag() {
  return within(await screen.findByRole('region', { name: '固定流程 DAG' }));
}

describe('the DAG is the frozen definition and never a progress meter', () => {
  it('keeps execution state and business verdict apart, and never marks a node passed', async () => {
    renderPage(
      pageRoutes({
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [
              nodeHistoryFixture({ nodeId: 'plan', iteration: 1, state: 'DECIDED', verdict: 'approved' }),
              // A Task that finished, with no business verdict recorded at all.
              nodeHistoryFixture({
                nodeId: 'plan_review',
                iteration: 1,
                state: 'RUNNING',
                verdict: undefined,
                attempts: [
                  {
                    taskId: 'run_7c1a:task:plan_review:1',
                    attempt: 1,
                    attemptKind: 'initial',
                    state: 'COMPLETED',
                    inputDigest: DIGEST_A,
                    artifactIds: [],
                  },
                ],
              }),
            ],
          }),
        },
      }),
    );
    const graph = await dag();

    const review = graph.getByRole('button', { name: /plan_review/ });
    expect(review).toHaveTextContent('RUNNING');
    // A Task finished, and the node still reports no business verdict: the
    // execution fact is not promoted into an outcome.
    expect(review).toHaveTextContent('裁决 未记录');
    expect(review).not.toHaveTextContent(/通过|成功|批准|放行/);

    // The one node that does carry a verdict shows the owner's own token.
    const plan = graph.getByRole('button', { name: /^plan\s/ });
    expect(plan).toHaveTextContent('approved');
    expect(plan).toHaveTextContent('DECIDED');
  });

  it('shows a node the run has not materialised as unmaterialised, not as passed', async () => {
    renderPage(
      pageRoutes({
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [nodeHistoryFixture({ nodeId: 'plan', iteration: 1, state: 'DECIDED', verdict: 'approved' })],
          }),
        },
      }),
    );
    const graph = await dag();
    const end = graph.getByRole('button', { name: /^end\s/ });
    expect(end).toHaveTextContent('未物化');
    expect(end).not.toHaveTextContent(/通过|成功|批准/);
  });

  it('keeps a skipped node visible and counts it separately', async () => {
    renderPage(
      pageRoutes({
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [
              nodeHistoryFixture({ nodeId: 'plan', iteration: 1, state: 'DECIDED', verdict: 'passed' }),
              nodeHistoryFixture({ nodeId: 'design_tests', iteration: 1, state: 'SKIPPED', verdict: undefined }),
            ],
          }),
        },
      }),
    );
    const graph = await dag();
    expect(graph.getByRole('button', { name: /design_tests/ })).toHaveTextContent('SKIPPED');
    const summary = await screen.findByRole('region', { name: '本轮状态统计' });
    expect(summary).toHaveTextContent(/跳过（SKIPPED）\s*1 条/);
    expect(summary).toHaveTextContent(/已决（DECIDED）\s*1 条/);
  });

  it('reports the DAG topology it was given, including the edges the server froze', async () => {
    renderPage(pageRoutes());
    const graph = await dag();
    expect(graph.getByRole('button', { name: /^verify_change/ })).toHaveAccessibleDescription(
      /code、design_tests|design_tests、code/,
    );
  });
});

describe('earlier iterations are history, never the current result', () => {
  it('labels the current and the earlier rounds and keeps both readable', async () => {
    renderPage(
      pageRoutes({
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [
              nodeHistoryFixture({ nodeId: 'plan', iteration: 1, state: 'DECIDED', verdict: 'rejected' }),
              nodeHistoryFixture({ nodeId: 'plan', iteration: 2, state: 'DECIDED', verdict: 'needs_revision' }),
            ],
          }),
        },
      }),
    );
    await dag();
    const rounds = within(await screen.findByRole('region', { name: '运行轮次与历史' }));
    expect(rounds.getByText(/当前轮次（第 2 轮）/)).toBeInTheDocument();
    expect(rounds.getByText(/历史轮次（第 1 轮）/)).toBeInTheDocument();
    // The earlier verdict stays visible; it is neither dropped nor shown as the
    // node's current result.
    expect(rounds.getByText('rejected')).toBeInTheDocument();
    expect(rounds.getByText('needs_revision')).toBeInTheDocument();
  });

  it('marks a node that has earlier rounds on the graph itself', async () => {
    renderPage(
      pageRoutes({
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [
              nodeHistoryFixture({ nodeId: 'plan', iteration: 1, state: 'DECIDED', verdict: 'rejected' }),
              nodeHistoryFixture({ nodeId: 'plan', iteration: 2, state: 'DECIDED', verdict: 'approved' }),
            ],
          }),
        },
      }),
    );
    const graph = await dag();
    const plan = graph.getByRole('button', { name: /^plan\s/ });
    expect(plan).toHaveTextContent(/历史轮次 1/);
    // The current round is the one summarised on the card.
    expect(plan).toHaveTextContent('approved');
  });
});

describe('the run view never writes on its own', () => {
  /**
   * S04's guard here was "the run view offers no recovery control at all",
   * because recovery belonged to frontend:S05. S05 has since delivered that
   * surface, so the placeholder is replaced by the invariant it stood for,
   * stated more strongly: reading a run (or re-reading it, or opening a node)
   * still issues no write, and every recovery control is bound to a frozen
   * command token rather than to a bare verb that could be mistaken for a
   * reload of the data.
   */
  it('issues no write while loading and rebinding the view', async () => {
    const { calls, user } = renderPage(pageRoutes());
    const graph = await dag();
    await user.click(graph.getByRole('button', { name: /^plan\s/ }));
    await waitFor(() => expect(screen.getByRole('region', { name: '节点详情' })).toBeInTheDocument());
    expect(calls.filter((call) => call.method !== 'GET' && call.url.includes('/runs'))).toHaveLength(0);
  });

  it('binds every recovery control to the frozen command it submits', async () => {
    renderPage(pageRoutes());
    await dag();
    const actions = screen.getAllByRole('button').map((button) => button.textContent ?? '');
    // No control offers an unauthored verb: a recovery command is always
    // labelled with the owner's own kind token.
    for (const label of actions.filter((text) => /重试|rerun|重新运行|取消|cancel|恢复|recovery/i.test(text))) {
      expect(label).toMatch(/[(（](retry|rerun|cancel|repair|retry-knowledge-suggestion)[)）]/);
    }
  });
});

describe('selection is recoverable and keyboard reachable', () => {
  it('recovers the selected node from the URL', async () => {
    renderPage(pageRoutes(), `/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}?node=plan_review`);
    await dag();
    const panel = within(await screen.findByRole('region', { name: '节点详情' }));
    expect(panel.getByText('plan_review')).toBeInTheDocument();
  });

  it('writes the selection into the URL so the view can be shared', async () => {
    const { fetchImpl } = createFakeFetch(pageRoutes());
    const client = createApiClient({ fetch: fetchImpl });
    let search = '';
    function Probe() {
      search = useLocation().search;
      return null;
    }
    renderWithProviders(
      <>
        <Probe />
        <RunView
          client={client}
          workspaceId={WORKSPACE}
          workItemId={FIXTURE_WORK_ITEM_ID}
          runId={FIXTURE_RUN_ID}
          workItem={workItemFixture({ version: 2, currentRunId: FIXTURE_RUN_ID })}
          onReloadWorkItem={() => undefined}
          scope="test-scope"
          generation={1}
        />
      </>,
      { fetch: fetchImpl, initialEntries: [`/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`] },
    );
    const user = userEvent.setup({ delay: null });
    const graph = await dag();
    await user.click(graph.getByRole('button', { name: /^plan\s/ }));
    await waitFor(() => expect(search).toContain('node=plan'));
  });

  it('moves focus along the graph with the arrow keys and selects with Enter', async () => {
    renderPage(pageRoutes());
    const user = userEvent.setup({ delay: null });
    const graph = await dag();
    const plan = graph.getByRole('button', { name: /^plan\s/ });

    plan.focus();
    expect(plan).toHaveFocus();
    await user.keyboard('{ArrowRight}');
    expect(graph.getByRole('button', { name: /plan_review/ })).toHaveFocus();

    await user.keyboard('{ArrowRight}{ArrowRight}');
    const code = graph.getByRole('button', { name: /^code\s/ });
    expect(code).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(graph.getByRole('button', { name: /design_tests/ })).toHaveFocus();
    await user.keyboard('{ArrowUp}');
    expect(code).toHaveFocus();

    await user.keyboard('{Enter}');
    const panel = within(await screen.findByRole('region', { name: '节点详情' }));
    expect(panel.getByText('code')).toBeInTheDocument();
  });
});

describe('failures and empty results are stated as themselves', () => {
  it('reports an unreadable definition instead of drawing a partial graph', async () => {
    renderPage(pageRoutes({ [WORKFLOW]: { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } } }));
    const graph = await dag();
    expect(graph.getByRole('alert')).toHaveTextContent('RESOURCE_UNAVAILABLE');
    // Only the retry control is present: no node is offered without a graph.
    expect(graph.queryAllByRole('button', { pressed: false })).toHaveLength(0);
    expect(graph.queryAllByRole('button', { pressed: true })).toHaveLength(0);
  });

  it('reports an unreadable run projection with a retry', async () => {
    renderPage(pageRoutes({ [RUN]: { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } } }));
    expect(await screen.findByRole('heading', { name: '无法读取运行投影' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
  });

  it('shows an empty artifact list as an empty result, not as an error', async () => {
    renderPage(pageRoutes({ [ARTIFACTS]: { status: 200, body: artifactListFixture([]) } }));
    expect(await screen.findByRole('heading', { name: '还没有已发布产物' })).toBeInTheDocument();
  });
});

describe('artifacts are references, not invented links', () => {
  it('names each published artifact by identity and digest and links the producing Task', async () => {
    renderPage(
      pageRoutes({
        [ARTIFACTS]: { status: 200, body: artifactListFixture([artifactFixture({ artifactId: 'art_plan_1' })]) },
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [
              nodeHistoryFixture({
                nodeId: 'plan',
                iteration: 1,
                state: 'DECIDED',
                verdict: 'approved',
                attempts: [
                  {
                    taskId: 'run_7c1a:task:plan:1',
                    attempt: 1,
                    attemptKind: 'initial',
                    state: 'COMPLETED',
                    inputDigest: DIGEST_A,
                    artifactIds: ['art_plan_1'],
                  },
                ],
              }),
            ],
          }),
        },
      }),
    );
    const artifacts = within(await screen.findByRole('region', { name: '产物与证据' }));
    expect(artifacts.getByText('art_plan_1')).toBeInTheDocument();
    expect(artifacts.getByText(/已发布（PUBLISHED）/)).toBeInTheDocument();
    // No content URL is frozen for the browser, so none is offered.
    expect(artifacts.queryAllByRole('link')).toHaveLength(0);
  });

  it('reports an artifact reference the server did not publish', async () => {
    renderPage(
      pageRoutes({
        [ARTIFACTS]: { status: 200, body: artifactListFixture([]) },
        [RUN]: {
          status: 200,
          body: runDetailFixture({
            nodes: [
              nodeHistoryFixture({
                nodeId: 'plan',
                iteration: 1,
                state: 'DECIDED',
                verdict: 'approved',
                attempts: [
                  {
                    taskId: 'run_7c1a:task:plan:1',
                    attempt: 1,
                    attemptKind: 'initial',
                    state: 'COMPLETED',
                    inputDigest: DIGEST_A,
                    artifactIds: ['art_missing'],
                  },
                ],
              }),
            ],
          }),
        },
      }),
    );
    await dag();
    const user = userEvent.setup({ delay: null });
    await user.click(screen.getByRole('button', { name: /^plan\s/ }));
    const panel = within(await screen.findByRole('region', { name: '节点详情' }));
    expect(panel.getByText(/art_missing/)).toBeInTheDocument();
    expect(panel.getByText(/未出现在已发布清单中/)).toBeInTheDocument();
  });
});

describe('the composite page keeps its existing facts', () => {
  it('still renders the run identity and the knowledge binding next to the DAG', async () => {
    renderPage(
      pageRoutes({
        [RUN]: { status: 200, body: runDetailFixture({ knowledgeRecommendation: knowledgeRecommendationFixture() }) },
      }),
    );
    expect(await screen.findByRole('heading', { name: '交付运行' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '知识绑定' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '固定流程 DAG' })).toBeInTheDocument();
  });

  it('renders the frozen definition identity the run was admitted with', async () => {
    renderPage(pageRoutes());
    const graph = await dag();
    expect(graph.getByText(/kowa-fixed-workflow@1/)).toBeInTheDocument();
  });
});

// Keeps the fixture import meaningful for the route table above.
void sessionBootstrapFixture;
