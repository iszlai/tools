// node test.js — no framework, no dependencies.
const assert = require('assert');
const { md } = require('./md.js');

let ran = 0;
const test = (name, fn) => {
  try {
    fn();
    ran++;
  } catch (err) {
    console.error(`FAIL  ${name}\n      ${err.message}`);
    process.exitCode = 1;
  }
};
const eq = (src, want) => assert.strictEqual(md(src), want);

test('headings', () => {
  eq('# One', '<h1>One</h1>');
  eq('## Two', '<h2>Two</h2>');
  eq('### Three', '<h3>Three</h3>');
  eq('#### Four', '<p>#### Four</p>');   // only three levels; the rest is prose
});

test('inline emphasis and code', () => {
  eq('**bold**', '<p><strong>bold</strong></p>');
  eq('*italic*', '<p><em>italic</em></p>');
  eq('`code`', '<p><code>code</code></p>');
});

test('unordered and ordered lists', () => {
  eq('- a\n- b', '<ul><li>a</li><li>b</li></ul>');
  eq('* a', '<ul><li>a</li></ul>');
  eq('1. a\n2. b', '<ol><li>a</li><li>b</li></ol>');
  eq('1) a', '<ol><li>a</li></ol>');
});

test('a list closes before the next block', () => {
  eq('- a\n\n# H', '<ul><li>a</li></ul><h1>H</h1>');
  eq('- a\n1. b', '<ul><li>a</li></ul><ol><li>b</li></ol>');
});

test('task lists render as checkboxes', () => {
  eq('- [ ] todo', '<ul><li class="task"><input type="checkbox" disabled>todo</li></ul>');
  eq('- [x] done', '<ul><li class="task done"><input type="checkbox" disabled checked>done</li></ul>');
  eq('- [X] done', '<ul><li class="task done"><input type="checkbox" disabled checked>done</li></ul>');
  // A task list is still a list, so it groups with plain items.
  eq('- [ ] a\n- b', '<ul><li class="task"><input type="checkbox" disabled>a</li><li>b</li></ul>');
});

test('fenced code is verbatim, not markdown', () => {
  eq('```\n**not bold**\n```', '<pre>**not bold**</pre>');
  eq('```\nunclosed', '<pre>unclosed</pre>');
});

// The whole reason this is safe to point at text that arrives over HTTP.
test('markup is escaped on every path', () => {
  eq('<script>alert(1)</script>', '<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>');
  eq('# <img onerror=x>', '<h1>&lt;img onerror=x&gt;</h1>');
  eq('- <b>x</b>', '<ul><li>&lt;b&gt;x&lt;/b&gt;</li></ul>');
  eq('- [ ] <b>x</b>', '<ul><li class="task"><input type="checkbox" disabled>&lt;b&gt;x&lt;/b&gt;</li></ul>');
  eq('```\n<b>x</b>\n```', '<pre>&lt;b&gt;x&lt;/b&gt;</pre>');
  eq('`<b>`', '<p><code>&lt;b&gt;</code></p>');
  eq('**<b>**', '<p><strong>&lt;b&gt;</strong></p>');
});

// Prose here is hard-wrapped, so this is the difference between readable and
// a paragraph break every 78 characters.
test('consecutive lines are one paragraph', () => {
  eq('one\ntwo', '<p>one two</p>');
  eq('one\n\ntwo', '<p>one</p><p>two</p>');
  eq('one\ntwo\n\nthree', '<p>one two</p><p>three</p>');
  // Emphasis may span a wrap, because the join happens before the inline pass.
  eq('**bold\nacross**', '<p><strong>bold across</strong></p>');
});

test('a paragraph closes before every other block', () => {
  eq('text\n# H', '<p>text</p><h1>H</h1>');
  eq('text\n- a', '<p>text</p><ul><li>a</li></ul>');
  eq('text\n1. a', '<p>text</p><ol><li>a</li></ol>');
  eq('text\n```\nx\n```', '<p>text</p><pre>x</pre>');
  eq('- a\ntext', '<ul><li>a</li></ul><p>text</p>');
});

test('empty and nullish input', () => {
  eq('', '');
  eq('\n\n', '');
  assert.strictEqual(md(null), '');
  assert.strictEqual(md(undefined), '');
});

test('a realistic ticket description', () => {
  const out = md([
    '## What to build',
    '',
    'Push the board to GitHub with `th sync`.',
    '',
    '- [x] dry run',
    '- [ ] idempotent',
  ].join('\n'));
  assert.ok(out.includes('<h2>What to build</h2>'), 'heading');
  assert.ok(out.includes('<code>th sync</code>'), 'inline code');
  assert.ok(out.includes('checked>dry run'), 'ticked box');
  assert.ok(out.includes('disabled>idempotent'), 'unticked box');
});

if (!process.exitCode) console.log(`ok  ${ran} tests`);
