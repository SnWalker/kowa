/**
 * Guards the test-only schema binding machinery itself.
 *
 * The binding is only as strong as this validator: it must fail closed on
 * vocabulary it does not implement, and each implemented keyword must actually
 * discriminate. The four frozen owner schemas must load unchanged, so a new
 * keyword in an owner schema stops the build instead of silently weakening the
 * fixture binding.
 */
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { createContractSchemaValidator, type ContractSchemaDocument } from '../fixtures/jsonSchema';

const FROZEN_SCHEMAS: ContractSchemaDocument[] = [
  'api/web-session/v1/schema.json',
  'api/identity-workspace/v1/schema.json',
  'api/runtime-web/v1/schema.json',
  'api/delivery-governance/v1/schema.json',
].map((path) => ({
  path,
  schema: JSON.parse(readFileSync(path, 'utf8')) as Record<string, unknown>,
}));

function check(schema: Record<string, unknown>, value: unknown): boolean {
  const validator = createContractSchemaValidator([{ path: 'api/example/v1/schema.json', schema }]);
  return validator.validate('api/example/v1/schema.json', value).valid;
}

describe('frozen owner schemas stay inside the supported vocabulary', () => {
  it('loads all four frozen schemas without unsupported keywords', () => {
    expect(() => createContractSchemaValidator(FROZEN_SCHEMAS)).not.toThrow();
  });
});

describe('the validator fails closed', () => {
  it('rejects vocabulary it does not implement', () => {
    expect(() => check({ type: 'object', dependentRequired: {} }, {})).toThrow(/unsupported schema keyword/);
  });

  it('rejects vocabulary it does not implement even when unused', () => {
    expect(() => check({ $defs: { unused: { patternProperties: {} } }, type: 'object' }, {})).toThrow(
      /unsupported schema keyword "patternProperties"/,
    );
  });

  it('rejects an unregistered document path', () => {
    const validator = createContractSchemaValidator([
      { path: 'api/example/v1/schema.json', schema: { type: 'object' } },
    ]);
    expect(() => validator.validate('api/missing/v1/schema.json', {})).toThrow(/not registered/);
  });

  it('rejects an unresolvable $ref', () => {
    expect(() => check({ $ref: '#/$defs/missing' }, {})).toThrow(/does not resolve/);
  });
});

describe('each implemented keyword discriminates', () => {
  const cases: [string, Record<string, unknown>, unknown, boolean][] = [
    ['type accepts matching value', { type: 'string' }, 'x', true],
    ['type rejects other value', { type: 'string' }, 1, false],
    ['integer rejects fractions', { type: 'integer' }, 1.5, false],
    ['const accepts equal value', { const: 'fresh' }, 'fresh', true],
    ['const rejects other value', { const: 'fresh' }, 'stale', false],
    ['enum accepts member', { enum: ['fresh', 'stale'] }, 'stale', true],
    ['enum rejects non-member', { enum: ['fresh', 'stale'] }, 'healthy', false],
    [
      'required rejects a missing property',
      { type: 'object', required: ['freshness'], properties: { freshness: { type: 'string' } } },
      {},
      false,
    ],
    [
      'additionalProperties false rejects an unknown property',
      { type: 'object', properties: { freshness: { type: 'string' } }, additionalProperties: false },
      { freshness: 'fresh', runtimeToken: 'x' },
      false,
    ],
    ['pattern is a search, not a full match', { type: 'string', pattern: '^a' }, 'abc', true],
    ['pattern rejects a mismatch', { type: 'string', pattern: '^a' }, 'ba', false],
    ['minLength counts code points', { type: 'string', minLength: 1, maxLength: 1 }, '😀', true],
    ['minLength rejects shorter strings', { type: 'string', minLength: 2 }, 'a', false],
    ['maxLength rejects longer strings', { type: 'string', maxLength: 2 }, 'abc', false],
    ['minItems rejects shorter arrays', { type: 'array', minItems: 1 }, [], false],
    ['maxItems rejects longer arrays', { type: 'array', maxItems: 1 }, [1, 2], false],
    ['uniqueItems rejects deep duplicates', { type: 'array', uniqueItems: true }, [{ a: 1 }, { a: 1 }], false],
    ['uniqueItems ignores key order', { type: 'array', uniqueItems: true }, [{ a: 1, b: 2 }, { b: 2, a: 1 }], false],
    ['minimum rejects a smaller number', { type: 'number', minimum: 1 }, 0, false],
    ['maximum rejects a larger number', { type: 'number', maximum: 65535 }, 65536, false],
    ['allOf requires every branch', { allOf: [{ type: 'string' }, { minLength: 2 }] }, 'a', false],
    ['oneOf rejects zero matches', { oneOf: [{ type: 'string' }, { type: 'number' }] }, true, false],
    ['oneOf rejects several matches', { oneOf: [{ type: 'string' }, { minLength: 1 }] }, 'a', false],
    ['not rejects a matching value', { not: { required: ['staleReason'] } }, { staleReason: 'expired' }, false],
    [
      'if/then applies the branch when the condition holds',
      {
        type: 'object',
        if: { properties: { role: { const: 'knowledge' } }, required: ['role'] },
        then: { properties: { access: { const: 'read' } } },
      },
      { role: 'knowledge', access: 'read_write' },
      false,
    ],
    [
      'if/then stays silent when the condition fails',
      {
        type: 'object',
        if: { properties: { role: { const: 'knowledge' } }, required: ['role'] },
        then: { properties: { access: { const: 'read' } } },
      },
      { role: 'project', access: 'read_write' },
      true,
    ],
    [
      'type-gated keywords ignore non-matching instances',
      { required: ['freshness'], pattern: '^a', minItems: 1 },
      42,
      true,
    ],
  ];

  it.each(cases)('%s', (_label, schema, value, expected) => {
    expect(check(schema, value)).toBe(expected);
  });

  it('resolves $refs across documents like the frozen schemas do', () => {
    const validator = createContractSchemaValidator([
      {
        path: 'api/example/v1/schema.json',
        schema: { $ref: '../../other/v1/schema.json#/$defs/WebIdentity' },
      },
      {
        path: 'api/other/v1/schema.json',
        schema: {
          $defs: {
            WebIdentity: {
              type: 'object',
              required: ['githubUserId', 'login'],
              properties: {
                githubUserId: { type: 'string', pattern: '^[1-9][0-9]*$' },
                login: { type: 'string', minLength: 1 },
              },
              additionalProperties: false,
            },
          },
        },
      },
    ]);
    expect(validator.validate('api/example/v1/schema.json', { githubUserId: '101', login: 'a' }).valid).toBe(true);
    expect(validator.validate('api/example/v1/schema.json', { GitHubUserID: '101' }).valid).toBe(false);
  });
});
