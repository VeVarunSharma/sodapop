import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parse } from 'yaml';

const workflowText = await readFile(new URL('../../../.github/workflows/pages.yml', import.meta.url), 'utf8');
const workflow = parse(workflowText);

test('Pages separates read-only review from trusted static publication', () => {
  assert.equal(workflow.on.pull_request_target, undefined);
  assert.deepEqual(workflow.on.release.types, ['published']);
  assert.equal(workflow.jobs.build.permissions.contents, 'read');
  assert.equal(workflow.jobs.build.permissions.pages, 'read');
  assert.equal(workflow.jobs.build.permissions['id-token'], undefined);
  assert.equal(workflow.jobs.build.permissions.actions, undefined);
  assert.equal(workflow.jobs.build.if, "github.event_name != 'release'");
  assert.match(workflow.jobs.build.env.SODAPOP_SITE_PREVIEW, /github\.event_name != 'pull_request'.*default_branch.*'0'.*'1'/);
  const deploy = workflow.jobs.deploy;
  assert.equal(deploy.permissions.pages, 'write');
  assert.equal(deploy.permissions['id-token'], 'write');
  assert.match(deploy.if, /github\.event_name != 'pull_request'/);
  assert.match(deploy.if, /github\.event_name != 'release'/);
  assert.match(deploy.if, /default_branch/);
  assert.equal(deploy.environment.name, 'github-pages');
  const steps = workflow.jobs.build.steps;
  const upload = steps.find((step) => step.uses?.startsWith('actions/upload-pages-artifact@'));
  assert.equal(upload.with.path, 'site/dist');
  assert.match(upload.if, /matrix\.publication == 'configured'/);
  assert.match(upload.if, /env\.SODAPOP_SITE_PREVIEW == '0'/);
  const checkout = steps.find((step) => step.uses?.startsWith('actions/checkout@'));
  assert.equal(checkout.with['persist-credentials'], false);
  assert.equal(checkout.with.ref, '${{ github.ref }}');
  const resolve = steps.find((step) => step.run === 'npm --prefix site run release:sync');
  assert.equal(resolve.if, "env.SODAPOP_SITE_PREVIEW == '0'");
  assert.equal(resolve.env, undefined);
  assert.ok(steps.some((step) => step.run === 'npm --prefix site run check:artifact -- --public'));
});

test('both URL bases are exercised without native or package publication work', () => {
  assert.deepEqual(workflow.jobs.build.strategy.matrix.publication, ['configured', 'project']);
  assert.equal(workflow.jobs.build.env.SODAPOP_SITE_BASE, undefined);
  assert.equal(workflow.jobs.build.env.SODAPOP_SITE_URL, undefined);
  for (const forbidden of [/make (?:build|bundle|package|qualify|runtime-smoke)/, /npm publish/, /gh release create/, /SODAPOP_GITHUB_CLIENT_ID/]) {
    assert.doesNotMatch(workflowText, forbidden);
  }
});

test('Pages metadata determines the target before any build command', () => {
  const steps = workflow.jobs.build.steps;
  const pages = steps.findIndex((step) => step.id === 'pages');
  const target = steps.findIndex((step) => step.run === 'node site/scripts/pages-target.mjs');
  const build = steps.findIndex((step) => step.run === 'npm --prefix site run build');
  assert.ok(pages >= 0 && target > pages && build > target);
  assert.match(steps[pages].if, /env\.SODAPOP_SITE_PREVIEW == '0'/);
  assert.equal(steps[target].env.SODAPOP_PAGES_URL, '${{ steps.pages.outputs.base_url }}');
  assert.match(steps[target].env.SODAPOP_PAGES_REQUIRED, /configured.*SODAPOP_SITE_PREVIEW == '0'/);
  assert.ok(steps.some((step) => step.run?.includes('assertPagesTarget(process.env.SODAPOP_PAGES_URL)')));
});

test('release refresh dispatches main instead of trying to deploy from a tag', () => {
  const refresh = workflow.jobs['release-refresh'];
  assert.equal(refresh.if, "github.event_name == 'release'");
  assert.equal(refresh.permissions.actions, 'write');
  assert.equal(refresh.permissions.pages, undefined);
  assert.equal(refresh.permissions['id-token'], undefined);
  assert.equal(refresh.steps.length, 1);
  assert.equal(refresh.steps[0].env.SODAPOP_DEFAULT_BRANCH, '${{ github.event.repository.default_branch }}');
  assert.match(refresh.steps[0].run, /gh workflow run pages\.yml/);
  assert.match(refresh.steps[0].run, /--ref "\$SODAPOP_DEFAULT_BRANCH"/);
  assert.doesNotMatch(refresh.steps[0].run, /tag_name|head_ref/);
  assert.match(workflow.concurrency.group, /release.*github\.run_id.*github\.ref/);
});

test('website checks include its source-identity and documentation inputs', () => {
  for (const event of ['push', 'pull_request']) {
    for (const input of ['go.mod', 'internal/runtimebundle/version.go', 'internal/commands/registry.go', 'packaging/windows/README.md']) {
      assert.ok(workflow.on[event].paths.includes(input), `missing ${event} input ${input}`);
    }
  }
});

test('website package cannot be published as the CLI', async () => {
  const pkg = JSON.parse(await readFile(new URL('../../package.json', import.meta.url), 'utf8'));
  assert.equal(pkg.private, true);
  assert.equal(pkg.name, '@sodapop/website');
  assert.equal(pkg.bin, undefined);
  assert.equal(pkg.workspaces, undefined);
  assert.equal(pkg.scripts['release:sync'], 'node scripts/resolve-releases.mjs');
});
