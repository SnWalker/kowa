import { createHash } from 'node:crypto';
import type { Page, Route } from '@playwright/test';
import {
  admissionFixture,
  executionConfigFixture,
  gitRemoteStatusFixture,
  humanTaskFixture,
  knowledgeRecommendationFixture,
  resolvedHumanTaskFixture,
  runDetailFixture,
  runtimeListFixture,
  sessionBootstrapFixture,
  workItemFixture,
  workspaceConfigFixture,
} from '../../fixtures/contracts';
import type { HumanTask, KnowledgeRecommendation, WorkItem } from '../../../src/api/contracts';
import { actor, isControlPlane } from './mode';

export type RecordedCall = { method: string; path: string; headers: Record<string, string>; body: unknown };

type Handler = (call: RecordedCall, route: Route) => Promise<{ status: number; body?: unknown; headers?: Record<string, string> }> | { status: number; body?: unknown; headers?: Record<string, string> };

export type FixtureServer = {
  calls: RecordedCall[];
  /** Replace a route answer while the page runs (for expiry/conflict flows). */
  set(method: string, pathPattern: string, handler: Handler): void;
  /** Serve no authenticated session: every recovery request answers 401. */
  signOut(): void;
};

const json = (status: number, body?: unknown, headers?: Record<string, string>) => ({
  status,
  headers: {
    'content-type': 'application/json',
    'cache-control': 'no-store',
    'referrer-policy': 'no-referrer',
    'x-content-type-options': 'nosniff',
    ...headers,
  },
  body: body === undefined ? undefined : JSON.stringify(body),
});

/**
 * Contract-bound API double at the HTTP boundary: every answer is a frozen
 * contract payload (validated against the owner schemas in the unit suite) or
 * the frozen error envelope. It is a stub for the network, never a substitute
 * for the real control plane, which `scripts/web-e2e-control-plane.sh` runs.
 */
export async function installFixtures(
  page: Page,
  shared?: { authenticated: boolean },
  options: { emptyKnowledge?: boolean } = {},
): Promise<FixtureServer> {
  const calls: RecordedCall[] = [];
  // The double starts like a browser without a session cookie; the login
  // journey is what authenticates it, exactly as in the control plane.
  const state = shared ?? { authenticated: false };
  let authenticated = state.authenticated;
  let workspaceVersion = 2;
  let workspaceName = 'Kowa';
  const handlers: { method: string; pattern: RegExp; handler: Handler }[] = [];

  // S02 state: the work items the journey creates, the delivery run it admits
  // and the knowledge confirmation it answers. The double mirrors the frozen
  // owner rules it is a stub for (required entries cannot be dropped, answers
  // target the current request version) instead of inventing its own.
  const workItems = new Map<string, WorkItem>();
  let run: { runId: string; workItemId: string; recommendation: KnowledgeRecommendation; humanTask: HumanTask } | null = null;
  const admittedKeys = new Map<string, unknown>();
  let counter = 0;

  function digestOf(content: string): string {
    return `sha256:${createHash('sha256').update(content).digest('hex')}`;
  }

  function set(method: string, pathPattern: string, handler: Handler) {
    handlers.unshift({ method, pattern: new RegExp(pathPattern), handler });
  }

  await page.route('**/api/**', async (route) => {
    const request = route.request();
    const method = request.method();
    const url = new URL(request.url());
    const call: RecordedCall = {
      method,
      path: url.pathname,
      headers: request.headers(),
      body: request.postDataJSON?.() ?? undefined,
    };
    calls.push(call);

    for (const entry of handlers) {
      if (entry.method === method && entry.pattern.test(url.pathname)) {
        const answer = await entry.handler(call, route);
        await route.fulfill(json(answer.status, answer.body, answer.headers));
        return;
      }
    }

    // Recovery snapshot.
    authenticated = state.authenticated;
    if (method === 'POST' && url.pathname === '/api/v2/auth/session') {
      if (!authenticated) {
        await route.fulfill(json(401, { error: 'UNAUTHORIZED' }));
        return;
      }
      const requestId = (call.body as { requestId?: string } | undefined)?.requestId;
      await route.fulfill(json(200, sessionBootstrapFixture({ requestId: requestId ?? 'fixture', identity: actor })));
      return;
    }
    if (method === 'POST' && url.pathname === '/api/v2/auth/github/login') {
      // The stub authorize endpoint returns to the callback, which hands the
      // browser back to the app exactly like the owner's 303 UI handoff.
      // In the double the provider step returns straight to the app's frozen
      // callback, so the app's own 303 UI handoff is still exercised while the
      // external provider hop stays outside the browser journey.
      const returnTo = (call.body as { returnTo?: string } | undefined)?.returnTo ?? '/workspaces';
      // The real owner answers with an absolute authorization URL; the double
      // stays on the test origin but keeps that shape (a relative URL is not a
      // navigation target and is refused by the consumer).
      const origin = new URL(request.url()).origin;
      await route.fulfill(json(200, {
        authorizationUrl: `${origin}/api/v2/auth/github/callback?code=fixture-code&state=fixture-state&returnTo=${encodeURIComponent(returnTo)}`,
      }));
      return;
    }
    if (method === 'POST' && url.pathname === '/api/v2/auth/logout') {
      authenticated = false;
      state.authenticated = false;
      await route.fulfill(json(204));
      return;
    }
    if (method === 'POST' && url.pathname === '/api/v1/workspaces') {
      workspaceName = (call.body as { name: string }).name;
      workspaceVersion = 1;
      await route.fulfill(json(201, workspaceConfigFixture({ workspaceId: 'workspace-created', name: workspaceName, version: 1, members: [{ githubUserId: actor.githubUserId, role: 'admin' }], project: undefined, knowledge: undefined })));
      return;
    }
    if (method === 'GET' && /^\/api\/v1\/workspaces\/[^/]+$/.test(url.pathname)) {
      await route.fulfill(json(200, workspaceConfigFixture({ name: workspaceName, version: workspaceVersion })));
      return;
    }
    if (method === 'PUT' && url.pathname.endsWith('/repositories')) {
      workspaceVersion += 1;
      await route.fulfill(json(200, workspaceConfigFixture({ version: workspaceVersion })));
      return;
    }
    if (method === 'GET' && url.pathname.endsWith('/runtimes')) {
      await route.fulfill(json(200, runtimeListFixture()));
      return;
    }
    if (method === 'GET' && /\/runtimes\/[^/]+$/.test(url.pathname)) {
      await route.fulfill(json(200, { schemaVersion: 'kowa.runtime-web.v1', kind: 'runtimeView', runtime: runtimeListFixture().runtimes[0] }));
      return;
    }
    if (method === 'GET' && url.pathname.endsWith('/execution-config')) {
      await route.fulfill(json(200, executionConfigFixture()));
      return;
    }
    if (method === 'GET' && url.pathname.endsWith('/git-remote-status')) {
      await route.fulfill(json(200, gitRemoteStatusFixture()));
      return;
    }

    // ---- kowa.workitem.v1 / kowa.web-application.v1 / human tasks ----------

    const workItemsMatch = /^\/api\/v1\/workspaces\/([^/]+)\/work-items$/.exec(url.pathname);
    if (workItemsMatch && method === 'GET') {
      await route.fulfill(json(200, {
        schemaVersion: 'kowa.workitem.v1',
        kind: 'workItemList',
        workItems: [...workItems.values()].reverse(),
      }));
      return;
    }
    if (workItemsMatch && method === 'POST') {
      const body = call.body as { requestKey: string; title: string; requirement: { version: number; content: string }; acceptance: { version: number; content: string } };
      counter += 1;
      const item = workItemFixture({
        workItemId: `wi_fixture_${counter}`,
        workspaceId: workItemsMatch[1] as string,
        title: body.title,
        requirement: { version: body.requirement.version, digest: digestOf(body.requirement.content), content: body.requirement.content },
        acceptance: { version: body.acceptance.version, digest: digestOf(body.acceptance.content), content: body.acceptance.content },
        state: 'OPEN',
        version: 1,
        currentRunId: undefined,
        createdByGitHubUserId: actor.githubUserId,
      });
      workItems.set(item.workItemId, item);
      await route.fulfill(json(201, { schemaVersion: 'kowa.workitem.v1', kind: 'workItem', workItem: item }));
      return;
    }

    const itemMatch = /^\/api\/v1\/workspaces\/([^/]+)\/work-items\/([^/]+)$/.exec(url.pathname);
    if (itemMatch && method === 'GET') {
      const item = workItems.get(itemMatch[2] as string);
      if (!item) {
        await route.fulfill(json(503, { error: 'RESOURCE_UNAVAILABLE' }));
        return;
      }
      await route.fulfill(json(200, { schemaVersion: 'kowa.workitem.v1', kind: 'workItem', workItem: item }));
      return;
    }

    const runsMatch = /^\/api\/v1\/workspaces\/([^/]+)\/work-items\/([^/]+)\/runs$/.exec(url.pathname);
    if (runsMatch && method === 'POST') {
      const workItemId = runsMatch[2] as string;
      const item = workItems.get(workItemId);
      const body = call.body as { requestKey: string; expectedVersion: number };
      if (!item) {
        await route.fulfill(json(503, { error: 'RESOURCE_UNAVAILABLE' }));
        return;
      }
      if (item.version !== body.expectedVersion) {
        await route.fulfill(json(409, { error: 'VERSION_CONFLICT' }));
        return;
      }
      if (admittedKeys.has(body.requestKey)) {
        await route.fulfill(json(201, { ...(admittedKeys.get(body.requestKey) as object), replayed: true }));
        return;
      }
      const runId = `run_fixture_${counter}`;
      // The knowledge index of this double: the frozen default (one required
      // entry plus two optional ones) or a successfully read empty index.
      const recommendation = knowledgeRecommendationFixture({
        workItemId,
        workflowRunId: runId,
        state: 'RECOMMENDED',
        ...(options.emptyKnowledge === true ? { candidates: [] } : {}),
      });
      const baseTask = humanTaskFixture();
      const humanTask = humanTaskFixture({
        workItemId,
        workflowRunId: runId,
        request: {
          ...baseTask.request,
          object: {
            kind: 'knowledge_recommendation',
            id: runId,
            version: recommendation.indexDigest,
            digest: recommendation.indexDigest,
          },
        },
      });
      run = { runId, workItemId, recommendation, humanTask };
      const answer = {
        admission: admissionFixture({ workItemId, workflowRunId: runId, workspaceId: item.workspaceId }),
        workItemVersion: item.version + 1,
        knowledgeRecommendation: recommendation,
        replayed: false,
      };
      admittedKeys.set(body.requestKey, answer);
      workItems.set(workItemId, { ...item, version: item.version + 1, currentRunId: runId, updatedAt: new Date().toISOString() });
      await route.fulfill(json(201, answer));
      return;
    }

    const runMatch = /^\/api\/v1\/workspaces\/([^/]+)\/work-items\/([^/]+)\/runs\/([^/]+)$/.exec(url.pathname);
    if (runMatch && method === 'GET') {
      const item = workItems.get(runMatch[2] as string);
      if (!run || !item || run.runId !== runMatch[3]) {
        await route.fulfill(json(200, runDetailFixture({
          workItemId: runMatch[2] as string,
          workflowRunId: runMatch[3] as string,
          knowledgeRecommendation: undefined,
        })));
        return;
      }
      await route.fulfill(json(200, runDetailFixture({
        workItemId: run.workItemId,
        workflowRunId: run.runId,
        knowledgeRecommendation: run.recommendation,
      })));
      return;
    }

    if (method === 'GET' && /^\/api\/v1\/workspaces\/[^/]+\/human-tasks$/.test(url.pathname)) {
      await route.fulfill(json(200, {
        schemaVersion: 'kowa.delivery-governance.v1',
        kind: 'humanTaskList',
        humanTasks: run ? [run.humanTask] : [],
      }));
      return;
    }
    const taskMatch = /^\/api\/v1\/workspaces\/[^/]+\/human-tasks\/([^/]+)$/.exec(url.pathname);
    if (taskMatch && method === 'GET') {
      if (!run || run.humanTask.humanTaskId !== taskMatch[1]) {
        await route.fulfill(json(503, { error: 'RESOURCE_UNAVAILABLE' }));
        return;
      }
      await route.fulfill(json(200, { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTask', humanTask: run.humanTask }));
      return;
    }
    const answersMatch = /^\/api\/v1\/workspaces\/[^/]+\/human-tasks\/([^/]+)\/responses$/.exec(url.pathname);
    if (answersMatch && method === 'POST' && run && run.humanTask.humanTaskId === answersMatch[1]) {
      const body = call.body as { requestVersion: number; decision: string; values: Record<string, string> };
      if (body.requestVersion !== run.humanTask.request.version) {
        await route.fulfill(json(409, { error: 'VERSION_CONFLICT' }));
        return;
      }
      const selected = (body.values.selection ?? '').split('\n').map((line) => line.trim()).filter((line) => line !== '');
      const known = new Set(run.recommendation.candidates.map((candidate) => candidate.path));
      const droppedRequired = run.recommendation.candidates.some((candidate) => candidate.required && !selected.includes(candidate.path));
      const outsideIndex = selected.some((path) => !known.has(path));
      const selection = selected.join('\n');
      const resolved = resolvedHumanTaskFixture(selection);
      run.humanTask = {
        ...run.humanTask,
        state: 'RESOLVED',
        version: run.humanTask.version + 1,
        updatedAt: '2026-10-16T08:10:00Z',
        resolvedAt: '2026-10-16T08:10:00Z',
        resolvedByGitHubUserId: actor.githubUserId,
        resolutionKind: 'kowa_answer',
        availableActions: [],
        response: {
          ...resolved.response!,
          humanTaskId: run.humanTask.humanTaskId,
          workspaceId: run.humanTask.workspaceId,
          requestVersion: run.humanTask.request.version,
          object: run.humanTask.request.object,
          githubUserId: actor.githubUserId,
          values: { decision: 'approved', selection },
        },
        continuation: {
          ...resolved.continuation!,
          humanTaskId: run.humanTask.humanTaskId,
          workflowRunId: run.runId,
          nodeRunId: run.humanTask.nodeRunId,
        },
      };
      // The frozen acceptance is decoupled from the binding: the answer is
      // accepted as a task answer, and a selection the flow cannot bind (a
      // dropped required entry or a path outside the frozen index) leaves the
      // recommendation unconfirmed instead of inventing a success.
      if (body.decision === 'approved' && !droppedRequired && !outsideIndex) {
        run.recommendation = {
          ...run.recommendation,
          state: 'CONFIRMED',
          snapshotId: 'snap_fixture',
          bindingEventId: 'be_fixture',
          effectiveInputId: 'ei_fixture',
          confirmedAt: '2026-10-16T08:10:01Z',
        };
      }
      await route.fulfill(json(200, { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTask', humanTask: run.humanTask }));
      return;
    }

    throw new Error(`unmapped fixture request ${method} ${url.pathname}`);
  });

  // The app's callback answers with the same 303 UI handoff the owner freezes.
  await page.route(/\/api\/v2\/auth\/github\/callback/, async (route) => {
    const url = new URL(route.request().url());
    authenticated = true;
    state.authenticated = true;
    // The real callback answers 303 Location: origin + returnTo. Playwright
    // cannot fulfil a 3xx in every engine, so the double hands the browser back
    // with the equivalent document-level redirect and the exact returnTo the
    // server would have chosen.
    const returnTo = url.searchParams.get('returnTo') ?? '/workspaces';
    await route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: `<!doctype html><title>session established</title><meta http-equiv="refresh" content="0;url=${returnTo}">`,
    });
  });

  return {
    calls,
    set,
    signOut() {
      authenticated = false;
      state.authenticated = false;
    },
  };
}

const installed = new WeakMap<Page, Promise<FixtureServer>>();

/**
 * Only fixture mode installs the double; the control plane is answered for
 * real. Installing twice on the same page is a no-op so a caller that adds
 * route overrides keeps them.
 */
export async function installApi(
  page: Page,
  shared?: { authenticated: boolean },
  options: { emptyKnowledge?: boolean } = {},
): Promise<FixtureServer | undefined> {
  if (isControlPlane) return undefined;
  const existing = installed.get(page);
  if (existing) return existing;
  const pending = installFixtures(page, shared, options);
  installed.set(page, pending);
  return pending;
}
