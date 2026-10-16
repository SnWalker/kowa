/**
 * "No state is carried by colour alone" as a machine-checked invariant.
 *
 * Every status the pages paint is a `Badge` whose tone is a colour. Colour is
 * decoration; the semantics have to survive as text, for a screen reader, for a
 * monochrome screen and for anyone who cannot tell the four tones apart. That
 * only holds if, for each state space the application renders:
 *
 *  - every state maps to a non-empty label, and
 *  - the mapping is injective — two different states can never read the same,
 *
 * so no reader has to fall back on the tint to tell two facts apart. A state
 * space that gained a new member without a label, or that reused an existing
 * label, fails here rather than shipping as an ambiguous colour.
 *
 * The module also pins the deliberate non-collapses: the states the product
 * exists to keep apart (unknown vs stale, execution failure vs business
 * rejection, open vs closed) must stay distinct labels *and* distinct details.
 */
import { describe, expect, it } from 'vitest';
import {
  ARTIFACT_STATES,
  ATTEMPT_STATES,
  HUMAN_TASK_STATES,
  KNOWLEDGE_RECOMMENDATION_STATES,
  MACHINE_IDENTITY_STATES,
  NODE_ROUND_STATES,
  NODE_TYPES,
  PUSH_PERMISSION_STATES,
  REMOTE_READ_STATES,
  RUN_STATES,
  RUNTIME_FRESHNESS,
  WORK_ITEM_STATES,
  type ArtifactState,
  type AttemptState,
  type HumanTaskState,
  type KnowledgeRecommendationState,
  type NodeRoundState,
  type PushPermissionState,
  type RunState,
  type WorkItemState,
} from '../../src/api/contracts';
import {
  artifactStatePresentation,
  attemptStatePresentation,
  attemptKindPresentation,
  nodeStatePresentation,
  nodeTypePresentation,
  runStatePresentation as runRoundStatePresentation,
} from '../../src/runs/presentation';
import {
  freshnessPresentation,
  machineIdentityPresentation,
  pushPermissionPresentation,
  remoteReadPresentation,
} from '../../src/runtimes/presentation';
import {
  humanTaskStatePresentation,
  recommendationStatePresentation,
  runStatePresentation as workItemRunStatePresentation,
  workItemStatePresentation,
} from '../../src/workitems/presentation';

type Labelled = { label: string; detail?: string };

/** Asserts the two properties every state space must hold, and reports the offenders. */
function expectDistinctLabels<T>(name: string, entries: readonly (readonly [T, Labelled])[]): void {
  const empty = entries.filter(([, value]) => value.label.trim() === '').map(([state]) => String(state));
  expect(empty, `${name}: states without any label`).toEqual([]);

  const byLabel = new Map<string, string[]>();
  for (const [state, value] of entries) {
    byLabel.set(value.label, [...(byLabel.get(value.label) ?? []), String(state)]);
  }
  const collided = [...byLabel.entries()]
    .filter(([, states]) => states.length > 1)
    .map(([label, states]) => `${label} ← ${states.join(', ')}`);
  expect(collided, `${name}: states that read the same`).toEqual([]);
}

/** Asserts a state space's details are distinct too, where each state carries one. */
function expectDistinctDetails<T>(name: string, entries: readonly (readonly [T, Labelled])[]): void {
  const missing = entries.filter(([, value]) => (value.detail ?? '').trim() === '').map(([state]) => String(state));
  expect(missing, `${name}: states without any explanatory text`).toEqual([]);
}

describe('run states', () => {
  const entries = RUN_STATES.map((state) => [state, runRoundStatePresentation(state)] as const);

  it('gives every run state its own text, and says what the state does not mean', () => {
    expectDistinctLabels('run state', entries);
    expectDistinctDetails('run state', entries);
    expect(RUN_STATES.length).toBe(entries.length);
  });

  it('keeps execution success apart from the work item closure', () => {
    // A SUCCEEDED run is an execution fact; only the delivery-governance close
    // transaction closes the work item. The words must not merge them.
    expect(runRoundStatePresentation('SUCCEEDED').detail).toMatch(/工作项关闭是另一项/);
  });

  it('never paints an in-flight run as an outcome', () => {
    expect(runRoundStatePresentation('ACTIVE').tone).not.toBe('ok');
    expect(runRoundStatePresentation('ACTIVE').label).toMatch(/进行中/);
  });

  it('reads a state the browser does not know as itself, not as a favourable default', () => {
    // The contract enumerates the states, but the projection is quoted verbatim:
    // an unrecognised token must still be distinguishable from every known one.
    const unknown = workItemRunStatePresentation('SOMETHING_NEW' as RunState);
    expectDistinctLabels('unknown run state', [
      ...RUN_STATES.map((state) => [state, workItemRunStatePresentation(state)] as const),
      ['SOMETHING_NEW' as RunState, unknown],
    ]);
  });
});

describe('node states, types and attempts', () => {
  it('gives every node round state its own text', () => {
    const entries = NODE_ROUND_STATES.map((state) => [state, nodeStatePresentation(state)] as const);
    expectDistinctLabels('node round state', entries);
    expectDistinctDetails('node round state', entries);
  });

  it('tells "not materialised yet" apart from every materialised state', () => {
    const absent = nodeStatePresentation(undefined);
    expectDistinctLabels('materialised vs not', [
      ...NODE_ROUND_STATES.map((state) => [state, nodeStatePresentation(state)] as const),
      [undefined as NodeRoundState | undefined, absent],
    ]);
    expect(absent.detail).toMatch(/未物化/);
  });

  it('never paints a node execution state as a business outcome', () => {
    for (const state of NODE_ROUND_STATES) {
      expect(nodeStatePresentation(state).tone, `${state} must not read as favourable`).not.toBe('ok');
    }
  });

  it('gives every node type its own text', () => {
    const entries = NODE_TYPES.map((type) => [type, nodeTypePresentation(type)] as const);
    expectDistinctLabels('node type', entries);
    expectDistinctDetails('node type', entries);
  });

  it('gives every attempt state its own text', () => {
    const entries = ATTEMPT_STATES.map((state) => [state, attemptStatePresentation(state)] as const);
    expectDistinctLabels('attempt state', entries);
    expectDistinctDetails('attempt state', entries);
    // A completed Task is an execution fact, never a business pass.
    expect(attemptStatePresentation('COMPLETED').detail).toMatch(/不说明业务裁决/);
  });

  it('names every attempt kind in words', () => {
    for (const kind of ['initial', 'retry', 'resume'] as const) {
      expect(attemptKindPresentation(kind).label.trim()).not.toBe('');
    }
    expect(attemptKindPresentation('retry').label).not.toBe(attemptKindPresentation('resume').label);
  });
});

describe('work item, human task, knowledge and artifact states', () => {
  it('gives every work item state its own text', () => {
    const entries = WORK_ITEM_STATES.map((state) => [state, workItemStatePresentation(state, true)] as const);
    expectDistinctLabels('work item state', entries);
    expectDistinctDetails('work item state', entries);
  });

  it('states whether a run is bound instead of letting the tint imply progress', () => {
    expect(workItemStatePresentation('OPEN' as WorkItemState, true).detail).toMatch(/已关联/);
    expect(workItemStatePresentation('OPEN' as WorkItemState, false).detail).toMatch(/尚未关联/);
  });

  it('gives every human task state its own text', () => {
    const entries = HUMAN_TASK_STATES.map((state) => [state, humanTaskStatePresentation(state)] as const);
    expectDistinctLabels('human task state', entries);
  });

  it('gives every knowledge recommendation state its own text', () => {
    const entries = KNOWLEDGE_RECOMMENDATION_STATES.map(
      (state) => [state, recommendationStatePresentation(state)] as const,
    );
    expectDistinctLabels('knowledge state', entries);
    expectDistinctDetails('knowledge state', entries);
    expect(recommendationStatePresentation('RECOMMENDED' as KnowledgeRecommendationState).detail).toMatch(/不会派发/);
  });

  it('gives every artifact state its own text', () => {
    const entries = ARTIFACT_STATES.map((state) => [state, artifactStatePresentation(state)] as const);
    expectDistinctLabels('artifact state', entries);
    expectDistinctDetails('artifact state', entries);
  });
});

describe('runtime and machine-identity states', () => {
  it('gives every freshness its own text and never lets stale read as fresh', () => {
    const entries = RUNTIME_FRESHNESS.map((freshness) => [freshness, freshnessPresentation(freshness)] as const);
    expectDistinctLabels('freshness', entries);
    expectDistinctDetails('freshness', entries);
    expect(freshnessPresentation('stale').tone).not.toBe('ok');
    expect(freshnessPresentation('unknown').tone).not.toBe('ok');
    expect(freshnessPresentation('unknown').detail).toMatch(/未知不是可用/);
  });

  it('tells the two staleness reasons apart', () => {
    expect(freshnessPresentation('stale', 'superseded-epoch').label)
      .not.toBe(freshnessPresentation('stale', 'expired').label);
  });

  it('gives every machine identity state its own text', () => {
    const entries = MACHINE_IDENTITY_STATES.map(
      (state) => [state, machineIdentityPresentation(state, ['135722937'], '135722937')] as const,
    );
    expectDistinctLabels('machine identity', entries);
    expect(machineIdentityPresentation('drifted', ['135722937'], '42').tone).not.toBe('ok');
  });

  it('gives every remote read state its own text', () => {
    // The explanation of *why* a probe failed is the contract's own
    // `remoteRead.reason`, rendered next to this badge; the badge therefore
    // carries the verdict alone and must stay unambiguous on its own.
    const entries = REMOTE_READ_STATES.map((state) => [state, remoteReadPresentation(state)] as const);
    expectDistinctLabels('remote read', entries);
  });

  it('gives every push permission state its own text', () => {
    const entries = PUSH_PERMISSION_STATES.map((state) => [state, pushPermissionPresentation(state)] as const);
    expectDistinctLabels('push permission', entries);
    // A GitHub-reported permission is a report, not a write test.
    expect(pushPermissionPresentation('granted' as PushPermissionState).label).toMatch(/不是写测试/);
  });
});

describe('the machine token survives the rendering', () => {
  /**
   * A state the owner enumerates in UPPERCASE is a machine fact. Prose that
   * replaced the token would force a reader to trust the browser's paraphrase;
   * the label therefore quotes the token verbatim as well as naming the state
   * in words. (Lowercase token families — freshness, identity, probing — are
   * named in words only, and are covered by the uniqueness rules above.)
   */
  const quoted: readonly (readonly [string, readonly string[], (state: string) => Labelled])[] = [
    ['run', RUN_STATES, (state) => runRoundStatePresentation(state as RunState)],
    ['node round', NODE_ROUND_STATES, (state) => nodeStatePresentation(state as NodeRoundState)],
    ['attempt', ATTEMPT_STATES, (state) => attemptStatePresentation(state as AttemptState)],
    ['artifact', ARTIFACT_STATES, (state) => artifactStatePresentation(state as ArtifactState)],
    ['human task', HUMAN_TASK_STATES, (state) => humanTaskStatePresentation(state as HumanTaskState)],
    ['work item', WORK_ITEM_STATES, (state) => workItemStatePresentation(state as WorkItemState, true)],
    ['knowledge', KNOWLEDGE_RECOMMENDATION_STATES, (state) => recommendationStatePresentation(state as KnowledgeRecommendationState)],
  ];

  for (const [name, states, present] of quoted) {
    it(`quotes the frozen ${name} token verbatim in its label`, () => {
      const unquoted = states.filter((state) => !present(state).label.includes(state));
      expect(unquoted, `${name}: labels that dropped the machine token`).toEqual([]);
    });
  }

  it('uses more than one tone, so a single tint is never the whole vocabulary', () => {
    const tones = new Set(RUN_STATES.map((state) => runRoundStatePresentation(state).tone));
    expect(tones.size).toBeGreaterThan(1);
  });
});
