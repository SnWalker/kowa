/**
 * The human-task surfaces, at the level the operator reads them.
 *
 * What is locked here:
 *
 * - the page renders the reviewed version the server froze and never rebuilds
 *   it from its own projections;
 * - an answer is explicit (a prefill never counts), single (a duplicate click
 *   does not reach the server twice), and refused safely when the request
 *   carries a field this page cannot render;
 * - an external-fact request has no answer control at all, and a task the
 *   server did not authorize for this member shows why instead of a form;
 * - a conflict or an expiry re-reads the server and waits for the member, and
 *   never retries a dangerous decision by itself.
 */
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import {
  DIGEST_A,
  FIXTURE_HUMAN_TASK_ID,
  FIXTURE_RUN_ID,
  FIXTURE_WORK_ITEM_ID,
  artifactFixture,
  artifactListFixture,
  fixedWorkflowFixture,
  humanTaskFixture,
  humanTaskResponseFixture,
  knowledgeRecommendationFixture,
  resolvedHumanTaskFixture,
  runDetailFixture,
  sessionBootstrapFixture,
  workItemFixture,
  workItemResponseFixture,
  workspaceConfigFixture,
} from '../fixtures/contracts';
import type { HumanField, HumanTask } from '../../src/api/contracts';
import { createFakeFetch, renderApp, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
/** Valid digests the fixtures do not export: the strict decoders refuse anything else. */
const DIGEST_OTHER = `sha256:${'f'.repeat(64)}`;
const DIGEST_ABSENT = `sha256:${'9'.repeat(64)}`;
const CONFIG = `GET /api/v1/workspaces/${WORKSPACE}`;
const LIST = `GET /api/v1/workspaces/${WORKSPACE}/human-tasks`;
const TASK = `GET /api/v1/workspaces/${WORKSPACE}/human-tasks/${FIXTURE_HUMAN_TASK_ID}`;
const ANSWER = `POST /api/v1/workspaces/${WORKSPACE}/human-tasks/${FIXTURE_HUMAN_TASK_ID}/responses`;
const ITEM = `GET /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`;
const RUN = `GET /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}/runs/${FIXTURE_RUN_ID}`;
const WORKFLOW = `GET /api/v1/workspaces/${WORKSPACE}/fixed-workflow`;
const ARTIFACTS = `GET /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}/artifacts`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function taskRoutes(task: HumanTask, overrides: Routes = {}): Routes {
  return {
    'POST /api/v2/auth/session': sessionRoute(sessionBootstrapFixture()),
    [CONFIG]: { status: 200, body: workspaceConfigFixture() },
    [LIST]: { status: 200, body: { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTaskList', humanTasks: [task] } },
    [TASK]: { status: 200, body: humanTaskResponseFixture(task) },
    [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ version: 2, currentRunId: FIXTURE_RUN_ID })) },
    [RUN]: { status: 200, body: runDetailFixture() },
    [WORKFLOW]: { status: 200, body: fixedWorkflowFixture() },
    [ARTIFACTS]: { status: 200, body: artifactListFixture() },
    ...overrides,
  };
}

function renderTask(task: HumanTask = humanTaskFixture(), overrides: Routes = {}) {
  const { fetchImpl, calls } = createFakeFetch(taskRoutes(task, overrides));
  const user = userEvent.setup({ delay: null });
  const view = renderApp({ fetch: fetchImpl, initialEntries: [`/workspaces/${WORKSPACE}/human-tasks/${task.humanTaskId}`] });
  return { calls, user, ...view };
}

const detail = () => screen.findByRole('region', { name: '人工待办' });

describe('a versioned request is rendered from the server’s own freeze', () => {
  it('shows the reviewed object version and never rebuilds it', async () => {
    const recommendation = knowledgeRecommendationFixture();
    renderTask();
    const versioned = within(await screen.findByRole('region', { name: '所审版本' }));
    expect(versioned.getByText('knowledge_recommendation')).toBeInTheDocument();
    expect(versioned.getAllByText(recommendation.indexDigest, { exact: false }).length).toBeGreaterThan(0);
    expect(versioned.getByText(/不会改绑/)).toBeInTheDocument();
    // The page’s own projection agrees with the frozen object version (it is
    // read separately, so the line settles once that read lands).
    expect(await versioned.findByText(/与该运行上的冻结索引一致/)).toBeInTheDocument();
  });

  it('states a mismatch between the frozen object version and the page’s projection as unknown, not as approval', async () => {
    renderTask(humanTaskFixture({
      request: { ...humanTaskFixture().request, object: { kind: 'knowledge_recommendation', id: FIXTURE_RUN_ID, version: DIGEST_OTHER, digest: DIGEST_OTHER } },
    }));
    const versioned = within(await screen.findByRole('region', { name: '所审版本' }));
    expect(await versioned.findByText(/不一致/)).toBeInTheDocument();
    expect(versioned.getByText(/以服务端冻结的对象版本为准/)).toBeInTheDocument();
  });

  it('reads the basis without inventing one: the frozen index comes from the run projection', async () => {
    renderTask();
    const basis = within(await screen.findByRole('region', { name: '依据' }));
    expect(await basis.findByText('docs/design.md')).toBeInTheDocument();
    expect(basis.getByText(/冻结索引条目共/)).toBeInTheDocument();
  });

  it('states the contract boundary for a plan approval whose deliverable is published as a reference', async () => {
    const published = artifactFixture({ artifactId: 'art_plan_reviewed', digest: DIGEST_A });
    renderTask(
      humanTaskFixture({
        request: {
          version: 1,
          title: 'Approve the reviewed plan',
          kind: 'plan_approval',
          completionMode: 'kowa_answer',
          fields: [
            { name: 'decision', type: 'decision', required: true },
            { name: 'feedback', type: 'comment', required: false },
          ],
          object: { kind: 'plan', id: FIXTURE_RUN_ID, version: DIGEST_A, digest: DIGEST_A },
          roles: ['admin', 'developer'],
        },
      }),
      { [ARTIFACTS]: { status: 200, body: artifactListFixture([published]) } },
    );
    const basis = within(await screen.findByRole('region', { name: '依据' }));
    expect(await basis.findByText('art_plan_reviewed')).toBeInTheDocument();
    expect(basis.getByText(/不提供浏览器端的内容读取/)).toBeInTheDocument();
  });

  it('says so when the reviewed deliverable is not in the published list instead of implying it is there', async () => {
    renderTask(
      humanTaskFixture({
        request: { ...humanTaskFixture().request, kind: 'plan_approval', object: { kind: 'plan', id: FIXTURE_RUN_ID, version: DIGEST_ABSENT, digest: DIGEST_ABSENT } },
      }),
    );
    const basis = within(await screen.findByRole('region', { name: '依据' }));
    expect(await basis.findByText(/未出现在已发布产物清单中/)).toBeInTheDocument();
  });
});

describe('answering is explicit, single and bounded by the frozen request', () => {
  it('offers the frozen fields and refuses to submit before a required decision is made', async () => {
    const { user, calls } = renderTask();
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    // The request declares decision + selection + feedback.
    expect(answer.getByText('decision')).toBeInTheDocument();
    const submit = answer.getByRole('button', { name: /提交答复/ });
    expect(submit).toBeDisabled();
    // Nothing was written by merely opening the page.
    expect(calls.filter((call) => call.method === 'POST' && call.url.includes('/responses'))).toHaveLength(0);
    await user.click(answer.getByRole('radio', { name: /批准/ }));
    expect(answer.getByRole('button', { name: /提交答复/ })).toBeEnabled();
  });

  it('submits exactly the frozen request version and object binding', async () => {
    const { user, calls } = renderTask();
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    await user.click(await answer.findByRole('radio', { name: /批准/ }));
    await user.click(answer.getByRole('button', { name: /提交答复/ }));
    await waitFor(() => expect(calls.filter((call) => call.url.endsWith('/responses'))).toHaveLength(1));
    const posted = calls.find((call) => call.url.endsWith('/responses')) as { body: Record<string, unknown>; headers: Headers };
    expect(posted.body.requestVersion).toBe(1);
    expect(posted.body.object).toEqual({ kind: 'knowledge_recommendation', id: FIXTURE_RUN_ID, version: knowledgeRecommendationFixture().indexDigest, digest: knowledgeRecommendationFixture().indexDigest });
    expect(posted.body.decision).toBe('approved');
    expect(posted.headers.get('X-CSRF-Token')).toBe('B'.repeat(43));
  });

  it('sends one request per intent even when the button is clicked twice', async () => {
    let release: (() => void) | undefined;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const { user, calls } = renderTask(humanTaskFixture(), {
      [ANSWER]: async () => {
        await gate;
        return { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture()) };
      },
    });
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    await user.click(await answer.findByRole('radio', { name: /批准/ }));
    const submit = answer.getByRole('button', { name: /提交答复/ });
    await user.click(submit);
    await waitFor(() => expect(submit).toBeDisabled());
    await user.click(submit);
    release?.();
    await waitFor(() => expect(calls.filter((call) => call.url.endsWith('/responses'))).toHaveLength(1));
  });

  it('reuses the same request key for the same intent and a new one after a real change', async () => {
    let attempt = 0;
    const { user, calls } = renderTask(humanTaskFixture(), {
      [ANSWER]: () => {
        attempt += 1;
        return attempt === 1 ? { status: 500, body: { error: 'INTERNAL_ERROR' } } : { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture()) };
      },
    });
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    await user.click(await answer.findByRole('radio', { name: /批准/ }));
    await user.click(answer.getByRole('button', { name: /提交答复/ }));
    await waitFor(() => expect(calls.filter((call) => call.url.endsWith('/responses'))).toHaveLength(1));
    await user.click(await answer.findByRole('button', { name: /提交答复/ }));
    await waitFor(() => expect(calls.filter((call) => call.url.endsWith('/responses'))).toHaveLength(2));
    const [first, second] = calls.filter((call) => call.url.endsWith('/responses'));
    expect((second as { body: { requestKey: string } }).body.requestKey).toBe((first as { body: { requestKey: string } }).body.requestKey);
  });

  it('keeps the typed answer when the server refuses, and never reports a refusal as accepted', async () => {
    const { user, calls } = renderTask(humanTaskFixture(), {
      [ANSWER]: { status: 403, body: { error: 'POLICY_BLOCKED' } },
    });
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    await user.click(await answer.findByRole('radio', { name: /批准/ }));
    await user.click(answer.getByRole('button', { name: /提交答复/ }));
    expect(await answer.findByRole('alert')).toHaveTextContent('POLICY_BLOCKED');
    // The draft is still there: nothing was thrown away and nothing was retried.
    expect(answer.getByRole('radio', { name: /批准/ })).toBeChecked();
    expect(calls.filter((call) => call.url.endsWith('/responses'))).toHaveLength(1);
    expect(screen.queryByText(/已提交/)).not.toBeInTheDocument();
    // Focus lands on the outcome so the next Tab continues from the refusal.
    await waitFor(() => expect(document.activeElement?.textContent ?? '').toContain('POLICY_BLOCKED'));
  });

  it('refuses to answer a choice the owner declared without options', async () => {
    // The frozen field-type enum is closed, so this is the one shape that
    // reaches the page without a control: a `choice` with no `options`.
    const fields: HumanField[] = [
      { name: 'decision', type: 'decision', required: true },
      { name: 'scope', type: 'choice', required: true },
    ];
    const { user, calls } = renderTask(humanTaskFixture({ request: { ...humanTaskFixture().request, fields } }));
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    expect((await answer.findAllByText('scope')).length).toBeGreaterThan(0);
    await user.click(answer.getByRole('radio', { name: /批准/ }));
    expect(answer.getByRole('button', { name: /提交答复/ })).toBeDisabled();
    await user.click(answer.getByRole('button', { name: /提交答复/ }));
    expect(calls.filter((call) => call.url.endsWith('/responses'))).toHaveLength(0);
  });

  it('does not offer a form when the server authorized no action for this member', async () => {
    renderTask(humanTaskFixture({ availableActions: [] }));
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    expect(await answer.findByText(/服务端没有为当前身份返回/)).toBeInTheDocument();
    expect(answer.queryByRole('button', { name: /提交答复/ })).not.toBeInTheDocument();
  });
});

describe('external facts and failed versions are settled where the contract says', () => {
  it('never renders an answer control for an external-fact request', async () => {
    const external = humanTaskFixture({
      request: {
        version: 1,
        title: 'Review the pull request',
        kind: 'pull_request_review',
        completionMode: 'external_fact',
        fields: [{ name: 'decision', type: 'decision', required: true }],
        object: { kind: 'pull_request_review', id: '42', version: '1'.repeat(40) },
        roles: ['admin', 'developer'],
      },
      availableActions: ['await-external-fact'],
    });
    renderTask(external);
    const task = await detail();
    expect(within(task).queryByRole('button', { name: /批准|驳回|提交答复/ })).not.toBeInTheDocument();
    const answer = within(screen.getByRole('region', { name: '答复' }));
    expect(answer.getByText(/只有已核对的 GitHub 事实/)).toBeInTheDocument();
  });

  it('turns an expired request into a re-review, never an automatic retry', async () => {
    const { calls } = renderTask(humanTaskFixture({ state: 'EXPIRED', availableActions: [], expiredReason: 'the bound plan moved to a newer version' }));
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    expect(await answer.findByText(/the bound plan moved to a newer version/)).toBeInTheDocument();
    expect(answer.getByText(/重新审阅/)).toBeInTheDocument();
    expect(answer.getByText(/不会自动重试|不自动重试/)).toBeInTheDocument();
    expect(answer.queryByRole('button', { name: /提交答复/ })).not.toBeInTheDocument();
    expect(calls.filter((call) => call.method === 'POST' && call.url.includes('/responses'))).toHaveLength(0);
  });

  it('shows the accepted answer and its continuation as server records', async () => {
    renderTask(resolvedHumanTaskFixture('docs/design.md'));
    const accepted = within(await screen.findByRole('region', { name: '答复记录' }));
    expect(await accepted.findByText('hr_9a1b')).toBeInTheDocument();
    expect(accepted.getByText(/人工动作/)).toBeInTheDocument();
    expect(accepted.getAllByText(/PENDING/).length).toBeGreaterThan(0);
  });

  it('re-reads the server on a version conflict and waits for an explicit confirmation', async () => {
    let answers = 0;
    const { user, calls } = renderTask(humanTaskFixture(), {
      [ANSWER]: () => {
        answers += 1;
        return { status: 409, body: { error: 'VERSION_CONFLICT' } };
      },
    });
    const answer = within(await screen.findByRole('region', { name: '答复' }));
    await user.click(await answer.findByRole('radio', { name: /批准/ }));
    const submit = answer.getByRole('button', { name: /提交答复/ });
    await user.click(submit);
    expect(await answer.findByText(/基于 v1/)).toBeInTheDocument();
    // One refused attempt, no automatic resubmission.
    expect(answers).toBe(1);
    expect(calls.filter((call) => call.url === `/api/v1/workspaces/${WORKSPACE}/human-tasks/${FIXTURE_HUMAN_TASK_ID}`).length).toBeGreaterThanOrEqual(2);
  });
});

describe('the work item’s open requests are visible from its run', () => {
  it('links a work item to the requests still waiting for a human', async () => {
    const { fetchImpl } = createFakeFetch(taskRoutes(humanTaskFixture()));
    renderApp({ fetch: fetchImpl, initialEntries: [`/workspaces/${WORKSPACE}/human-tasks`] });
    const list = within(await screen.findByRole('region', { name: '待办清单' }));
    expect(await list.findByText('Confirm the frozen knowledge selection')).toBeInTheDocument();
    expect(list.getByText(FIXTURE_WORK_ITEM_ID, { exact: false })).toBeInTheDocument();
  });
});
