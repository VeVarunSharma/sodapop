import test from 'node:test';
import assert from 'node:assert/strict';
import { focusableTables } from '../../src/lib/docs-tables.mjs';

test('documentation tables gain keyboard focus without rewriting surrounding markup', () => {
  const html = `<p>Let's explore <code>/theme</code>.</p>
<table class='themes'><thead><tr><th>Theme</th></tr></thead><tbody><tr><td>Neon Arcade</td></tr></tbody></table>
<TABLE
id="commands"><tr><td><code>/theme arcade</code></td></tr></TABLE>`;
  assert.equal(focusableTables(html),
    html.replace('<table ', '<table tabindex="0" ').replace('<TABLE\n', '<TABLE tabindex="0"\n'));
});

test('existing focus choices and repeated renders are preserved', () => {
  const html = '<table tabindex="0" class="themes"><tr><td>Theme</td></tr></table>' +
    '<table tabindex="-1"><tr><td>Programmatic focus</td></tr></table>';
  assert.equal(focusableTables(html), html);
  const focused = focusableTables('<table><tr><td>One</td></tr></table>');
  assert.equal(focusableTables(focused), focused);
});

test('table text in code, comments, and attributes is not mistaken for an element', () => {
  const html = '<pre><code>&lt;table&gt;example&lt;/table&gt;</code></pre>' +
    '<!-- <table>not rendered</table> --><p data-example="<table>">A &amp; B</p>';
  assert.equal(focusableTables(html), html);
  assert.equal(focusableTables(''), '');
});
