import { describe, expect, it } from 'vitest';
import {
  decodeAdmissionResponse,
  decodeApiErrorCategory,
  decodeArtifactListResponse,
  decodeDeliveryView,
  decodeWatermarkView,
  decodeExecutionConfigResponse,
  decodeFixedWorkflowView,
  decodeGitRemoteStatusResponse,
  decodeHumanTaskResponse,
  decodeLoginResponse,
  decodeRecoveryResponse,
  decodeRunDetail,
  decodeRuntimeListResponse,
  decodeRuntimeViewResponse,
  decodeSessionBootstrapResponse,
  decodeWorkItemListResponse,
  decodeWorkItemResponse,
  decodeWorkspaceConfig,
} from '../../src/api/decode';
import { API_ERROR_CATEGORIES } from '../../src/api/contracts';
import { layoutDag } from '../../src/runs/dag';
import {
  DIGEST_A,
  admissionFixture,
  artifactFixture,
  admissionResponseFixture,
  artifactListFixture,
  checksObservationFixture,
  deliveryViewFixture,
  externalOperationFixture,
  pullRequestObservationFixture,
  reviewObservationFixture,
  verificationFixture,
  watermarkViewFixture,
  executionConfigFixture,
  fixedWorkflowFixture,
  gitRemoteStatusFixture,
  externalFactHumanTaskFixture,
  humanTaskFixture,
  humanTaskListFixture,
  humanTaskResponseFixture,
  recoveryResponseFixture,
  knowledgeRecommendationFixture,
  resolvedHumanTaskFixture,
  runDetailFixture,
  runtimeListFixture,
  runtimeViewFixture,
  sessionBootstrapFixture,
  workItemFixture,
  workItemListFixture,
  workItemResponseFixture,
  workspaceConfigFixture,
  FIXTURE_PR_NUMBER,
  FIXTURE_DELIVERY_COMMIT,
} from '../fixtures/contracts';
import { createValidator, readExamples, type ContractName } from '../fixtures/schema';

const validator = createValidator();

/** Owner examples whose value is the payload itself (not a request/expectation pair). */
function payload(value: unknown): unknown | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined;
  const object = value as Record<string, unknown>;
  return 'request' in object || 'expected' in object ? undefined : value;
}

describe('official examples stay bound to the frozen schemas', () => {
  const contracts: ContractName[] = [
    'web-session',
    'identity-workspace',
    'runtime-web',
    'delivery-governance',
    'workitem',
    'web-application',
    'observability',
  ];

  it.each(contracts)('%s: every positive example validates', (name) => {
    const positives = readExamples(name).filter((example) => example.file.startsWith('positive-'));
    expect(positives.length).toBeGreaterThan(0);
    for (const example of positives) {
      const result = validator.validate(name, example.value);
      expect(result.valid, `${example.file}: ${result.errors}`).toBe(true);
    }
  });

  it.each(contracts)('%s: every schema-negative example is rejected', (name) => {
    const negatives = readExamples(name).filter((example) => example.file.startsWith('negative-schema-'));
    expect(negatives.length).toBeGreaterThan(0);
    for (const example of negatives) {
      const result = validator.validate(name, example.value);
      expect(result.valid, `${example.file} unexpectedly validates`).toBe(false);
    }
  });
});

describe('fixtures are contract-bound', () => {
  it('workspace config fixture validates', () => {
    const result = validator.validate('identity-workspace', workspaceConfigFixture());
    expect(result.valid, result.errors).toBe(true);
  });

  it('session bootstrap fixture validates', () => {
    const result = validator.validate('web-session', sessionBootstrapFixture());
    expect(result.valid, result.errors).toBe(true);
  });

  it('runtime fixtures validate in every freshness state', () => {
    const result = validator.validate('runtime-web', runtimeListFixture());
    expect(result.valid, result.errors).toBe(true);
    const view = validator.validate('runtime-web', runtimeViewFixture());
    expect(view.valid, view.errors).toBe(true);
    const config = validator.validate('runtime-web', executionConfigFixture());
    expect(config.valid, config.errors).toBe(true);
  });

  it('git remote fixture validates with both repository roles', () => {
    const result = validator.validate('delivery-governance', gitRemoteStatusFixture());
    expect(result.valid, result.errors).toBe(true);
  });

  it('the error envelope fixture validates for every category the frontend classifies', () => {
    // The union is bound to the widest S02-consumed family; the narrower
    // families freeze a subset and must accept every one of their own
    // categories too.
    for (const category of API_ERROR_CATEGORIES) {
      const result = validator.validate('web-application', { error: category });
      expect(result.valid, `${category}: ${result.errors}`).toBe(true);
    }
    for (const category of ['VALIDATION_ERROR', 'UNAUTHORIZED', 'FORBIDDEN', 'VERSION_CONFLICT', 'RESOURCE_UNAVAILABLE', 'POLICY_BLOCKED', 'INTERNAL_ERROR']) {
      const result = validator.validate('identity-workspace', { error: category });
      expect(result.valid, `${category}: ${result.errors}`).toBe(true);
    }
  });

  it('work item and admission fixtures validate', () => {
    expect(validator.validate('workitem', workItemResponseFixture()).valid).toBe(true);
    expect(validator.validate('workitem', workItemListFixture()).valid).toBe(true);
    expect(validator.validate('web-application', admissionResponseFixture()).valid).toBe(true);
    expect(validator.validate('web-application', runDetailFixture()).valid).toBe(true);
  });

  it('the fixed workflow and artifact fixtures validate', () => {
    const workflow = validator.validate('fixed-workflow', fixedWorkflowFixture());
    expect(workflow.valid, workflow.errors).toBe(true);
    const artifacts = validator.validate('web-application', artifactListFixture());
    expect(artifacts.valid, artifacts.errors).toBe(true);
    const empty = validator.validate('web-application', artifactListFixture([]));
    expect(empty.valid, empty.errors).toBe(true);
  });

  it('human task fixtures validate in both states and both completion modes', () => {
    const open = validator.validate('delivery-governance', humanTaskResponseFixture());
    expect(open.valid, open.errors).toBe(true);
    const resolved = validator.validate('delivery-governance', humanTaskResponseFixture(resolvedHumanTaskFixture()));
    expect(resolved.valid, resolved.errors).toBe(true);
    const list = validator.validate('delivery-governance', humanTaskListFixture());
    expect(list.valid, list.errors).toBe(true);
    const external = validator.validate('delivery-governance', humanTaskResponseFixture(externalFactHumanTaskFixture()));
    expect(external.valid, external.errors).toBe(true);
  });

  it('the recovery answer fixture validates in both outcomes', () => {
    for (const applied of [true, false]) {
      const result = validator.validate('web-application', recoveryResponseFixture({ applied }));
      expect(result.valid, result.errors).toBe(true);
    }
    for (const kind of ['retry', 'rerun', 'cancel', 'repair', 'retry-knowledge-suggestion'] as const) {
      const result = validator.validate('web-application', recoveryResponseFixture({ kind }));
      expect(result.valid, `${kind}: ${result.errors}`).toBe(true);
    }
  });
});

describe('decoders accept the official positive examples', () => {
  function example(name: ContractName, file: string): unknown {
    const found = readExamples(name).find((entry) => entry.file === file);
    expect(found, `${file} is missing`).toBeDefined();
    return found?.value;
  }

  it('decodes the session bootstrap snapshot', () => {
    const decoded = decodeSessionBootstrapResponse(example('web-session', 'positive-session-bootstrap.json'));
    expect(decoded.identity).toEqual({ githubUserId: '101', login: 'member-a' });
    expect(decoded.requestId).toBe('generation-2-request-1');
  });

  it('decodes the workspace configuration', () => {
    const decoded = decodeWorkspaceConfig(example('identity-workspace', 'positive-workspace-config.json'));
    expect(decoded.project?.access).toBe('read_write');
    expect(decoded.knowledge?.access).toBe('read');
  });

  it('decodes the runtime list with all three freshness states', () => {
    const decoded = decodeRuntimeListResponse(example('runtime-web', 'positive-runtime-list.json'));
    expect(decoded.runtimes.map((runtime) => runtime.freshness)).toEqual(['fresh', 'stale', 'unknown']);
    expect(decoded.runtimes[1]?.staleReason).toBe('expired');
    expect(decoded.runtimes[2]?.observation).toBeUndefined();
  });

  it('decodes the runtime view and both execution configurations', () => {
    expect(decodeRuntimeViewResponse(example('runtime-web', 'positive-runtime-view.json')).runtime.staleReason)
      .toBe('superseded-epoch');
    expect(decodeExecutionConfigResponse(example('runtime-web', 'positive-execution-config.json')).config.version).toBe(3);
    expect(decodeExecutionConfigResponse(example('runtime-web', 'positive-execution-config-zero.json')).config.version).toBe(0);
  });

  it('decodes the git remote status projection', () => {
    const decoded = decodeGitRemoteStatusResponse(example('delivery-governance', 'positive-git-remote-status.json'));
    expect(decoded.status.machineIdentity.state).toBe('matched');
    expect(decoded.status.repositories.map((repository) => repository.writeObservation)).toEqual(['none', 'none']);
  });

  it('decodes the work item and its list projection', () => {
    const item = decodeWorkItemResponse(example('workitem', 'positive-work-item.json'));
    expect(item.workItem.state).toBe('OPEN');
    expect(item.workItem.currentRunId).toBe('run_7c1a');
    expect(item.workItem.requirement.digest).toMatch(/^sha256:[0-9a-f]{64}$/);
    const list = decodeWorkItemListResponse(example('workitem', 'positive-work-item-list.json'));
    expect(list.kind).toBe('workItemList');
    expect(list.workItems.length).toBeGreaterThan(0);
  });

  it('decodes the admission with its frozen knowledge recommendation', () => {
    const admission = decodeAdmissionResponse(example('web-application', 'positive-admission.json'));
    expect(admission.replayed).toBe(false);
    expect(admission.workItemVersion).toBe(2);
    expect(admission.admission.projectCommitOid).toMatch(/^[0-9a-f]{40}$/);
    expect(admission.knowledgeRecommendation?.state).toBe('RECOMMENDED');
    expect(admission.knowledgeRecommendation?.candidates).toHaveLength(1);
    expect(admission.knowledgeRecommendation?.candidates[0]?.required).toBe(true);
    expect(admission.knowledgeRecommendation?.indexDigest).toMatch(/^sha256:[0-9a-f]{64}$/);
    expect(admission.knowledgeRecommendation?.humanTaskId).toBeDefined();
  });

  it('decodes the run detail projection without inventing a knowledge projection', () => {
    const detail = decodeRunDetail(example('web-application', 'positive-run-detail.json'));
    expect(detail.state).toBe('ACTIVE');
    expect(detail.knowledgeRecommendation).toBeUndefined();
    expect(detail.nodes.map((node) => node.nodeId)).toContain('knowledge_recommend');
    expect(detail.watermark.runUpdatedAt).toBeDefined();
  });

  it('decodes the frozen definition view the DAG is drawn from', () => {
    const view = decodeFixedWorkflowView(example('fixed-workflow', 'positive-fixed-workflow-view.json'));
    expect(view.name).toBe('kowa-fixed-workflow');
    expect(view.version).toBe(1);
    expect(view.order).toHaveLength(view.nodes.length);
    // The edges the drawing needs are the ones the owner froze: the layout is
    // derived from these and from nothing else.
    expect(view.nodes.find((node) => node.id === 'test')?.dependsOn).toEqual(['verify_change', 'design_tests']);
    expect(view.nodes.find((node) => node.id === 'publish_pr')?.dependsOn).toHaveLength(3);
    expect(view.order[0]).toBe('knowledge_recommend');
  });

  it('decodes the published artifact list', () => {
    const list = decodeArtifactListResponse(example('web-application', 'positive-artifact-list.json'));
    expect(list.kind).toBe('artifactList');
    expect(list.workItemId).toBeDefined();
  });

  it('decodes the open and resolved human task', () => {
    const open = decodeHumanTaskResponse(example('delivery-governance', 'positive-human-task.json'));
    expect(open.humanTask.state).toBe('OPEN');
    expect(open.humanTask.availableActions).toEqual(['respond']);
    expect(open.humanTask.request.fields.map((field) => field.name)).toEqual(['decision', 'comment']);
    const resolved = decodeHumanTaskResponse(example('delivery-governance', 'positive-human-task-resolved.json'));
    expect(resolved.humanTask.state).toBe('RESOLVED');
    expect(resolved.humanTask.availableActions).toEqual([]);
    expect(resolved.humanTask.response?.decision).toBe('approved');
    expect(resolved.humanTask.continuation?.intent).toBe('resume_node');
  });

  it('decodes the owner’s own recovery answer', () => {
    const applied = decodeRecoveryResponse(example('web-application', 'positive-recovery.json'));
    expect(applied.kind).toBe('rerun');
    expect(applied.applied).toBe(true);
    expect(applied.state).toBe('ACTIVE');
    expect(applied.detail).toBeDefined();
  });

  it('decodes the external-fact request that never accepts a Kowa answer', () => {
    const task = decodeHumanTaskResponse(example('delivery-governance', 'positive-external-fact-task.json')).humanTask;
    expect(task.request.completionMode).toBe('external_fact');
    expect(task.response).toBeUndefined();
  });

  it('decodes the login response envelope', () => {
    expect(decodeLoginResponse({ authorizationUrl: 'https://github.com/login/oauth/authorize?client_id=x' }))
      .toEqual({ authorizationUrl: 'https://github.com/login/oauth/authorize?client_id=x' });
  });

  it('refuses a login response that is not an http(s) navigation target', () => {
    // Defence in depth for the browser navigation the consumer performs: the
    // approved local OAuth stub is plain HTTP and stays valid, any other scheme
    // does not.
    expect(() => decodeLoginResponse({ authorizationUrl: 'javascript:alert(1)' })).toThrow(/http\(s\)/);
    expect(() => decodeLoginResponse({ authorizationUrl: 'data:text/html,<script>alert(1)</script>' })).toThrow(/http\(s\)/);
    expect(() => decodeLoginResponse({ authorizationUrl: '/relative/callback' })).toThrow(/absolute URL/);
    expect(decodeLoginResponse({ authorizationUrl: 'http://127.0.0.1:8099/stub/github/authorize?state=x' }).authorizationUrl)
      .toContain('127.0.0.1:8099');
  });

  it('decodes every frozen error category', () => {
    for (const category of API_ERROR_CATEGORIES) {
      expect(decodeApiErrorCategory({ error: category })).toBe(category);
    }
  });
});

describe('decoders reject the official negative cases', () => {
  it('refuses a recovery answer outside the frozen five commands', () => {
    // The frontend never widens the command set: a sixth kind is not a
    // projection it may act on.
    expect(() => decodeRecoveryResponse({ ...recoveryResponseFixture(), kind: 'rollback' })).toThrow(/recovery.kind/);
    // `applied` is a boolean fact, not a truthy string.
    expect(() => decodeRecoveryResponse({ ...recoveryResponseFixture(), applied: 'yes' })).toThrow(/recovery.applied/);
    // An answer with a field the contract does not freeze fails closed.
    expect(() => decodeRecoveryResponse({ ...recoveryResponseFixture(), nodeId: 'plan' })).toThrow(/unexpected field/);
  });

  it('refuses a human request whose narrowed action list is missing', () => {
    const task = humanTaskFixture();
    const tampered = { ...humanTaskResponseFixture(task), humanTask: { ...task, availableActions: undefined } };
    expect(() => decodeHumanTaskResponse(tampered)).toThrow(/availableActions/);
  });

  it('rejects schema-negative and semantic payloads', () => {
    const negatives = [
      ...readExamples('web-session').filter((example) => example.file.startsWith('negative-schema-')),
      ...readExamples('runtime-web').filter((example) => example.file.startsWith('negative-')),
      ...readExamples('delivery-governance').filter((example) => example.file.startsWith('negative-schema-')),
      ...readExamples('workitem').filter((example) => example.file.startsWith('negative-schema-')),
      ...readExamples('fixed-workflow').filter((example) => example.file.startsWith('negative-schema-')),
      ...readExamples('web-application').filter((example) => example.file.startsWith('negative-schema-')),
    ];
    expect(negatives.length).toBeGreaterThan(0);

    for (const example of negatives) {
      const value = payload(example.value);
      if (value === undefined) continue; // request/expectation pairs for other operations
      const decoders: ((input: unknown) => unknown)[] = [
        decodeSessionBootstrapResponse,
        decodeRuntimeListResponse,
        decodeRuntimeViewResponse,
        decodeExecutionConfigResponse,
        decodeGitRemoteStatusResponse,
        decodeWorkItemResponse,
        decodeWorkItemListResponse,
        decodeAdmissionResponse,
        decodeRunDetail,
        decodeFixedWorkflowView,
        decodeArtifactListResponse,
        decodeHumanTaskResponse,
        decodeRecoveryResponse,
      ];
      const accepted = decoders.some((decode) => {
        try {
          decode(value);
          return true;
        } catch {
          return false;
        }
      });
      expect(accepted, `${example.file} was accepted by a decoder`).toBe(false);
    }
  });

  it('rejects an unknown field on a runtime view', () => {
    const tampered = { ...runtimeViewFixture(), extraField: 'must fail closed' };
    expect(() => decodeRuntimeViewResponse(tampered)).toThrow(/unexpected field/);
  });

  it('rejects an unknown field inside a runtime', () => {
    const tampered = {
      ...runtimeViewFixture(),
      runtime: { ...runtimeViewFixture().runtime, runtimeToken: 'secret' },
    };
    expect(() => decodeRuntimeViewResponse(tampered)).toThrow(/unexpected field/);
  });

  it('rejects a knowledge repository that claims write access', () => {
    const config = workspaceConfigFixture();
    const tampered = {
      ...config,
      knowledge: { ...config.knowledge!, access: 'read_write' as const },
    };
    expect(() => decodeWorkspaceConfig(tampered)).toThrow(/role knowledge must have access read/);
  });

  it('rejects a work item that smuggles an actor or is missing its content digest', () => {
    const item = workItemFixture();
    expect(() => decodeWorkItemResponse({ ...workItemResponseFixture(item), actorId: '999' })).toThrow(/unexpected field/);
    expect(() =>
      decodeWorkItemResponse(workItemResponseFixture({ ...item, requirement: { version: 1, content: 'x' } as never })),
    ).toThrow(/missing field digest/);
  });

  it('rejects an admission that froze a branch name instead of a commit oid', () => {
    expect(() =>
      decodeAdmissionResponse(admissionResponseFixture({ admission: admissionFixture({ projectCommitOid: 'refs/heads/main' }) })),
    ).toThrow(/projectCommitOid has an invalid format/);
  });

  it('rejects a run projection that carries an unknown attempt state', () => {
    const detail = runDetailFixture();
    const tampered = {
      ...detail,
      nodes: [
        {
          ...detail.nodes[0],
          attempts: [
            {
              taskId: 'task-1',
              attempt: 1,
              attemptKind: 'initial',
              state: 'STARTED',
              inputDigest: DIGEST_A,
              artifactIds: [],
            },
          ],
        },
      ],
    };
    expect(() => decodeRunDetail(tampered)).toThrow(/attempt.state is not one of/);
  });

  it('rejects a human task that offers an action the owner refuses', () => {
    expect(() => decodeHumanTaskResponse(humanTaskResponseFixture(resolvedHumanTaskFixture()))).not.toThrow();
    expect(() =>
      decodeHumanTaskResponse(humanTaskResponseFixture({ ...resolvedHumanTaskFixture(), availableActions: ['respond'] })),
    ).toThrow(/must not carry available actions/);
    expect(() =>
      decodeHumanTaskResponse(humanTaskResponseFixture({ ...humanTaskFixture(), availableActions: ['approve'] as never })),
    ).toThrow(/not one of/);
  });

  it('rejects a definition view whose order or ids repeat', () => {
    const view = fixedWorkflowFixture();
    expect(() => decodeFixedWorkflowView({ ...view, order: [...view.order, view.order[0]] })).toThrow(/order carries a duplicate/);
    expect(() => decodeFixedWorkflowView({ ...view, nodes: [...view.nodes, view.nodes[0]] })).toThrow(/nodes carry a duplicate id/);
    // A short order is a wire-legal view that simply cannot be placed; that is
    // the layout's refusal to make, not the decoder's, and it must not be
    // silently drawn.
    const shortened = { ...view, order: view.order.slice(1) };
    expect(() => decodeFixedWorkflowView(shortened)).not.toThrow();
    expect(() => layoutDag(shortened.nodes, shortened.order)).toThrow(/不一致/);
  });

  it('rejects a definition view carrying a doubled edge or an unknown node type', () => {
    const view = fixedWorkflowFixture();
    const nodes = view.nodes.map((node) =>
      node.id === 'verify_change' ? { ...node, dependsOn: ['code', 'code'] } : node,
    );
    expect(() => decodeFixedWorkflowView({ ...view, nodes })).toThrow(/duplicate/);
    const wrongType = view.nodes.map((node) => (node.id === 'end' ? { ...node, type: 'terminal' } : node));
    expect(() => decodeFixedWorkflowView({ ...view, nodes: wrongType })).toThrow(/not one of/);
  });

  it('rejects an artifact list carrying an unknown state or an out-of-range size', () => {
    expect(() =>
      decodeArtifactListResponse(artifactListFixture([artifactFixture({ state: 'ARCHIVED' as never })])),
    ).toThrow(/not one of/);
    expect(() =>
      decodeArtifactListResponse(artifactListFixture([artifactFixture({ size: 99_999_999 })])),
    ).toThrow(/artifact.size/);
    expect(() =>
      decodeArtifactListResponse({ ...artifactListFixture(), artifacts: undefined }),
    ).toThrow(/must be an array|missing field/);
  });

  it('rejects a confirmed recommendation without its confirmation time', () => {
    expect(() =>
      decodeAdmissionResponse(admissionResponseFixture({
        knowledgeRecommendation: knowledgeRecommendationFixture({ state: 'CONFIRMED', humanTaskId: undefined }),
      })),
    ).toThrow(/confirmed recommendation carries confirmedAt/);
  });
});

describe('the delivery evidence and observation watermarks stay bound to their owners', () => {
  function ownerExample(name: ContractName, file: string): unknown {
    const found = readExamples(name).find((entry) => entry.file === file);
    expect(found, `${file} is missing`).toBeDefined();
    return found?.value;
  }

  it('validates the delivery view in the closed and the still-open shape', () => {
    const closed = validator.validate('delivery-governance', deliveryViewFixture());
    expect(closed.valid, closed.errors).toBe(true);
    const open = validator.validate('delivery-governance', deliveryViewFixture({ closure: undefined }));
    expect(open.valid, open.errors).toBe(true);
  });

  it('validates every observation subject kind and the unknown shape', () => {
    const view = deliveryViewFixture({
      facts: [
        pullRequestObservationFixture(),
        reviewObservationFixture(),
        checksObservationFixture(),
        checksObservationFixture({
          observationId: 'obs_checks_unknown',
          unknown: true,
          checks: undefined,
          unknownReason: 'gh exited non-zero',
        }),
        checksObservationFixture({ observationId: 'obs_ref', subjectKind: 'branch_head', checks: undefined, ref: { commitOid: FIXTURE_DELIVERY_COMMIT } }),
      ],
    });
    const result = validator.validate('delivery-governance', view);
    expect(result.valid, result.errors).toBe(true);
  });

  it('validates the observability watermarks in the fresh, superseded and never-established shapes', () => {
    const result = validator.validate('observability', watermarkViewFixture());
    expect(result.valid, result.errors).toBe(true);
    const degraded = validator.validate('observability', watermarkViewFixture({
      observations: [
        { subjectKind: 'pull_request', subjectKey: FIXTURE_PR_NUMBER, lastAttemptAt: '2026-10-16T10:10:00Z', lastAttemptKnown: true, freshness: 'stale', freshnessReason: 'expired', supersededByFailure: false },
        { subjectKind: 'pull_request_review', subjectKey: '42/5001', lastAttemptAt: '2026-10-16T10:12:00Z', lastAttemptKnown: false, unknownReason: 'unavailable', freshness: 'unknown', freshnessReason: 'superseded-by-failure', supersededByFailure: true },
        { subjectKind: 'pull_request_checks', subjectKey: FIXTURE_DELIVERY_COMMIT, lastAttemptAt: '2026-10-16T10:12:30Z', lastAttemptKnown: false, unknownReason: 'unavailable', freshness: 'unknown', freshnessReason: 'never-established', supersededByFailure: true },
      ],
    }));
    expect(degraded.valid, degraded.errors).toBe(true);
  });

  it('decodes the owner delivery example and keeps every fact in its own subject', () => {
    const decoded = decodeDeliveryView(ownerExample('delivery-governance', 'positive-delivery-view.json'));
    expect(decoded.operations.map((operation) => operation.kind)).toEqual(['publish_pr']);
    expect(decoded.operations[0]?.result?.pullRequestAuthorId).toBe('135722937');
    // The projection's own order is preserved: the page never re-sorts facts.
    expect(decoded.facts.map((fact) => fact.subjectKind)).toEqual([
      'pull_request',
      'pull_request_review',
      'pull_request_checks',
      'pull_request_checks',
    ]);
    // The unknown row carries no fact at all, and its reason is preserved.
    const unknown = decoded.facts.find((fact) => fact.unknown);
    expect(unknown?.unknownReason).toContain('gh exited non-zero');
    expect(unknown?.checks).toBeUndefined();
    expect(decoded.closure?.mergeObservationId).toBe('obs_pr_42');
  });

  it('decodes the owner watermark example, including the degraded subject', () => {
    const decoded = decodeWatermarkView(ownerExample('observability', 'positive-watermarks.json'));
    expect(decoded.observations.map((observation) => observation.freshness)).toEqual(['fresh', 'unknown']);
    expect(decoded.observations[1]?.freshnessReason).toBe('never-established');
    expect(decoded.observations[1]?.supersededByFailure).toBe(true);
    expect(decoded.freshnessWindowSeconds).toBe(900);
  });
});

describe('the delivery decoders refuse exactly the shapes the operator must not see', () => {
  const view = () => JSON.parse(JSON.stringify(deliveryViewFixture())) as Record<string, unknown>;
  const facts = (payload: unknown) => {
    const copy = view();
    copy.facts = payload;
    return copy;
  };

  it('refuses an unknown read that carries a fact', () => {
    expect(() =>
      decodeDeliveryView(facts([checksObservationFixture({ unknown: true, unknownReason: 'unavailable' })])),
    ).toThrow(/unknown observation must not carry a fact/);
  });

  it('refuses a successful read that carries no fact', () => {
    expect(() =>
      decodeDeliveryView(facts([checksObservationFixture({ checks: undefined })])),
    ).toThrow(/must carry its fact/);
  });

  it('refuses a subject kind carrying another subject\'s payload', () => {
    const smuggled = checksObservationFixture({ subjectKind: 'pull_request_review' });
    expect(() => decodeDeliveryView(facts([smuggled]))).toThrow(/does not belong to subject kind/);
  });

  it('refuses an operation whose request contradicts its kind', () => {
    const operation = externalOperationFixture();
    const copy = view();
    copy.operations = [{ ...operation, request: { ...operation.request, operation: 'verify_change' } }];
    expect(() => decodeDeliveryView(copy)).toThrow(/differs from operation.kind/);
  });

  it('refuses an operation that carries an action list the owner never freezes', () => {
    const copy = view();
    copy.operations = [{ ...externalOperationFixture(), availableActions: ['merge'] }];
    expect(() => decodeDeliveryView(copy)).toThrow(/unexpected field availableActions/);
  });

  it('refuses a verification whose subject contradicts its payload', () => {
    expect(() =>
      decodeDeliveryView(view() as never as Parameters<typeof decodeDeliveryView>[0] && {
        ...view(),
        verification: [verificationFixture({ subjectKind: 'commit', artifactId: 'art_1a2b3c' })],
      } as unknown as Parameters<typeof decodeDeliveryView>[0]),
    ).toThrow(/must not carry an artifact subject/);
  });

  it('refuses a watermark that publishes a credential shape as a diagnostic reason', () => {
    expect(() =>
      decodeWatermarkView(watermarkViewFixture({
        observations: [
          {
            subjectKind: 'pull_request',
            subjectKey: FIXTURE_PR_NUMBER,
            lastAttemptAt: '2026-10-16T10:10:00Z',
            lastAttemptKnown: false,
            // Assembled at run time on purpose: the value must have the shape
            // the frozen `SafeText` refuses, without a literal credential in
            // the source (the repository's secret scan rejects those).
            unknownReason: ['gh auth failed with ', 'ghp', '_', '0123456789abcdefghij'].join(''),
            freshness: 'unknown',
            freshnessReason: 'never-established',
            supersededByFailure: true,
          },
        ],
      })),
    ).toThrow(/credential shape/);
  });

  it('refuses a delivery view carrying an unknown field anywhere', () => {
    const copy = view();
    copy.approvedBy = '101';
    expect(() => decodeDeliveryView(copy)).toThrow(/unexpected field approvedBy/);
  });
});
