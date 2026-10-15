/**
 * Test-only binding of fixtures to the frozen owner contracts.
 *
 * The schemas are read from `api/` at test time and validated with the
 * dependency-free draft 2020-12 subset validator in `./jsonSchema.ts`, which
 * reads the frozen artifacts themselves and fails closed on vocabulary it does
 * not implement. This binds every fixture and official example to the same
 * artifacts the owners freeze and pin by digest; it is not part of the runtime
 * bundle.
 */
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { createContractSchemaValidator } from './jsonSchema';

export type ContractName =
  | 'web-session'
  | 'identity-workspace'
  | 'runtime-web'
  | 'delivery-governance'
  | 'workitem'
  | 'web-application';

const CONTRACT_DIRS: Record<ContractName, string> = {
  'web-session': 'api/web-session/v1',
  'identity-workspace': 'api/identity-workspace/v1',
  'runtime-web': 'api/runtime-web/v1',
  'delivery-governance': 'api/delivery-governance/v1',
  workitem: 'api/workitem/v1',
  'web-application': 'api/web-application/v1',
};

function schemaPath(name: ContractName): string {
  return `${CONTRACT_DIRS[name]}/schema.json`;
}

function readJson(path: string): { [key: string]: unknown } {
  return JSON.parse(readFileSync(path, 'utf8')) as { [key: string]: unknown };
}

export function createValidator() {
  const validator = createContractSchemaValidator(
    (Object.keys(CONTRACT_DIRS) as ContractName[]).map((name) => ({
      path: schemaPath(name),
      schema: readJson(schemaPath(name)),
    })),
  );
  return {
    validate(name: ContractName, value: unknown): { valid: boolean; errors: string } {
      const result = validator.validate(schemaPath(name), value);
      return { valid: result.valid, errors: result.errors.join('\n') };
    },
  };
}

export function readExamples(name: ContractName): { file: string; value: unknown }[] {
  const directory = join(CONTRACT_DIRS[name], 'examples');
  return readdirSync(directory)
    .filter((file) => file.endsWith('.json'))
    .sort()
    .map((file) => ({ file, value: readJson(join(directory, file)) }));
}
