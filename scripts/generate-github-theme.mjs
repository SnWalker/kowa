#!/usr/bin/env node
/**
 * Generates `src/styles/github-theme.css` from the open-source GitHub design
 * tokens shipped in `@primer/primitives`.
 *
 * Why a generator instead of importing Primer's stylesheet: the published
 * `dist/css/functional/themes/*.css` files carry ~1900 variables for every
 * GitHub surface (marketing, diffs, syntax, ANSI). Kowa needs one product
 * palette, in its own `data-theme` contract (system / light / dark), mapped
 * onto shadcn's variable names. Reading the token files and emitting exactly
 * the variables the theme actually consumes keeps the mapping reviewable and
 * fails loudly when an upstream token disappears.
 *
 * Every mapped variable must resolve to a concrete value in both themes; an
 * unresolved or missing token aborts generation with a non-zero exit instead
 * of silently emitting a broken palette. Values are copied verbatim — this
 * script never invents a colour.
 *
 * Usage: node scripts/generate-github-theme.mjs   (writes the file in place)
 */
import { readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const primitivesPackage = JSON.parse(
  readFileSync(require.resolve('@primer/primitives/package.json'), 'utf8'),
);
const primitivesDir = dirname(require.resolve('@primer/primitives/package.json'));

/**
 * The shadcn theme contract plus Kowa's restrained status palette, each bound
 * to a Primer functional token. `primary` is GitHub's accent (blue) rather than
 * its green success-emphasis button, because the product brief pins blue as the
 * primary colour; success/attention/danger/done stay reserved for machine
 * states, exactly as they are on GitHub.
 */
const COLOR_MAP = {
  // shadcn core contract
  '--background': 'bgColor-default',
  '--foreground': 'fgColor-default',
  '--card': 'bgColor-default',
  '--card-foreground': 'fgColor-default',
  '--popover': 'bgColor-default',
  '--popover-foreground': 'fgColor-default',
  '--primary': 'bgColor-accent-emphasis',
  '--primary-foreground': 'fgColor-onEmphasis',
  '--secondary': 'button-default-bgColor-rest',
  '--secondary-foreground': 'fgColor-default',
  '--secondary-hover': 'button-default-bgColor-hover',
  '--muted': 'bgColor-muted',
  '--muted-foreground': 'fgColor-muted',
  '--accent': 'control-transparent-bgColor-hover',
  '--accent-foreground': 'fgColor-default',
  '--destructive': 'bgColor-danger-emphasis',
  '--destructive-foreground': 'fgColor-onEmphasis',
  '--border': 'borderColor-default',
  '--input': 'borderColor-default',
  '--ring': 'focus-outlineColor',
  // Kowa status palette (fg + muted background + matching border per state)
  '--status-success': 'fgColor-success',
  '--status-success-muted': 'bgColor-success-muted',
  '--status-success-border': 'borderColor-success-muted',
  '--status-attention': 'fgColor-attention',
  '--status-attention-muted': 'bgColor-attention-muted',
  '--status-attention-border': 'borderColor-attention-muted',
  '--status-danger': 'fgColor-danger',
  '--status-danger-muted': 'bgColor-danger-muted',
  '--status-danger-border': 'borderColor-danger-muted',
  '--status-done': 'fgColor-done',
  '--status-done-muted': 'bgColor-done-muted',
  '--status-done-border': 'borderColor-done-muted',
  '--status-neutral': 'fgColor-muted',
  '--status-neutral-muted': 'bgColor-neutral-muted',
  '--status-neutral-border': 'borderColor-neutral-muted',
  // the primary colour's own family: link/icon text, hover state, selected
  // surface and tinted border. GitHub keeps the text accent (#0969da / #4493f8
  // in dark, where the emphasis background would fail AA on the dark canvas)
  // separate from the emphasis background, so the two stay distinct tokens.
  '--primary-fg': 'fgColor-accent',
  '--primary-muted': 'bgColor-accent-muted',
  '--primary-border': 'borderColor-accent-muted',
  // surfaces beyond card/muted that product chrome needs
  '--inset': 'bgColor-inset',
  '--border-muted': 'borderColor-muted',
};

/**
 * The one derived token. Primer has no hover step for the accent emphasis
 * colour — GitHub's own filled buttons are green, and `button-primary-*` is
 * that green. The product brief pins blue as primary, so the hover is derived
 * from the official token instead of invented as a hex: mixing 10% toward the
 * foreground darkens the button on the light canvas and lightens it on the
 * dark one, which is exactly how Primer's own hover steps behave.
 */
const PRIMARY_HOVER = 'color-mix(in oklab, var(--primary) 90%, var(--foreground))';

/** Typography and geometry come from the same package, emitted once. */
const SCALE_TOKENS = {
  typography: [
    ['--font-sans', 'fontStack-sansSerif'],
    ['--font-mono', 'fontStack-monospace'],
    ['--text-caption-size', 'text-caption-size'],
    ['--text-caption-line-height', 'text-caption-lineHeight'],
    ['--text-body-small-size', 'text-body-size-small'],
    ['--text-body-small-line-height', 'text-body-lineHeight-small'],
    ['--text-body-size', 'text-body-size-medium'],
    ['--text-body-line-height', 'text-body-lineHeight-medium'],
    ['--text-body-large-size', 'text-body-size-large'],
    ['--text-body-large-line-height', 'text-body-lineHeight-large'],
    ['--text-title-small-size', 'text-title-size-small'],
    ['--text-title-small-line-height', 'text-title-lineHeight-small'],
    ['--text-title-medium-size', 'text-title-size-medium'],
    ['--text-title-medium-line-height', 'text-title-lineHeight-medium'],
    ['--text-subtitle-size', 'text-subtitle-size'],
    ['--text-code-size', 'text-codeBlock-size'],
    ['--text-code-line-height', 'text-codeBlock-lineHeight'],
    ['--text-weight-normal', 'text-body-weight'],
    ['--text-weight-semibold', 'text-title-weight-small'],
  ],
  radius: [
    ['--radius-small', 'borderRadius-small'],
    ['--radius', 'borderRadius-default'],
    ['--radius-large', 'borderRadius-large'],
    ['--radius-full', 'borderRadius-full'],
  ],
  border: [
    ['--border-width', 'borderWidth-default'],
    ['--border-width-thick', 'borderWidth-thick'],
    ['--focus-outline-width', 'focus-outline-width'],
    ['--focus-outline-offset', 'focus-outline-offset'],
  ],
};

/** Loads one of the package's token documents (`dist/docs/functional/...`). */
function readTokenDocument(relativePath) {
  const file = join(primitivesDir, 'dist', 'docs', 'functional', relativePath);
  return JSON.parse(readFileSync(file, 'utf8'));
}

const TYPOGRAPHY = readTokenDocument('typography/typography.json');
const RADIUS = readTokenDocument('size/radius.json');
const BORDER = readTokenDocument('size/border.json');

/**
 * Parses the first declaration block of a Primer theme stylesheet (the block
 * before any `@media`, i.e. the canonical theme rather than its system-mode
 * mirror) and resolves `var(--x)` chains to concrete values.
 */
function readTheme(file) {
  const text = readFileSync(file, 'utf8');
  const mediaIndex = text.indexOf('@media');
  const block = mediaIndex === -1 ? text : text.slice(0, mediaIndex);
  const declarations = new Map();
  for (const match of block.matchAll(/^\s*--([A-Za-z0-9-]+):\s*([^;]+);/gm)) {
    declarations.set(match[1], match[2].trim());
  }
  const resolve = (name, seen) => {
    if (seen.has(name)) throw new Error(`${file}: circular var() reference at --${name}`);
    const value = declarations.get(name);
    if (value === undefined) throw new Error(`${file}: missing token --${name}`);
    const reference = /^var\(--([A-Za-z0-9-]+)\)$/.exec(value);
    if (!reference) return value;
    seen.add(name);
    return resolve(reference[1], seen);
  };
  return { resolve, count: declarations.size };
}

const themeFile = (theme) =>
  join(primitivesDir, 'dist', 'css', 'functional', 'themes', `${theme}.css`);
const light = readTheme(themeFile('light'));
const dark = readTheme(themeFile('dark'));

function colorBlock(theme, indent) {
  const lines = Object.entries(COLOR_MAP).map(([target, source]) => {
    const value = theme.resolve(source, new Set());
    return `${indent}  ${target}: ${value};`;
  });
  lines.push(`${indent}  --primary-hover: ${PRIMARY_HOVER};`);
  return lines.join('\n');
}

function scalarBlock(definitions) {
  return definitions
    .map(([target, document, key]) => {
      const entry = document[key];
      if (entry === undefined) {
        throw new Error(`missing scale token ${key} in @primer/primitives`);
      }
      const value = entry.value ?? entry.key;
      // Typography line heights arrive as numbers, sizes as strings.
      if (typeof value === 'number') return `  ${target}: ${value};`;
      if (typeof value !== 'string' || value.trim() === '') {
        throw new Error(`scale token ${key} has no scalar value`);
      }
      return `  ${target}: ${value.trim()};`;
    })
    .join('\n');
}

const provenance = `/*
 * Generated by scripts/generate-github-theme.mjs — do not edit by hand.
 * Source: @primer/primitives ${primitivesPackage.version}
 *   dist/css/functional/themes/light.css
 *   dist/css/functional/themes/dark.css
 *   dist/docs/functional/typography/typography.json
 *   dist/docs/functional/size/{radius,border}.json
 * Every colour is copied verbatim from those files except --primary-hover, which
 * is derived (see PRIMARY_HOVER). Regenerate with
 * \`mise exec -- node scripts/generate-github-theme.mjs\`.
 */`;

const output = `${provenance}

:root {
  color-scheme: light;
${colorBlock(light, '')}
}

:root[data-theme="dark"] {
  color-scheme: dark;
${colorBlock(dark, '')}
}

@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]):not([data-theme="dark"]) {
    color-scheme: dark;
${colorBlock(dark, '  ')}
  }
}

/* Theme-independent scale: typography, radius and border geometry. */
:root {
${scalarBlock(SCALE_TOKENS.typography.map(([target, key]) => [target, TYPOGRAPHY, key]))}
${scalarBlock(SCALE_TOKENS.radius.map(([target, key]) => [target, RADIUS, key]))}
${scalarBlock(SCALE_TOKENS.border.map(([target, key]) => [target, BORDER, key]))}
}
`;

const target = join(root, 'src', 'styles', 'github-theme.css');
writeFileSync(target, output);
const scaleCount =
  SCALE_TOKENS.typography.length + SCALE_TOKENS.radius.length + SCALE_TOKENS.border.length;
process.stdout.write(
  `wrote ${relative(root, target)} (${Object.keys(COLOR_MAP).length} colours x 3 blocks, ` +
    `${scaleCount} scale tokens) from @primer/primitives ${primitivesPackage.version}\n`,
);
