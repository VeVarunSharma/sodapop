import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { parseDocument } from 'yaml';

const repository = 'https://github.com/VeVarunSharma/sodapop';
const directory = new URL('../../../.github/ISSUE_TEMPLATE/', import.meta.url);
const definitions = [
  {
    file: '01-bug-report.yml',
    label: 'bug',
    fields: [
      ['behavior', 'textarea', true],
      ['reproduction', 'textarea', true],
      ['environment', 'textarea', true],
      ['context', 'textarea', false],
    ],
  },
  {
    file: '02-feature-request.yml',
    label: 'enhancement',
    fields: [
      ['problem', 'textarea', true],
      ['proposal', 'textarea', true],
      ['alternatives', 'textarea', false],
    ],
  },
  {
    file: '03-documentation.yml',
    label: 'documentation',
    fields: [
      ['location', 'input', true],
      ['problem', 'textarea', true],
      ['suggestion', 'textarea', false],
    ],
  },
];

function parseYAML(source) {
  const document = parseDocument(source, { strict: true, uniqueKeys: true });
  assert.equal(document.errors.length, 0,
    `YAML must parse without errors: ${document.errors.map((error) => error.message).join('; ')}`);
  return document.toJS({ maxAliasCount: 0 });
}

function allowedKeys(value, keys, description) {
  assert.ok(value !== null && typeof value === 'object' && !Array.isArray(value),
    `${description} must be an object`);
  for (const key of Object.keys(value)) {
    assert.ok(keys.includes(key), `${description} has an unexpected key: ${key}`);
  }
}

function nonempty(value, description) {
  assert.equal(typeof value, 'string', `${description} must be a string`);
  assert.ok(value.trim(), `${description} must not be empty`);
}

function validateForm(form, definition) {
  allowedKeys(form, ['name', 'description', 'labels', 'body'], 'form metadata');
  nonempty(form.name, 'form name');
  assert.ok(form.name.length > 3, 'GitHub form names need more than three characters');
  nonempty(form.description, 'form description');
  assert.deepEqual(form.labels, [definition.label], 'form label must match the existing repository label');
  assert.ok(Array.isArray(form.body), 'form body must be an array');

  const inputs = [];
  const introductions = [];
  for (const element of form.body) {
    if (element.type === 'markdown') {
      allowedKeys(element, ['type', 'attributes'], 'introduction');
      allowedKeys(element.attributes, ['value'], 'introduction attributes');
      nonempty(element.attributes.value, 'introduction copy');
      introductions.push(element.attributes.value);
      continue;
    }
    allowedKeys(element, ['type', 'id', 'attributes', 'validations'], 'input');
    assert.ok(['input', 'textarea'].includes(element.type), 'forms should use only simple text inputs');
    nonempty(element.id, 'field ID');
    assert.match(element.id, /^[a-zA-Z0-9_-]+$/, 'field ID must use GitHub-supported characters');
    allowedKeys(element.attributes, ['label', 'description', 'placeholder', 'value', 'render'], 'input attributes');
    nonempty(element.attributes.label, 'input label');
    for (const attribute of ['description', 'placeholder']) {
      if (Object.hasOwn(element.attributes, attribute)) {
        nonempty(element.attributes[attribute], `input ${attribute}`);
      }
    }
    allowedKeys(element.validations, ['required'], 'input validations');
    assert.equal(typeof element.validations.required, 'boolean', 'required must be an explicit boolean');
    if (element.validations.required) {
      assert.equal(Object.hasOwn(element.attributes, 'value'), false,
        'required answers must not be prefilled; use placeholders');
    }
    assert.equal(element.attributes.render, undefined,
      'text inputs must preserve Markdown editing and textarea attachments');
    inputs.push(element);
  }
  assert.equal(introductions.length, 1, 'each form needs one short introduction');
  assert.equal(form.body[0].type, 'markdown', 'sharing guidance must appear before user inputs');
  const intro = introductions[0];
  assert.ok(intro.includes(`${repository}/issues`), 'include the canonical existing-issues link');
  for (const concept of [
    /tokens/i, /device codes/i, /personal paths/i, /confidential/i,
    /environment files/i, /state directories/i, /credential-store dumps/i,
    /raw OAuth responses/i, /security-sensitive.*public issues/i,
  ]) {
    assert.match(intro, concept, 'keep the relevant privacy and public-reporting guidance');
  }
  assert.equal(new Set(inputs.map(({ id }) => id)).size, inputs.length, 'field IDs must be unique within a form');
  assert.equal(inputs.length, definition.fields.length, 'keep the agreed lean input count');
  assert.deepEqual(
    inputs.map(({ id, type, validations }) => [id, type, validations.required]),
    definition.fields,
    'keep stable IDs, field types, and the agreed required/optional inputs',
  );
}

function validateChooser(config) {
  allowedKeys(config, ['blank_issues_enabled', 'contact_links'], 'chooser');
  assert.equal(config.blank_issues_enabled, true, 'keep the blank-issue escape hatch');
  assert.ok(Array.isArray(config.contact_links), 'contact links must be an array');
  assert.equal(config.contact_links.length, 1, 'use only the confirmed help contact');
  const link = config.contact_links[0];
  allowedKeys(link, ['name', 'url', 'about'], 'help contact');
  nonempty(link.name, 'help contact name');
  nonempty(link.about, 'help contact description');
  assert.equal(link.url, `${repository}#readme`, 'help must use the verified README, not an unconfigured channel');
}

const forms = [];
for (const definition of definitions) {
  forms.push(parseYAML(await readFile(new URL(definition.file, directory), 'utf8')));
}
const chooser = parseYAML(await readFile(new URL('config.yml', directory), 'utf8'));

for (const [index, definition] of definitions.entries()) {
  test(`${definition.file} is a lean, privacy-conscious issue form`, () => {
    validateForm(forms[index], definition);
  });
}

test('the chooser offers exactly three distinct forms, blank issues, and a real help link', async () => {
  const files = (await readdir(directory))
    .filter((file) => /\.(?:ya?ml|md)$/i.test(file) && !['AGENTS.md', 'README.md'].includes(file))
    .sort();
  assert.deepEqual(files, [...definitions.map(({ file }) => file), 'config.yml'].sort());
  assert.equal(new Set(forms.map(({ name }) => name.toLowerCase())).size, definitions.length);
  validateChooser(chooser);
});

test('bug diagnostics work without authentication or a fixed platform/version catalog', () => {
  const environment = forms[0].body.find(({ id }) => id === 'environment');
  assert.equal(environment.type, 'textarea');
  assert.match(environment.attributes.description, /sodapop --version/);
  assert.match(environment.attributes.description, /unavailable/i);
  assert.match(environment.attributes.description, /unknown/i);
  assert.match(environment.attributes.description, /Do not sign in, sign out, or delete state/i);
  for (const detail of [/SDK.*runtime/i, /OS.*architecture/i, /Terminal/i, /Installation method/i, /if known/i, /if relevant/i]) {
    assert.match(environment.attributes.placeholder, detail);
  }
});

test('malformed YAML and duplicate metadata keys are rejected', () => {
  for (const source of ['body: [', 'name: First\nname: Second\nbody: []\n']) {
    assert.throws(() => parseYAML(source), /YAML must parse without errors/);
  }
});

for (const [name, mutate, expected] of [
  ['duplicate IDs', (form) => { form.body[2].id = form.body[1].id; }, /IDs must be unique/],
  ['invalid IDs', (form) => { form.body[1].id = 'invalid id'; }, /GitHub-supported characters/],
  ['unavailable labels', (form) => { form.labels = ['triage']; }, /existing repository label/],
  ['prefilled required answers', (form) => { form.body[1].attributes.value = 'Replace this'; }, /must not be prefilled/],
  ['code-only evidence', (form) => { form.body[4].attributes.render = 'shell'; }, /textarea attachments/],
  ['automatic assignment', (form) => { form.assignees = ['maintainer']; }, /unexpected key: assignees/],
  ['forced titles', (form) => { form.title = '[Bug] '; }, /unexpected key: title/],
  ['organization issue types', (form) => { form.type = 'bug'; }, /unexpected key: type/],
  ['extra required controls', (form) => {
    form.body.push({ type: 'input', id: 'extra', attributes: { label: 'Extra' }, validations: { required: true } });
  }, /lean input count/],
]) {
  test(`form checks catch ${name}`, () => {
    const invalid = structuredClone(forms[0]);
    mutate(invalid);
    assert.throws(() => validateForm(invalid, definitions[0]), expected);
  });
}

test('chooser checks reject removed fallbacks and unconfigured private-reporting links', () => {
  const noBlank = structuredClone(chooser);
  noBlank.blank_issues_enabled = false;
  assert.throws(() => validateChooser(noBlank), /blank-issue escape hatch/);
  const unavailableContact = structuredClone(chooser);
  unavailableContact.contact_links[0].url = `${repository}/security/advisories/new`;
  assert.throws(() => validateChooser(unavailableContact), /unconfigured channel/);
});

test('reporting and maintainer guides describe the actual intake and checks', async () => {
  const troubleshooting = await readFile(new URL('../../../docs/troubleshooting.md', import.meta.url), 'utf8');
  assert.ok(troubleshooting.includes(`${repository}/issues/new/choose`));
  assert.match(troubleshooting, /blank issue/i);
  assert.match(troubleshooting, /sodapop --version/);
  assert.match(troubleshooting, /unavailable.*unknown/i);
  const development = await readFile(new URL('../../../docs/development.md', import.meta.url), 'utf8');
  assert.ok(development.includes('node --test site/test/unit/issue-forms.test.mjs site/test/unit/content.test.mjs'));
  assert.match(development, /default branch/i);
  assert.match(development, /template-only edits/);
});
