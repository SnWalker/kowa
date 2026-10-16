/**
 * The delivery panel's red lines, at the level of the pure semantics that drive
 * what the operator reads:
 *
 * - five identities stay apart, and no layer is allowed to stand in for
 *   another (`C05`: the Web initiator is never the GitHub writer);
 * - a review is only "applied to" the head it names, and a missing head is
 *   unknown rather than a pass;
 * - `merged` is an observed fact, never a closure;
 * - `checks: none` and a failed read are not passes.
 */
import { describe, expect, it } from 'vitest';
import {
  closureChecklist,
  commitUrl,
  fixRunBindings,
  githubRepositorySlug,
  identityConflicts,
  identityLayers,
  lastSuccessfulObservationAt,
  operationStatePresentation,
  pullRequestUrl,
  redactCredentialShapes,
  reviewApplicability,
  reviewStatePresentation,
  unknownObservations,
  verificationGroups,
  verificationResultPresentation,
  checksPresentation,
} from '../../src/delivery/presentation';
import {
  FIXTURE_DELIVERY_COMMIT,
  FIXTURE_MACHINE_GITHUB_ID,
  FIXTURE_MERGE_COMMIT,
  FIXTURE_PR_NUMBER,
  FIXTURE_REVIEWER_GITHUB_ID,
  checksObservationFixture,
  closureRecordFixture,
  deliveryVerificationFixture,
  externalOperationFixture,
  pullRequestObservationFixture,
  reviewObservationFixture,
  runDetailFixture,
  verificationFixture,
  workItemFixture,
} from '../fixtures/contracts';

describe('the five identities are carried separately and never impersonate each other', () => {
  it('labels every layer with the fact it comes from', () => {
    const layers = identityLayers({
      workItem: workItemFixture(),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
    });
    const byKey = new Map(layers.map((layer) => [layer.key, layer]));

    // The Web initiator is the run's initiator and nothing else.
    expect(byKey.get('web-initiator')?.value).toContain('101');
    expect(byKey.get('web-initiator')?.source).toContain('startedByGitHubUserId');
    // The machine gh account is its own layer, from its own field.
    expect(byKey.get('machine-run')?.value).toContain(FIXTURE_MACHINE_GITHUB_ID);
    expect(byKey.get('machine-run')?.source).toContain('gitExecutionUserId');
    // The PR author is what GitHub recorded, not what the run expected.
    expect(byKey.get('pr-author')?.value).toContain(FIXTURE_MACHINE_GITHUB_ID);
    expect(byKey.get('pr-author')?.source).toContain('pullRequestAuthorId');
    // Every layer keeps a distinct key.
    expect(new Set(layers.map((layer) => layer.key)).size).toBe(layers.length);
  });

  it('does not present the Web initiator as the GitHub writer of the commit', () => {
    const layers = identityLayers({
      workItem: workItemFixture(),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
    });
    const writer = layers.filter((layer) => layer.key !== 'web-initiator');
    for (const layer of writer) {
      expect(layer.key).not.toBe('web-initiator');
    }
    // The Web initiator layer must not claim ownership of anything outside it.
    const webLayer = layers.find((layer) => layer.key === 'web-initiator')!;
    expect(webLayer.source).not.toContain('pullRequestAuthorId');
    expect(webLayer.note ?? '').toContain('不');
  });

  it('states plainly that no consumed contract carries the commit author/committer', () => {
    const layers = identityLayers({
      workItem: workItemFixture(),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
    });
    const signature = layers.find((layer) => layer.key === 'commit-signature');
    expect(signature).toBeDefined();
    // The layer exists so the operator sees the gap instead of inferring an
    // author from a name that was never recorded.
    expect(signature!.value).toContain('未');
    expect(signature!.source).toBe('—');
  });

  it('reports a PR whose actual author is not the frozen machine account as a conflict', () => {
    const operation = externalOperationFixture({
      result: { ...externalOperationFixture().result!, pullRequestAuthorId: FIXTURE_REVIEWER_GITHUB_ID },
    });
    const conflicts = identityConflicts(operation, runDetailFixture());
    expect(conflicts.map((conflict) => conflict.code)).toContain('pr-author-mismatch');
  });

  it('reports a result commit that differs from the request as a conflict, and never as success', () => {
    const operation = externalOperationFixture({
      result: { ...externalOperationFixture().result!, commitOid: FIXTURE_MERGE_COMMIT },
    });
    const conflicts = identityConflicts(operation, runDetailFixture());
    expect(conflicts.map((conflict) => conflict.code)).toContain('commit-mismatch');
  });

  it('reports an operation account that differs from the run frozen account', () => {
    const operation = externalOperationFixture({
      request: { ...externalOperationFixture().request, expectedGitHubUserId: FIXTURE_REVIEWER_GITHUB_ID },
    });
    const conflicts = identityConflicts(operation, runDetailFixture());
    expect(conflicts.map((conflict) => conflict.code)).toContain('operation-account-differs-from-run');
  });

  it('finds no conflict when every recorded identity agrees', () => {
    expect(identityConflicts(externalOperationFixture(), runDetailFixture())).toEqual([]);
  });
});

describe('a review applies to a head or it does not; never "probably"', () => {
  it('treats a review on the current head as applicable', () => {
    expect(reviewApplicability(reviewObservationFixture().review, FIXTURE_DELIVERY_COMMIT)).toBe('applies-to-head');
  });

  it('marks a review on an older commit as superseded by the head drift', () => {
    expect(reviewApplicability(reviewObservationFixture().review, FIXTURE_MERGE_COMMIT)).toBe('superseded-by-head');
  });

  it('treats a review without a commit, or an unknown head, as unknown rather than applicable', () => {
    const withoutCommit = { ...reviewObservationFixture().review!, commitOid: undefined };
    expect(reviewApplicability(withoutCommit, FIXTURE_DELIVERY_COMMIT)).toBe('unknown');
    expect(reviewApplicability(reviewObservationFixture().review, undefined)).toBe('unknown');
    expect(reviewApplicability(undefined, FIXTURE_DELIVERY_COMMIT)).toBe('unknown');
  });

  it('carries the raw GitHub verdict without inventing one for a state it does not know', () => {
    expect(reviewStatePresentation('APPROVED').label).toContain('APPROVED');
    expect(reviewStatePresentation('CHANGES_REQUESTED').tone).toBe('warn');
    const unknown = reviewStatePresentation('SOMETHING_NEW');
    expect(unknown.recognised).toBe(false);
    // The raw value is still shown: the operator must see what GitHub said.
    expect(unknown.label).toContain('SOMETHING_NEW');
  });
});

describe('checks report four distinct states and never read "none" as a pass', () => {
  it('keeps none, pending, failed and passed apart', () => {
    const none = checksPresentation({ commitOid: FIXTURE_DELIVERY_COMMIT, state: 'none', total: 0, failed: 0, pending: 0 });
    const passed = checksPresentation({ commitOid: FIXTURE_DELIVERY_COMMIT, state: 'passed', total: 2, failed: 0, pending: 0 });
    expect(none.tone).not.toBe('ok');
    expect(none.detail).toContain('不是');
    expect(passed.tone).toBe('ok');
  });
});

describe('the observation watermark reports the last successful read, not the last attempt', () => {
  it('takes the newest observedAt among successful reads only', () => {
    const unknown = checksObservationFixture({ observationId: 'obs_checks_unknown', unknown: true, checks: undefined, unknownReason: 'gh exited non-zero' });
    expect(lastSuccessfulObservationAt([pullRequestObservationFixture(), unknown])).toBe('2026-10-16T10:00:00Z');
    expect(unknownObservations([pullRequestObservationFixture(), unknown])).toHaveLength(1);
  });

  it('reports no successful read at all when every recorded read failed', () => {
    const failed = pullRequestObservationFixture({ unknown: true, pullRequest: undefined, unknownReason: 'unreadable' });
    expect(lastSuccessfulObservationAt([failed])).toBeUndefined();
  });
});

describe('verification evidence is grouped by whether it gates closure', () => {
  it('separates the synchronous gates from the auxiliary facts', () => {
    const { gates, auxiliary } = verificationGroups(deliveryVerificationFixture());
    expect(gates.map((record) => record.kind)).toEqual([
      'required_tests',
      'independent_review',
      'security_review',
      'post_merge_integration',
    ]);
    expect(auxiliary.map((record) => record.kind)).toEqual(['knowledge_suggestion', 'offline_evaluation']);
  });

  it('never lets a simulated or failed row read as a pass', () => {
    const simulated = verificationResultPresentation(verificationFixture({ source: 'simulated' }));
    expect(simulated.tone).not.toBe('ok');
    const failed = verificationResultPresentation(verificationFixture({ result: 'failed' }));
    expect(failed.tone).toBe('bad');
  });
});

describe('the closure record is re-read item by item, and a missing record is not a closure', () => {
  it('resolves every binding the owner froze when the facts agree', () => {
    const items = closureChecklist(closureRecordFixture(), {
      workItem: workItemFixture({ state: 'CLOSED', version: 4, currentRunId: runDetailFixture().workflowRunId }),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
      facts: [pullRequestObservationFixture(), reviewObservationFixture(), checksObservationFixture()],
      verification: deliveryVerificationFixture(),
    });
    expect(items.every((item) => item.resolved === 'yes'), JSON.stringify(items.filter((item) => item.resolved !== 'yes'))).toBe(true);
  });

  it('marks the merge binding as not re-read when the recorded merge commit differs', () => {
    const items = closureChecklist(closureRecordFixture({ mergeCommitOid: FIXTURE_DELIVERY_COMMIT }), {
      workItem: workItemFixture({ state: 'CLOSED', currentRunId: runDetailFixture().workflowRunId }),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
      facts: [pullRequestObservationFixture(), reviewObservationFixture(), checksObservationFixture()],
      verification: deliveryVerificationFixture(),
    });
    const merge = items.find((item) => item.key === 'merge')!;
    expect(merge.resolved).toBe('no');
  });

  it('marks the review binding as not re-read when the review is on another head', () => {
    const items = closureChecklist(closureRecordFixture(), {
      workItem: workItemFixture({ state: 'CLOSED', currentRunId: runDetailFixture().workflowRunId }),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
      facts: [pullRequestObservationFixture({ pullRequest: { ...pullRequestObservationFixture().pullRequest!, headOid: FIXTURE_MERGE_COMMIT } }), reviewObservationFixture()],
      verification: deliveryVerificationFixture(),
    });
    expect(items.find((item) => item.key === 'review')!.resolved).toBe('no');
  });

  it('reports a missing test verification id as not resolvable rather than assumed passed', () => {
    const items = closureChecklist(closureRecordFixture({ requiredTestVerificationIds: ['ve_missing'] }), {
      workItem: workItemFixture({ state: 'CLOSED', currentRunId: runDetailFixture().workflowRunId }),
      run: runDetailFixture(),
      operations: [externalOperationFixture()],
      facts: [pullRequestObservationFixture(), reviewObservationFixture(), checksObservationFixture()],
      verification: deliveryVerificationFixture(),
    });
    expect(items.find((item) => item.key === 'required-tests')!.resolved).toBe('no');
  });
});

describe('the run lineage keeps the delivery run and its repair runs apart', () => {
  it('returns only repair bindings, sharing no run id', () => {
    const bindings = [
      { workItemId: 'wi', workflowRunId: 'run_1', kind: 'initial' as const, boundByGitHubUserId: '101', boundAt: '2026-10-16T08:00:00Z' },
      {
        workItemId: 'wi',
        workflowRunId: 'run_2',
        kind: 'repair' as const,
        sourceRunId: 'run_1',
        mergedBaselineOid: FIXTURE_MERGE_COMMIT,
        boundByGitHubUserId: '101',
        boundAt: '2026-10-16T11:00:00Z',
      },
    ];
    const repairs = fixRunBindings(bindings, 'run_1');
    expect(repairs.map((binding) => binding.workflowRunId)).toEqual(['run_2']);
    expect(repairs[0]?.mergedBaselineOid).toBe(FIXTURE_MERGE_COMMIT);
  });
});

describe('GitHub links are built only from a validated repository slug', () => {
  it('accepts the GitHub owner/repo form and refuses anything else', () => {
    expect(githubRepositorySlug('team/project')).toBe('team/project');
    expect(githubRepositorySlug('team')).toBeUndefined();
    expect(githubRepositorySlug('https://github.com/team/project')).toBeUndefined();
    expect(githubRepositorySlug('team/pro ject')).toBeUndefined();
    expect(githubRepositorySlug('javascript:alert(1)/x')).toBeUndefined();
    expect(githubRepositorySlug(undefined)).toBeUndefined();
  });

  it('builds https URLs on github.com only', () => {
    expect(pullRequestUrl('team/project', FIXTURE_PR_NUMBER)).toBe(`https://github.com/team/project/pull/${FIXTURE_PR_NUMBER}`);
    expect(commitUrl('team/project', FIXTURE_DELIVERY_COMMIT)).toBe(`https://github.com/team/project/commit/${FIXTURE_DELIVERY_COMMIT}`);
  });
});

describe('the operation state is the owner vocabulary', () => {
  it('never presents UNKNOWN or FAILED as a completed write', () => {
    expect(operationStatePresentation('UNKNOWN').tone).not.toBe('ok');
    expect(operationStatePresentation('UNKNOWN').note).toContain('对账');
    expect(operationStatePresentation('SUCCEEDED').tone).toBe('ok');
    expect(operationStatePresentation('ATTEMPTED').tone).not.toBe('ok');
  });
});

describe('diagnostic text never renders a credential shape', () => {
  it('masks a token-shaped run and says it did, without touching the rest', () => {
    const raw = ['gh auth failed with ', 'ghp', '_', '0123456789abcdefghij'].join('');
    const masked = redactCredentialShapes(raw);
    expect(masked.redacted).toBe(true);
    expect(masked.text).toBe('gh auth failed with [已遮蔽]');
    expect(masked.text).not.toContain('ghp_');
  });

  it('leaves ordinary diagnostic text exactly as the owner recorded it', () => {
    const plain = redactCredentialShapes('gh exited non-zero: 403 from the checks API');
    expect(plain.redacted).toBe(false);
    expect(plain.text).toBe('gh exited non-zero: 403 from the checks API');
  });
});
