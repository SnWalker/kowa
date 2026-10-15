/**
 * Test-only JSON Schema (draft 2020-12) subset validator.
 *
 * Fixtures and the owners' official examples are bound to the frozen contract
 * schemas under `api/` by validating them against the schema documents
 * themselves, so the binding follows the frozen artifacts instead of a
 * hand-copied field list.
 *
 * The supported keyword set is exactly the vocabulary present in those frozen
 * schemas. Loading a document aborts on any keyword outside that set instead of
 * ignoring it, so an owner schema that adopts new vocabulary fails the tests
 * loudly rather than silently weakening the binding. `format` is annotation
 * only: the frozen schemas use `format: "date-time"` and the reference
 * validator configuration used for this binding (draft 2020-12 without
 * registered format assertions) did not assert it either.
 *
 * Type-specific keywords apply only to instances of the matching type, as the
 * specification requires: `required` on a non-object passes, `pattern` on a
 * non-string passes, and so on.
 */

export interface ContractSchemaDocument {
  /** Repository-relative path, e.g. `api/web-session/v1/schema.json`. */
  path: string;
  schema: Record<string, unknown>;
}

export interface SchemaValidation {
  valid: boolean;
  errors: string[];
}

export interface ContractSchemaValidator {
  /**
   * Validate `value` against the root schema of the registered document at
   * `path`. Unknown documents and unsupported vocabulary throw; a returned
   * `valid: false` always carries human-readable instance paths.
   */
  validate(path: string, value: unknown): SchemaValidation;
}

const SUPPORTED_KEYWORDS = new Set([
  // Annotations: no assertion, kept so documents that carry them still load.
  '$id',
  '$schema',
  '$comment',
  'title',
  'description',
  'default',
  'examples',
  'deprecated',
  'readOnly',
  'writeOnly',
  // References and definitions.
  '$ref',
  '$defs',
  // Assertions.
  'type',
  'enum',
  'const',
  'properties',
  'required',
  'additionalProperties',
  'items',
  'minItems',
  'maxItems',
  'uniqueItems',
  'minLength',
  'maxLength',
  'pattern',
  'format',
  'minimum',
  'maximum',
  'allOf',
  'oneOf',
  'not',
  'if',
  'then',
  'else',
]);

export function createContractSchemaValidator(
  documents: ContractSchemaDocument[],
): ContractSchemaValidator {
  const registry = new Map<string, ContractSchemaDocument>();
  for (const document of documents) {
    assertSupportedVocabulary(document);
    registry.set(normalizePath(document.path), document);
  }

  return {
    validate(path: string, value: unknown): SchemaValidation {
      const document = registry.get(normalizePath(path));
      if (!document) throw new Error(`schema document is not registered: ${path}`);
      const errors: string[] = [];
      validateNode(document, document.schema, value, '$', errors, registry);
      return { valid: errors.length === 0, errors };
    },
  };
}

/* ------------------------------------------------------------------ loading */

/** Rejects documents whose vocabulary this validator does not implement. */
function assertSupportedVocabulary(document: ContractSchemaDocument): void {
  const seen = new Set<object>();
  const queue: Record<string, unknown>[] = [document.schema];
  while (queue.length > 0) {
    const node = queue.pop();
    if (!node || seen.has(node)) continue;
    seen.add(node);
    for (const keyword of Object.keys(node)) {
      if (!SUPPORTED_KEYWORDS.has(keyword)) {
        throw new Error(
          `${document.path}: unsupported schema keyword "${keyword}"; ` +
            'extend tests/fixtures/jsonSchema.ts and re-admit the binding',
        );
      }
    }
    assertSubSchemaShapes(document, node);
    queue.push(...subschemasOf(node));
  }
}

/**
 * A known keyword can still carry a variant this validator does not implement
 * (boolean sub-schemas, the tuple form of `items`). Those must abort at load
 * time as well: silently skipping them would let a future owner schema weaken
 * the binding while the "the frozen schemas must load" guard stayed green.
 */
function assertSubSchemaShapes(document: ContractSchemaDocument, node: Record<string, unknown>): void {
  for (const keyword of ['properties', '$defs'] as const) {
    const map = node[keyword];
    if (map === undefined) continue;
    if (!isObject(map)) throw new Error(`${document.path}: unsupported "${keyword}" shape`);
    for (const name of Object.keys(map)) {
      if (!isObject(map[name])) {
        throw new Error(
          `${document.path}: ${keyword}.${name} must be a sub-schema object; boolean sub-schemas are not implemented`,
        );
      }
    }
  }
  const items = node['items'];
  if (items !== undefined && !isObject(items)) {
    throw new Error(`${document.path}: unsupported "items" shape (the tuple form is not implemented)`);
  }
  const additional = node['additionalProperties'];
  if (additional !== undefined && typeof additional !== 'boolean' && !isObject(additional)) {
    throw new Error(`${document.path}: unsupported "additionalProperties" shape`);
  }
}

function subschemasOf(node: Record<string, unknown>): Record<string, unknown>[] {
  const found: Record<string, unknown>[] = [];
  const add = (candidate: unknown): void => {
    if (isObject(candidate)) found.push(candidate);
  };
  const addList = (candidate: unknown): void => {
    if (Array.isArray(candidate)) for (const item of candidate) add(item);
  };
  const definitions = node['$defs'];
  if (isObject(definitions)) for (const key of Object.keys(definitions)) add(definitions[key]);
  const properties = node['properties'];
  if (isObject(properties)) for (const key of Object.keys(properties)) add(properties[key]);
  add(node['items']);
  add(node['additionalProperties']);
  add(node['not']);
  add(node['if']);
  add(node['then']);
  add(node['else']);
  addList(node['allOf']);
  addList(node['oneOf']);
  return found;
}

/* ---------------------------------------------------------------- validation */

type Registry = Map<string, ContractSchemaDocument>;

function validateNode(
  document: ContractSchemaDocument,
  schema: Record<string, unknown>,
  value: unknown,
  path: string,
  errors: string[],
  registry: Registry,
): void {
  const reference = schema['$ref'];
  if (reference !== undefined) {
    if (typeof reference !== 'string') throw new Error(`${document.path}: $ref must be a string`);
    const target = resolveReference(registry, document, reference);
    validateNode(target.document, target.schema, value, path, errors, registry);
  }

  if ('const' in schema && !deepEqual(value, schema['const'])) {
    errors.push(`${path}: ${describe(value)} is not the required constant ${describe(schema['const'])}`);
  }

  const allowed = schema['enum'];
  if (Array.isArray(allowed) && !allowed.some((candidate) => deepEqual(candidate, value))) {
    errors.push(`${path}: ${describe(value)} is not one of ${JSON.stringify(allowed)}`);
  }

  const type = schema['type'];
  if (type !== undefined) {
    const candidates = Array.isArray(type) ? type : [type];
    const matches = candidates.some(
      (candidate) => typeof candidate === 'string' && typeMatches(candidate, value),
    );
    if (!matches) {
      errors.push(`${path}: expected type ${describe(type)}, got ${describeType(value)}`);
    }
  }

  if (isObject(value)) validateObjectKeywords(document, schema, value, path, errors, registry);
  if (Array.isArray(value)) validateArrayKeywords(document, schema, value, path, errors, registry);
  if (typeof value === 'string') validateStringKeywords(schema, value, path, errors);
  if (typeof value === 'number') validateNumberKeywords(schema, value, path, errors);

  const allOf = schema['allOf'];
  if (Array.isArray(allOf)) {
    for (const subschema of allOf) {
      if (isObject(subschema)) validateNode(document, subschema, value, path, errors, registry);
    }
  }

  const oneOf = schema['oneOf'];
  if (Array.isArray(oneOf)) {
    const matches = oneOf.filter(
      (subschema) => isObject(subschema) && passes(document, subschema, value, registry),
    ).length;
    if (matches !== 1) {
      errors.push(`${path}: expected exactly one "oneOf" branch to match, matched ${matches}`);
    }
  }

  const notSchema = schema['not'];
  if (isObject(notSchema) && passes(document, notSchema, value, registry)) {
    errors.push(`${path}: value must not match a "not" clause but does`);
  }

  const condition = schema['if'];
  if (isObject(condition)) {
    const branch = passes(document, condition, value, registry) ? schema['then'] : schema['else'];
    if (isObject(branch)) validateNode(document, branch, value, path, errors, registry);
  }
}

function validateObjectKeywords(
  document: ContractSchemaDocument,
  schema: Record<string, unknown>,
  value: Record<string, unknown>,
  path: string,
  errors: string[],
  registry: Registry,
): void {
  const properties = schema['properties'];
  if (isObject(properties)) {
    for (const key of Object.keys(properties)) {
      if (Object.prototype.hasOwnProperty.call(value, key)) {
        const subschema = properties[key];
        if (isObject(subschema)) {
          validateNode(document, subschema, value[key], `${path}.${key}`, errors, registry);
        }
      }
    }
  }

  const required = schema['required'];
  if (Array.isArray(required)) {
    for (const name of required) {
      if (typeof name === 'string' && !Object.prototype.hasOwnProperty.call(value, name)) {
        errors.push(`${path}: missing required property "${name}"`);
      }
    }
  }

  const additional = schema['additionalProperties'];
  if (additional !== undefined) {
    const known = isObject(properties) ? new Set(Object.keys(properties)) : new Set<string>();
    for (const key of Object.keys(value)) {
      if (known.has(key)) continue;
      if (additional === false) errors.push(`${path}: unexpected property "${key}"`);
      else if (isObject(additional)) {
        validateNode(document, additional, value[key], `${path}.${key}`, errors, registry);
      }
    }
  }
}

function validateArrayKeywords(
  document: ContractSchemaDocument,
  schema: Record<string, unknown>,
  value: unknown[],
  path: string,
  errors: string[],
  registry: Registry,
): void {
  const items = schema['items'];
  if (isObject(items)) {
    for (let index = 0; index < value.length; index += 1) {
      validateNode(document, items, value[index], `${path}[${index}]`, errors, registry);
    }
  }

  const minItems = schema['minItems'];
  if (typeof minItems === 'number' && value.length < minItems) {
    errors.push(`${path}: expected at least ${minItems} items, got ${value.length}`);
  }

  const maxItems = schema['maxItems'];
  if (typeof maxItems === 'number' && value.length > maxItems) {
    errors.push(`${path}: expected at most ${maxItems} items, got ${value.length}`);
  }

  if (schema['uniqueItems'] === true) {
    const seen = new Set<string>();
    for (let index = 0; index < value.length; index += 1) {
      const fingerprint = canonical(value[index]);
      if (seen.has(fingerprint)) {
        errors.push(`${path}[${index}]: duplicate item is not allowed by "uniqueItems"`);
      }
      seen.add(fingerprint);
    }
  }
}

function validateStringKeywords(
  schema: Record<string, unknown>,
  value: string,
  path: string,
  errors: string[],
): void {
  const length = [...value].length;
  const minLength = schema['minLength'];
  if (typeof minLength === 'number' && length < minLength) {
    errors.push(`${path}: expected at least ${minLength} characters, got ${length}`);
  }
  const maxLength = schema['maxLength'];
  if (typeof maxLength === 'number' && length > maxLength) {
    errors.push(`${path}: expected at most ${maxLength} characters, got ${length}`);
  }
  const pattern = schema['pattern'];
  if (typeof pattern === 'string' && !compiledPattern(pattern).test(value)) {
    errors.push(`${path}: ${describe(value)} does not match pattern ${pattern}`);
  }
}

function compiledPattern(pattern: string): RegExp {
  const cached = patternCache.get(pattern);
  if (cached) return cached;
  const compiled = new RegExp(pattern);
  patternCache.set(pattern, compiled);
  return compiled;
}

function validateNumberKeywords(
  schema: Record<string, unknown>,
  value: number,
  path: string,
  errors: string[],
): void {
  const minimum = schema['minimum'];
  if (typeof minimum === 'number' && value < minimum) {
    errors.push(`${path}: ${value} is below the minimum ${minimum}`);
  }
  const maximum = schema['maximum'];
  if (typeof maximum === 'number' && value > maximum) {
    errors.push(`${path}: ${value} is above the maximum ${maximum}`);
  }
}

const patternCache = new Map<string, RegExp>();

/* ------------------------------------------------------------------- helpers */

function passes(
  document: ContractSchemaDocument,
  schema: Record<string, unknown>,
  value: unknown,
  registry: Registry,
): boolean {
  const errors: string[] = [];
  validateNode(document, schema, value, '$', errors, registry);
  return errors.length === 0;
}

function resolveReference(
  registry: Registry,
  from: ContractSchemaDocument,
  reference: string,
): { document: ContractSchemaDocument; schema: Record<string, unknown> } {
  const hash = reference.indexOf('#');
  const file = hash === -1 ? reference : reference.slice(0, hash);
  const pointer = hash === -1 ? '' : reference.slice(hash + 1);
  const targetPath = file === '' ? normalizePath(from.path) : normalizePath(resolveRelative(from.path, file));
  const document = registry.get(targetPath);
  if (!document) throw new Error(`${from.path}: unresolved $ref document "${reference}"`);
  return { document, schema: lookupPointer(document, pointer, reference) };
}

function lookupPointer(
  document: ContractSchemaDocument,
  pointer: string,
  reference: string,
): Record<string, unknown> {
  if (pointer === '' || pointer === '/') return document.schema;
  if (!pointer.startsWith('/')) throw new Error(`${document.path}: unsupported $ref pointer "${reference}"`);
  let node: unknown = document.schema;
  for (const rawSegment of pointer.slice(1).split('/')) {
    const segment = decodeURIComponent(rawSegment).replace(/~1/g, '/').replace(/~0/g, '~');
    if (!isObject(node)) throw new Error(`${document.path}: $ref "${reference}" does not resolve`);
    node = node[segment];
  }
  if (!isObject(node)) throw new Error(`${document.path}: $ref "${reference}" does not resolve to a schema`);
  return node;
}

function typeMatches(type: string, value: unknown): boolean {
  switch (type) {
    case 'object':
      return isObject(value);
    case 'array':
      return Array.isArray(value);
    case 'string':
      return typeof value === 'string';
    case 'number':
      return typeof value === 'number';
    case 'integer':
      return typeof value === 'number' && Number.isInteger(value);
    case 'boolean':
      return typeof value === 'boolean';
    case 'null':
      return value === null;
    default:
      throw new Error(`unsupported "type" value: ${type}`);
  }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function deepEqual(left: unknown, right: unknown): boolean {
  return canonical(left) === canonical(right);
}

/** Order-insensitive JSON rendering, also used for "uniqueItems". */
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (isObject(value)) {
    const entries = Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`);
    return `{${entries.join(',')}}`;
  }
  return JSON.stringify(value) ?? 'undefined';
}

function describe(value: unknown): string {
  const rendered = JSON.stringify(value) ?? String(value);
  return rendered.length > 160 ? `${rendered.slice(0, 157)}...` : rendered;
}

function describeType(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  return typeof value;
}

function normalizePath(path: string): string {
  return resolveRelative('', path);
}

function resolveRelative(fromPath: string, reference: string): string {
  const base = fromPath === '' ? [] : fromPath.split('/').slice(0, -1);
  const segments = reference.split('/');
  const parts = [...base];
  for (const segment of segments) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') parts.pop();
    else parts.push(segment);
  }
  return parts.join('/');
}
