/**
 * The semantics the human-task and recovery surfaces must not get wrong.
 *
 * Three red lines drive this file:
 *
 * - the answer a page can submit is bounded by what `human.detail` froze: the
 *   server's action list decides who may answer, the frozen request decides
 *   which fields exist, and a field this page cannot render is never submitted;
 * - an answer is a human act, never a claim about the object's outcome;
 * - a recovery command is a *candidate* the server decides on: the page may
 *   only offer it next to the server fact it rests on, and never on its own
 *   authority.
 */
import { describe, expect, it } from 'vitest';
import {
  type AnswerValues,
  actionPresentation,
  answerValues,
  completionModePresentation,
  decisionPresentation,
  fieldControl,
  humanFieldPresentation,
  objectBindingPresentation,
  reviewedArtifact,
  reviewedVersionCheck,
  revisionBudget,
  unrenderableFields,
} from '../../src/humantasks/presentation';
import { recoveryCandidates } from '../../src/runs/recovery';
import {
  artifactFixture,
  artifactListFixture,
  fixedWorkflowFixture,
  humanTaskFixture,
  knowledgeRecommendationFixture,
  nodeHistoryFixture,
  runDetailFixture,
  workItemFixture,
} from '../fixtures/contracts';
import type { HumanField } from '../../src/api/contracts';

/** Narrows the union so a refusal’s reason and an answer’s values are typed. */
function refusalReason(result: AnswerValues): string {
  if (result.ok) throw new Error('expected the answer to be refused');
  return result.reason;
}

function acceptedValues(result: AnswerValues): Record<string, string> {
  if (!result.ok) throw new Error(`expected an acceptable answer, got: ${result.reason}`);
  return result.values;
}

const WORK_ITEM = workItemFixture({ version: 2, currentRunId: 'run_7c1a' });

function candidatesFor(overrides: {
  run?: Parameters<typeof runDetailFixture>[0];
  workflow?: Parameters<typeof fixedWorkflowFixture>[0];
} = {}) {
  return recoveryCandidates({
    run: runDetailFixture(overrides.run),
    workItem: WORK_ITEM,
    workflow: fixedWorkflowFixture(overrides.workflow),
  });
}

describe('completion modes decide what may settle a request', () => {
  it('says a kowa_answer is settled inside Kowa by an authorized answer', () => {
    const mode = completionModePresentation('kowa_answer');
    expect(mode.answerable).toBe(true);
    expect(mode.detail).toContain('答复');
  });

  it('says an external_fact is settled only by a reconciled GitHub fact, never by this page', () => {
    const mode = completionModePresentation('external_fact');
    expect(mode.answerable).toBe(false);
    expect(mode.detail).toContain('GitHub');
    expect(mode.detail).toMatch(/不产生|不会产生|没有.*答复/);
  });

  it('takes the action list from the server, not from the request mode alone', () => {
    // An open kowa_answer the server did not authorize for this member.
    const undecided = actionPresentation(humanTaskFixture({ availableActions: [] }));
    expect(undecided.canAnswer).toBe(false);
    expect(undecided.detail).toContain('服务端');

    const assigned = actionPresentation(humanTaskFixture({ availableActions: ['respond'] }));
    expect(assigned.canAnswer).toBe(true);

    // An external-fact task with its own action: still no answer form.
    const external = actionPresentation(
      humanTaskFixture({ availableActions: ['await-external-fact'], request: { ...humanTaskFixture().request, completionMode: 'external_fact' } }),
    );
    expect(external.canAnswer).toBe(false);
    expect(external.awaitingExternalFact).toBe(true);
  });

  it('never authorizes an answer on a task the server did not open', () => {
    const resolved = actionPresentation(humanTaskFixture({ state: 'RESOLVED', availableActions: ['respond'] }));
    expect(resolved.canAnswer).toBe(false);
  });
});

describe('the dynamic form is bounded by the frozen request', () => {
  const fields: HumanField[] = [
    { name: 'decision', type: 'decision', required: true },
    { name: 'notes', type: 'comment', required: false },
  ];

  it('maps each frozen field type to one control', () => {
    expect(fieldControl('text')).toBe('input');
    expect(fieldControl('decision')).toBe('radio');
    expect(fieldControl('choice')).toBe('select');
    expect(fieldControl('comment')).toBe('textarea');
  });

  it('refuses to submit a request carrying a field this page cannot render', () => {
    const unknown = [{ name: 'signature', type: 'drawing' } as unknown as HumanField];
    expect(unrenderableFields(unknown)).toHaveLength(1);
    const refusal = answerValues({ fields: unknown, decision: 'approved', values: {} });
    expect(refusalReason(refusal)).toContain('signature');
  });

  it('refuses a choice the server declared without options instead of inventing a free value', () => {
    const choice = [{ name: 'scope', type: 'choice' as const, required: true }];
    expect(answerValues({ fields: choice, decision: 'submitted', values: { scope: 'x' } }).ok).toBe(false);
  });

  it('requires every required field before an answer exists', () => {
    const missing = answerValues({ fields, decision: 'approved', values: {} });
    expect(refusalReason(missing)).toContain('decision');

    const filled = answerValues({
      fields,
      decision: 'approved',
      values: { decision: 'approved', notes: 'looks right' },
    });
    expect(acceptedValues(filled)).toEqual({ decision: 'approved', notes: 'looks right' });
  });

  it('keeps the top-level decision equal to the decision field', () => {
    const disagreeing = answerValues({ fields, decision: 'rejected', values: { decision: 'approved' } });
    expect(refusalReason(disagreeing)).toContain('decision');
  });

  it('drops empty optional fields instead of submitting blanks the owner would refuse', () => {
    expect(acceptedValues(answerValues({ fields, decision: 'approved', values: { decision: 'approved', notes: '  ' } })))
      .toEqual({ decision: 'approved' });
  });

  it('labels a field with its frozen name and type so the form never invents one', () => {
    const presentation = humanFieldPresentation({ name: 'selection', type: 'text', required: false });
    expect(presentation.label).toBe('selection');
    expect(presentation.detail).toContain('text');
  });
});

describe('the reviewed version is the server’s, and no page re-binds it', () => {
  it('renders the object binding exactly as human.detail froze it', () => {
    const presentation = objectBindingPresentation({ kind: 'knowledge_recommendation', id: 'run_7c1a', version: 'sha256:abc', digest: 'sha256:abc' });
    expect(presentation.version).toBe('sha256:abc');
    expect(presentation.detail).toContain('不会改绑');
  });

  it('compares the frozen object version against the page’s own projection', () => {
    const recommendation = knowledgeRecommendationFixture();
    expect(reviewedVersionCheck({ kind: 'knowledge_recommendation', id: 'run_7c1a', version: recommendation.indexDigest }, recommendation.indexDigest)).toBe('match');
    expect(reviewedVersionCheck({ kind: 'knowledge_recommendation', id: 'run_7c1a', version: 'sha256:other' }, recommendation.indexDigest)).toBe('mismatch');
    expect(reviewedVersionCheck({ kind: 'knowledge_recommendation', id: 'run_7c1a', version: 'sha256:other' }, undefined)).toBe('unknown');
  });

  it('finds the reviewed deliverable only by its frozen digest', () => {
    const published = artifactFixture({ artifactId: 'art_reviewed', digest: 'sha256:aaaa' });
    const other = artifactFixture({ artifactId: 'art_other', digest: 'sha256:bbbb' });
    const list = artifactListFixture([published, other]).artifacts;
    expect(reviewedArtifact(list, 'sha256:aaaa')?.artifactId).toBe('art_reviewed');
    expect(reviewedArtifact(list, 'sha256:cccc')).toBeUndefined();
    expect(reviewedArtifact(list, undefined)).toBeUndefined();
  });
});

describe('an accepted answer is a human act, not an outcome', () => {
  it('describes the decision as the member’s act and never as the object’s result', () => {
    for (const decision of ['approved', 'rejected', 'submitted'] as const) {
      const presentation = decisionPresentation(decision);
      expect(presentation.label).toContain(decision);
      expect(presentation.detail).toContain('人工动作');
      expect(presentation.detail).not.toMatch(/放行|已完成|已通过|成功落地|已合并/);
    }
  });
});

describe('the revision budget is a frozen definition fact next to recorded rounds', () => {
  it('reads the budget from the frozen definition and counts only recorded rounds', () => {
    const rounds = [
      nodeHistoryFixture({ nodeId: 'plan', iteration: 1 }),
      nodeHistoryFixture({ nodeId: 'plan', iteration: 2 }),
    ];
    const budget = revisionBudget(fixedWorkflowFixture().nodes, 'plan_review', rounds);
    expect(budget.maxAutoRevisions).toBe(3);
    expect(budget.targetNodeId).toBe('plan');
    expect(budget.recordedRounds).toBe(2);
  });

  it('reports an unreadable definition as unknown instead of a zero budget', () => {
    const budget = revisionBudget(undefined, 'plan_review', []);
    expect(budget.maxAutoRevisions).toBeUndefined();
    expect(budget.detail).toContain('不可读');
  });
});

describe('recovery commands are candidates bound to a server fact', () => {
  it('offers only the five frozen kinds', () => {
    const kinds = candidatesFor().map((candidate) => candidate.kind);
    for (const kind of kinds) {
      expect(['retry', 'rerun', 'cancel', 'repair', 'retry-knowledge-suggestion']).toContain(kind);
    }
  });

  it('names the server fact behind every candidate, and the missing one when it cannot be offered', () => {
    for (const candidate of candidatesFor()) {
      expect(candidate.basis.length).toBeGreaterThan(0);
      if (!candidate.available) expect(candidate.basis).toMatch(/缺少|没有|未|已|不可读/);
    }
  });

  it('carries the version the owner actually checks for each kind', () => {
    const run = runDetailFixture({ version: 7 });
    const byKind = new Map(recoveryCandidates({ run, workItem: WORK_ITEM, workflow: fixedWorkflowFixture() }).map((c) => [c.kind, c]));

    // `cancel` is refused unless the caller echoes the run version.
    expect(byKind.get('cancel')?.versionGuard).toBe('run');
    expect(byKind.get('cancel')?.expectedVersion).toBe(7);
    // `repair` admits against the WorkItem version.
    expect(byKind.get('repair')?.versionGuard).toBe('workitem');
    expect(byKind.get('repair')?.expectedVersion).toBe(2);
    expect(byKind.get('repair')?.reasonRequired).toBe(true);
    // The owner does not version-guard retry/rerun/suggestion.
    expect(byKind.get('rerun')?.versionGuard).toBe('none');
    expect(byKind.get('retry-knowledge-suggestion')?.versionGuard).toBe('none');
  });

  it('offers a retry only where a failed or expired attempt really exists', () => {
    const failed = candidatesFor({
      run: {
        nodes: [
          nodeHistoryFixture({ nodeId: 'plan', state: 'EXECUTION_FAILED', attempts: [
            { taskId: 't1', attempt: 1, attemptKind: 'initial', state: 'FAILED', inputDigest: 'sha256:x', artifactIds: [] },
          ] }),
        ],
      },
    });
    expect(failed.some((candidate) => candidate.kind === 'retry' && candidate.nodeId === 'plan' && candidate.available)).toBe(true);

    const finished = candidatesFor({
      run: {
        nodes: [
          nodeHistoryFixture({ nodeId: 'plan', state: 'DECIDED', verdict: 'approved', attempts: [
            { taskId: 't1', attempt: 1, attemptKind: 'initial', state: 'COMPLETED', inputDigest: 'sha256:x', artifactIds: [] },
          ] }),
        ],
      },
    });
    expect(finished.some((candidate) => candidate.kind === 'retry' && candidate.available)).toBe(false);
    expect(finished.some((candidate) => candidate.kind === 'rerun' && candidate.nodeId === 'plan' && candidate.available)).toBe(true);
  });

  it('does not offer a rerun when the run no longer holds advancement authority', () => {
    const run = runDetailFixture({ nodes: [nodeHistoryFixture({ nodeId: 'plan', state: 'DECIDED', verdict: 'approved' })] });
    const otherRun = recoveryCandidates({
      run,
      workItem: workItemFixture({ version: 2, currentRunId: 'run_somewhere_else' }),
      workflow: fixedWorkflowFixture(),
    });
    expect(otherRun.some((candidate) => candidate.kind === 'rerun' && candidate.available)).toBe(false);
    expect(otherRun.some((candidate) => candidate.kind === 'rerun' && candidate.basis.includes('推进权'))).toBe(true);
  });

  it('does not offer a cancel for a run that already ended', () => {
    const ended = candidatesFor({ run: { state: 'SUCCEEDED' } });
    const cancel = ended.find((candidate) => candidate.kind === 'cancel');
    expect(cancel?.available).toBe(false);
    expect(cancel?.basis).toContain('SUCCEEDED');
  });

  it('offers the suggestion retry only for a closed work item whose suggestion branch failed', () => {
    const open = candidatesFor({ run: { state: 'SUCCEEDED', knowledgeSuggestion: { workflowRunId: 'run_7c1a', state: 'FAILED', attempts: 1, updatedAt: '2026-10-16T09:00:00Z', history: [] } } });
    expect(open.find((candidate) => candidate.kind === 'retry-knowledge-suggestion')?.available).toBe(false);

    const closed = recoveryCandidates({
      run: runDetailFixture({ state: 'SUCCEEDED', knowledgeSuggestion: { workflowRunId: 'run_7c1a', state: 'FAILED', attempts: 1, updatedAt: '2026-10-16T09:00:00Z', history: [] } }),
      workItem: workItemFixture({ version: 3, state: 'CLOSED', currentRunId: 'run_7c1a' }),
      workflow: fixedWorkflowFixture(),
    });
    expect(closed.find((candidate) => candidate.kind === 'retry-knowledge-suggestion')?.available).toBe(true);
  });

  it('says the server decides, never that the page authorized the command', () => {
    for (const candidate of candidatesFor()) {
      expect(candidate.authority).toContain('服务端');
    }
  });
});
