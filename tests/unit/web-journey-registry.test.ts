/**
 * The browser-journey delivery registry frontend:S07 publishes for the backend
 * Join stage, checked against itself and against the frozen contracts.
 *
 * The registry is an artifact another lane consumes, so it gets the same
 * treatment the fixtures get: the evidence instance is validated against the
 * registry's own schema with the repository's schema validator, the manifest is
 * held to the shape `scripts/check-stage-contracts.py` requires of a delivery
 * manifest, and every pinned digest is recomputed from the manifest on disk.
 * A stale pin or a schema/instance drift fails here rather than at the join.
 */
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { createContractSchemaValidator } from '../fixtures/jsonSchema';

/** Repository-relative, like every other fixture reader in this suite. */
const EVIDENCE_DIR = join('doc', 'Kowa前端设计', 'record', 'evidence');

function read(relative: string): Record<string, unknown> {
  return JSON.parse(readFileSync(join(EVIDENCE_DIR, relative), 'utf8')) as Record<string, unknown>;
}

function digest(relative: string): string {
  return createHash('sha256').update(readFileSync(join(EVIDENCE_DIR, relative))).digest('hex');
}

const evidencePath = 'S07-delivery-evidence.json';
const schemaPath = 'S07-delivery.schema.json';

describe('the browser-journey delivery registry', () => {
  const manifest = read('S07-delivery.json');
  const schema = read(schemaPath);
  const evidence = read(evidencePath);

  it('is a delivery manifest in the shape the stage contract checker binds', () => {
    // `check-stage-contracts.py` compares the key set exactly, reads `schema`
    // relative to the manifest, recomputes this digest and requires the schema's
    // `title` to equal the contract. All four hold here.
    expect(Object.keys(manifest).sort()).toEqual(['contract', 'owner', 'schema', 'schemaSha256', 'operations'].sort());
    expect(manifest.contract).toBe('kowa.web-journey.v1');
    expect(manifest.owner).toBe('frontend:S07');
    expect(manifest.schema).toBe(schemaPath);
    expect(schema.title).toBe(manifest.contract);
    expect(manifest.schemaSha256).toBe(digest(schemaPath));
  });

  it('registers the journey operation at the level the join stage asks for', () => {
    expect(manifest.operations).toEqual({ 'journey.e2e': 'journey' });
  });

  it('carries an evidence instance that satisfies its own schema', () => {
    const validator = createContractSchemaValidator([{ path: schemaPath, schema }]);
    const result = validator.validate(schemaPath, evidence);
    expect(result.errors).toEqual([]);
    expect(result.valid).toBe(true);
  });

  it('names the released stage, the commit the journeys ran at, and the real target', () => {
    expect(evidence.schemaVersion).toBe('kowa.web-journey.v1');
    expect(evidence.kind).toBe('webJourneyEvidence');
    expect(evidence.stage).toBe('frontend:S07');
    expect(evidence.head).toMatch(/^[a-f0-9]{40}$/);
    const target = evidence.target as Record<string, unknown>;
    expect(target.kind).toBe('realControlPlane');
    expect(String(target.compositionRoot)).toContain('internal/server');
    expect(String(target.script)).toBe('scripts/web-e2e-control-plane.sh');
    // Every substituted external dependency is named, so a reader can see
    // exactly which facts are controlled rather than real.
    expect((target.isolatedExternal as string[]).length).toBeGreaterThanOrEqual(4);
  });

  it('pins each consumed contract to the digest of the manifest on disk', () => {
    const consumed = evidence.consumed as { contract: string; owner: string; operations: string[]; manifest: string; sha256: string }[];
    const contracts = consumed.map((entry) => entry.contract);
    expect(new Set(contracts).size).toBe(contracts.length);
    expect(contracts.sort()).toEqual([
      'kowa.delivery-governance.v1',
      'kowa.fixed-workflow.v1',
      'kowa.identity-workspace.v1',
      'kowa.observability.v1',
      'kowa.runtime-web.v1',
      'kowa.web-application.v1',
      'kowa.web-session.v1',
      'kowa.workitem.v1',
    ]);
    for (const entry of consumed) {
      const manifestPath = entry.manifest;
      const owner = JSON.parse(readFileSync(manifestPath, 'utf8')) as Record<string, unknown>;
      // The owner on disk is the authority for both identity and digest.
      expect(owner.contract, entry.contract).toBe(entry.contract);
      expect(owner.owner, entry.contract).toBe(entry.owner);
      expect(entry.sha256, entry.contract)
        .toBe(createHash('sha256').update(readFileSync(manifestPath)).digest('hex'));
      // Every operation the registry claims is registered by that manifest.
      const registered = Object.keys(owner.operations as Record<string, unknown>);
      for (const operation of entry.operations) {
        expect(registered, `${entry.contract} ${operation}`).toContain(operation);
      }
    }
    expect(consumed.reduce((total, entry) => total + entry.operations.length, 0)).toBe(27);
  });

  it('states its own limits rather than letting the reader assume more', () => {
    const limitations = evidence.limitations as string[];
    expect(limitations.length).toBeGreaterThanOrEqual(5);
    // The Join stage must not read these journeys as real external acceptance.
    expect(limitations.join('\n')).toMatch(/backend:S08/);
    expect(limitations.join('\n')).toMatch(/commit author|committer/);
  });
});
