import { describe, expect, it } from 'vitest';
import {
  decodeApiErrorCategory,
  decodeExecutionConfigResponse,
  decodeGitRemoteStatusResponse,
  decodeLoginResponse,
  decodeRuntimeListResponse,
  decodeRuntimeViewResponse,
  decodeSessionBootstrapResponse,
  decodeWorkspaceConfig,
} from '../../src/api/decode';
import { API_ERROR_CATEGORIES } from '../../src/api/contracts';
import {
  executionConfigFixture,
  gitRemoteStatusFixture,
  runtimeListFixture,
  runtimeViewFixture,
  sessionBootstrapFixture,
  workspaceConfigFixture,
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
  const contracts: ContractName[] = ['web-session', 'identity-workspace', 'runtime-web', 'delivery-governance'];

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

  it('the error envelope fixture validates for every frozen category', () => {
    for (const category of API_ERROR_CATEGORIES) {
      const result = validator.validate('identity-workspace', { error: category });
      expect(result.valid, `${category}: ${result.errors}`).toBe(true);
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

  it('decodes the login response envelope', () => {
    expect(decodeLoginResponse({ authorizationUrl: 'https://github.com/login/oauth/authorize?client_id=x' }))
      .toEqual({ authorizationUrl: 'https://github.com/login/oauth/authorize?client_id=x' });
  });

  it('decodes every frozen error category', () => {
    for (const category of API_ERROR_CATEGORIES) {
      expect(decodeApiErrorCategory({ error: category })).toBe(category);
    }
  });
});

describe('decoders reject the official negative cases', () => {
  it('rejects schema-negative and semantic payloads', () => {
    const negatives = [
      ...readExamples('web-session').filter((example) => example.file.startsWith('negative-schema-')),
      ...readExamples('runtime-web').filter((example) => example.file.startsWith('negative-')),
      ...readExamples('delivery-governance').filter((example) => example.file.startsWith('negative-schema-')),
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
});
