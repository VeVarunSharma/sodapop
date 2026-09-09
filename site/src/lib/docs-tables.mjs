import { parseFragment } from 'parse5';

/** @param {string} html */
export function focusableTables(html) {
  const document = parseFragment(html, { sourceCodeLocationInfo: true });
  /** @type {number[]} */
  const offsets = [];
  /** @param {import('parse5').DefaultTreeAdapterMap['node']} node */
  function visit(node) {
    if ('tagName' in node && node.tagName === 'table' &&
        !node.attrs.some(({ name }) => name === 'tabindex')) {
      const tag = node.sourceCodeLocation?.startTag;
      if (!tag) throw new Error('A documentation table is missing its source opening tag');
      offsets.push(tag.startOffset + '<table'.length);
    }
    if ('childNodes' in node) node.childNodes.forEach(visit);
  }
  visit(document);
  // Preserve rendered code and other markup byte-for-byte.
  for (const offset of offsets.sort((a, b) => b - a)) {
    html = `${html.slice(0, offset)} tabindex="0"${html.slice(offset)}`;
  }
  return html;
}
