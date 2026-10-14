/**
 * Test-only JSON Schema loading for the frozen owner contracts.
 *
 * The schemas are read from `api/` at test time and validated with Ajv (draft
 * 2020-12). Cross-file `$ref`s are rewritten to the referenced schema's own
 * `$id`, which is how the identity contract is registered. This binds every
 * fixture to the same artifact the owners freeze and pin by digest; it is not
 * part of the runtime bundle.
 */
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import Ajv2020 from 'ajv/dist/2020';
import type { Options } from 'ajv';

export type ContractName = 'web-session' | 'identity-workspace' | 'runtime-web' | 'delivery-governance';

const CONTRACT_DIRS: Record<ContractName, string> = {
  'web-session': 'api/web-session/v1',
  'identity-workspace': 'api/identity-workspace/v1',
  'runtime-web': 'api/runtime-web/v1',
  'delivery-governance': 'api/delivery-governance/v1',
};

function readJson(path: string): { [key: string]: unknown } {
  return JSON.parse(readFileSync(path, 'utf8')) as { [key: string]: unknown };
}

export function createValidator() {
  const ajv = new Ajv2020({ allErrors: true, strict: false } as Options);
  const ids: Partial<Record<ContractName, string>> = {};
  for (const [name, directory] of Object.entries(CONTRACT_DIRS) as [ContractName, string][]) {
    const schema = readJson(join(directory, 'schema.json'));
    ids[name] = String(schema['$id']);
  }
  for (const [name, directory] of Object.entries(CONTRACT_DIRS) as [ContractName, string][]) {
    const raw = readFileSync(join(directory, 'schema.json'), 'utf8');
    const rewritten = raw.replace(
      /"\.\.\/\.\.\/identity-workspace\/v1\/schema\.json#/g,
      `"${ids['identity-workspace']}#`,
    );
    ajv.addSchema(JSON.parse(rewritten) as object, ids[name]);
  }
  return {
    validate(name: ContractName, value: unknown): { valid: boolean; errors: string } {
      const validate = ajv.getSchema(ids[name] as string);
      if (!validate) throw new Error(`schema ${name} is not registered`);
      const valid = validate(value) === true;
      return { valid, errors: JSON.stringify(validate.errors ?? [], null, 2) };
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
