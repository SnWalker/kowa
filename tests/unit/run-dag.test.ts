/**
 * The DAG layout is presentation geometry, not business semantics: it may only
 * derive layers and order from the server's frozen `dependsOn` edges and
 * `order`. A graph it cannot place exactly is refused instead of drawn
 * approximately, because an approximated graph would misstate the frozen
 * topology the operator is reading.
 */
import { describe, expect, it } from 'vitest';
import { DagLayoutError, layoutDag } from '../../src/runs/dag';

const node = (id: string, dependsOn: string[] = []) => ({ id, dependsOn });

describe('DAG layout', () => {
  it('places a single node in one layer with no edges', () => {
    const layout = layoutDag([node('only')], ['only']);
    expect(layout.layers).toEqual([['only']]);
    expect(layout.edges).toEqual([]);
    expect(layout.layerCount).toBe(1);
    expect(layout.positions.only).toEqual({ layer: 0, row: 0 });
  });

  it('separates a fan-out into consecutive layers', () => {
    const layout = layoutDag(
      [node('root'), node('left', ['root']), node('right', ['root'])],
      ['root', 'left', 'right'],
    );
    expect(layout.layers).toEqual([['root'], ['left', 'right']]);
    expect(layout.positions.left!.layer).toBe(1);
    expect(layout.positions.right!.layer).toBe(1);
    expect(layout.positions.left!.row).toBe(0);
    expect(layout.positions.right!.row).toBe(1);
    expect(layout.edges).toEqual([
      { from: 'root', to: 'left' },
      { from: 'root', to: 'right' },
    ]);
  });

  it('drops a fan-in node one layer below its deepest ancestor', () => {
    // a -> b -> d and a -> c -> d: the join must sit below b and c, never
    // beside them, or the drawing would imply d can start before c decided.
    const layout = layoutDag(
      [node('a'), node('b', ['a']), node('c', ['a', 'b']), node('d', ['b', 'c'])],
      ['a', 'b', 'c', 'd'],
    );
    expect(layout.layers).toEqual([['a'], ['b'], ['c'], ['d']]);
  });

  it('breaks ties by the server order and stays stable for equal ranks', () => {
    // Two independent roots and two orphans of the second layer: the server's
    // order decides the sequence, so the same projection always draws the same.
    const layout = layoutDag(
      [node('z'), node('a'), node('m', ['z']), node('b', ['a'])],
      ['z', 'a', 'm', 'b'],
    );
    expect(layout.layers).toEqual([['z', 'a'], ['m', 'b']]);
    const again = layoutDag(
      [node('z'), node('a'), node('m', ['z']), node('b', ['a'])],
      ['z', 'a', 'm', 'b'],
    );
    expect(again.layers).toEqual(layout.layers);
  });

  it('orders a layer by the server order, not by input order', () => {
    const layout = layoutDag([node('second'), node('first')], ['first', 'second']);
    expect(layout.layers).toEqual([['first', 'second']]);
  });

  it('never places an edge inside a layer or backwards', () => {
    const layout = layoutDag(
      [node('a'), node('b', ['a']), node('c', ['a']), node('j', ['b', 'c'])],
      ['a', 'b', 'c', 'j'],
    );
    for (const edge of layout.edges) {
      expect(layout.positions[edge.to]!.layer).toBeGreaterThan(layout.positions[edge.from]!.layer);
    }
    expect(layout.edges).toEqual([
      { from: 'a', to: 'b' },
      { from: 'a', to: 'c' },
      { from: 'b', to: 'j' },
      { from: 'c', to: 'j' },
    ]);
  });

  it('refuses an edge to a node the projection does not carry', () => {
    expect(() => layoutDag([node('a', ['ghost'])], ['a'])).toThrow(DagLayoutError);
  });

  it('refuses a duplicate node id', () => {
    expect(() => layoutDag([node('a'), node('a')], ['a', 'a'])).toThrow(DagLayoutError);
  });

  it('refuses a cyclic projection instead of inventing an order', () => {
    expect(() => layoutDag([node('a', ['b']), node('b', ['a'])], ['a', 'b'])).toThrow(DagLayoutError);
  });

  it('refuses a node missing from the server order', () => {
    expect(() => layoutDag([node('a'), node('b')], ['a'])).toThrow(DagLayoutError);
  });
});
