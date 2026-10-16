/**
 * The two red lines this stage exists to hold:
 *
 * 1. Execution state and business verdict are two facts with two producers and
 *    never collapse into one "done / not done" mark. A Task that completed is
 *    not a verdict; a node that reached a decision is not an approval.
 * 2. A historical round is never presented as the current result, and a node
 *    the run has not materialised yet is never presented as passed.
 *
 * These tests pin the *absent* cases too (no verdict, no round), because the
 * failure this stage guards against is precisely an absent fact being rendered
 * as a favourable one.
 */
import { describe, expect, it } from 'vitest';
import type { NodeHistory } from '../../src/api/contracts';
import {
  attemptKindPresentation,
  attemptStatePresentation,
  currentRound,
  groupRoundsByNode,
  historicalRounds,
  nodeStatePresentation,
  nodeTypePresentation,
  nodeVerdictPresentation,
  runStatePresentation,
  summariseRounds,
} from '../../src/runs/presentation';

const round = (overrides: Partial<NodeHistory> = {}): NodeHistory => ({
  nodeId: 'plan',
  type: 'capability',
  iteration: 1,
  state: 'DECIDED',
  inputDigest: `sha256:${'a'.repeat(64)}`,
  attempts: [],
  ...overrides,
});

/** Wording that would claim a business outcome the server never stated. */
const OUTCOME_CLAIM = /通过|成功|批准|放行|已完成业务|succeeded|approved/i;

describe('execution state never carries the business verdict', () => {
  it('describes a decided node as decided, not as passed', () => {
    const presentation = nodeStatePresentation('DECIDED');
    expect(presentation.label).toContain('DECIDED');
    expect(presentation.label).not.toMatch(OUTCOME_CLAIM);
    expect(presentation.detail).not.toMatch(OUTCOME_CLAIM);
    expect(presentation.detail).toContain('裁决');
  });

  it('keeps skipped distinct instead of folding it into a good state', () => {
    const presentation = nodeStatePresentation('SKIPPED');
    expect(presentation.label).toContain('跳过');
    expect(presentation.label).toContain('SKIPPED');
    expect(presentation.tone).not.toBe('ok');
  });

  it('keeps an execution failure about execution, not about the business', () => {
    const presentation = nodeStatePresentation('EXECUTION_FAILED');
    expect(presentation.label).toContain('EXECUTION_FAILED');
    // A15: "the business did not pass" is not "execution broke" and the reverse.
    expect(presentation.detail).toContain('执行');
  });

  it('never presents a node the run has not materialised as passed', () => {
    const presentation = nodeStatePresentation(undefined);
    expect(presentation.label).toContain('未物化');
    expect(presentation.tone).toBe('unknown');
    expect(presentation.label).not.toMatch(OUTCOME_CLAIM);
  });

  it('states that a completed Task is not a passed verdict', () => {
    const presentation = attemptStatePresentation('COMPLETED');
    expect(presentation.label).toContain('COMPLETED');
    expect(presentation.label).not.toMatch(OUTCOME_CLAIM);
  });
});

describe('the verdict is the server token, quoted rather than interpreted', () => {
  it('quotes the owner token unchanged and adds no tone of its own', () => {
    expect(nodeVerdictPresentation('approved')).toEqual({ label: 'approved' });
    expect(nodeVerdictPresentation('needs_revision')).toEqual({ label: 'needs_revision' });
    // An unrecognised token is still a real answer: it is shown, never dropped
    // and never guessed at.
    expect(nodeVerdictPresentation('weird_token')).toEqual({ label: 'weird_token' });
  });

  it('reports an absent verdict as absent', () => {
    expect(nodeVerdictPresentation(undefined)).toBeUndefined();
    expect(nodeVerdictPresentation('')).toBeUndefined();
  });
});

describe('rounds are grouped by node with the current round told apart', () => {
  const nodes = [
    round({ nodeId: 'plan', iteration: 3, verdict: 'approved' }),
    round({ nodeId: 'plan', iteration: 1, verdict: 'rejected' }),
    round({ nodeId: 'plan', iteration: 2, verdict: 'needs_revision' }),
    round({ nodeId: 'code', iteration: 1, state: 'RUNNING', verdict: undefined }),
  ];

  it('groups rounds per node in iteration order', () => {
    const grouped = groupRoundsByNode(nodes);
    expect([...grouped.keys()].sort()).toEqual(['code', 'plan']);
    expect(grouped.get('plan')?.map((entry) => entry.iteration)).toEqual([1, 2, 3]);
  });

  it('picks the highest iteration as the current round whatever the order', () => {
    expect(currentRound([...nodes].reverse().filter((entry) => entry.nodeId === 'plan'))?.iteration).toBe(3);
    expect(currentRound([])).toBeUndefined();
  });

  it('returns every earlier iteration as a historical round, oldest first', () => {
    const history = historicalRounds(nodes.filter((entry) => entry.nodeId === 'plan'));
    expect(history.map((entry) => entry.iteration)).toEqual([1, 2]);
    expect(history.some((entry) => entry.verdict === 'rejected')).toBe(true);
    // The current round never also appears as history.
    expect(history.some((entry) => entry.iteration === 3)).toBe(false);
  });

  it('has no historical round when a node ran once', () => {
    expect(historicalRounds([round({ iteration: 1 })])).toEqual([]);
  });
});

describe('the run summary counts rounds without deciding anything', () => {
  it('counts every state separately, keeping skipped and failed visible', () => {
    const summary = summariseRounds([
      round({ nodeId: 'a', state: 'DECIDED', verdict: 'approved' }),
      round({ nodeId: 'b', state: 'SKIPPED' }),
      round({ nodeId: 'c', state: 'EXECUTION_FAILED' }),
      round({ nodeId: 'd', state: 'RUNNING' }),
      round({ nodeId: 'e', state: 'WAITING_HUMAN' }),
      round({ nodeId: 'e', iteration: 2, state: 'DECIDED', verdict: 'rejected' }),
    ]);
    expect(summary.materialised).toBe(6);
    expect(summary.skipped).toBe(1);
    expect(summary.executionFailed).toBe(1);
    expect(summary.waitingHuman).toBe(1);
    expect(summary.running).toBe(1);
    expect(summary.decided).toBe(2);
  });

  it('reports an unmaterialised node as a gap, not as a zero outcome', () => {
    const summary = summariseRounds([round({ nodeId: 'a', state: 'DECIDED', verdict: 'approved' })]);
    expect(summary.materialised).toBe(1);
    expect(summary).not.toHaveProperty('passed');
    expect(summary).not.toHaveProperty('failed');
  });
});

describe('the remaining vocabularies stay literal', () => {
  it('names node types without inventing a ranking', () => {
    expect(nodeTypePresentation('capability').label).toBe('能力执行');
    expect(nodeTypePresentation('human_task').label).toBe('人工任务');
    expect(nodeTypePresentation('system').label).toBe('系统操作');
    expect(nodeTypePresentation('join').label).toBe('汇合');
    expect(nodeTypePresentation('end').label).toBe('结束');
  });

  it('keeps attempt kinds and run states literal', () => {
    expect(attemptKindPresentation('retry').label).toContain('重试');
    expect(runStatePresentation('SUCCEEDED').label).toContain('SUCCEEDED');
    expect(runStatePresentation('ACTIVE').label).toContain('ACTIVE');
  });
});
