'use strict';
// FR-28/FR-29 smoke: drives the real flowreview binary in headless Chromium
// against a repo with no model configured, so every summary takes the
// deterministic fallback path (internal/llm/summary.Fallback) -- the only
// path this binary can exercise without downloading a multi-GB model. Kept
// separate from tests/smoke.js (which app.js/file-list/sidebar work is
// concurrently changing) so this file never needs to touch, or conflict
// with, that one; `make smoke` runs both.
//
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke-summary.js

const { chromium } = require('playwright-core');
const { execFileSync, spawn } = require('child_process');
const { mkdtempSync, writeFileSync } = require('fs');
const { tmpdir } = require('os');
const path = require('path');

const BIN = path.resolve(__dirname, '..', 'bin', 'flowreview');

let failures = 0;
function check(name, cond, detail = '') {
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  -- ' + detail}`);
  if (!cond) failures++;
}

function git(dir, env, ...args) {
  execFileSync('git', args, { cwd: dir, env: { ...process.env, ...env } });
}

// goFnSource mirrors tests/smoke.js's own helper: a function whose body is
// long enough that the one changed line sits well inside it, so expand/
// enclosing-function resolution (which the fallback's "what" text also uses
// for its location name) has something real to find.
function goFnSource(tenValue) {
  const lines = ['package main', '', 'func Compute() int {'];
  for (let i = 0; i < 20; i++) lines.push(`\tv${i} := ${i === 10 ? tenValue : i}`);
  lines.push('\treturn v0', '}');
  return lines.join('\n') + '\n';
}

function buildRepo() {
  const dir = mkdtempSync(path.join(tmpdir(), 'flowreview-smoke-summary-'));
  const env = {
    GIT_AUTHOR_NAME: 'Smoke', GIT_AUTHOR_EMAIL: 'smoke@example.com',
    GIT_COMMITTER_NAME: 'Smoke', GIT_COMMITTER_EMAIL: 'smoke@example.com',
  };
  git(dir, env, 'init', '-q', '-b', 'main', '.');
  git(dir, env, 'config', 'user.name', 'Smoke');
  git(dir, env, 'config', 'user.email', 'smoke@example.com');

  writeFileSync(path.join(dir, 'fn.go'), goFnSource(10));
  git(dir, env, 'add', 'fn.go');
  git(dir, env, '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'init');

  writeFileSync(path.join(dir, 'fn.go'), goFnSource(100));
  return dir;
}

// serve starts flowreview --no-open against repoDir with an isolated
// FLOWREVIEW_HOME (a fresh temp dir, never the user's real
// ~/Library/Application Support/FlowReview) and no config.json in it, so
// models.LoadConfig sees Enabled=false and the summary pipeline is
// fallback-only -- exactly the path this smoke test checks.
function serve(repoDir, home) {
  const proc = spawn(BIN, ['--no-open', '--port', '0'], {
    cwd: repoDir,
    env: { ...process.env, FLOWREVIEW_HOME: home },
  });
  return new Promise((resolve, reject) => {
    let buf = '';
    proc.stdout.on('data', (d) => {
      buf += d;
      const m = buf.match(/https?:\/\/127\.0\.0\.1:\d+\//);
      if (m) resolve({ proc, url: m[0] });
    });
    proc.stderr.on('data', (d) => process.stderr.write(d));
    proc.on('error', reject);
    setTimeout(() => reject(new Error('flowreview did not report a URL in time')), 5000);
  });
}

(async () => {
  const repoDir = buildRepo();
  const home = mkdtempSync(path.join(tmpdir(), 'flowreview-smoke-summary-home-'));
  const { proc, url } = await serve(repoDir, home);
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });

  try {
    await page.goto(url);
    await page.waitForSelector('.file-row');
    await page.locator('.file-row', { hasText: 'fn.go' }).click();
    await page.waitForSelector('#diff-rows [data-hunk]');

    /* 1 -- a card renders above the hunk, with a fallback marker: no model
       is configured in this isolated FLOWREVIEW_HOME, so every summary
       takes internal/llm/summary.Fallback's deterministic path. */
    await page.waitForSelector('.fr-summary-strip .fr-summary-toggle', { timeout: 5000 });
    const fallbackCount = await page.locator('.fr-fallback').count();
    check('FR-29: a summary card renders with a fallback marker', fallbackCount > 0, `fallback markers: ${fallbackCount}`);

    const whatText = await page.locator('.fr-summary-strip .fr-what').first().textContent();
    check('FR-29: the card has non-empty "what" text', !!whatText && whatText.trim().length > 0, JSON.stringify(whatText));

    /* 2 -- clicking the strip opens a popover with the regenerate glyph
       button, and Escape (or the close button) closes it again. */
    await page.locator('.fr-summary-strip .fr-summary-toggle').first().click();
    await page.waitForSelector('#fr-summary-popover');
    const regenVisible = await page.isVisible('#fr-summary-popover .fr-regenerate');
    check('FR-29: the popover has a visible regenerate button', regenVisible);
    await page.keyboard.press('Escape');
    await page.waitForSelector('#fr-summary-popover', { state: 'detached', timeout: 2000 }).catch(() => {});
    const popoverGone = (await page.locator('#fr-summary-popover').count()) === 0;
    check('FR-29: Escape closes the popover', popoverGone);

    /* 3 -- the visible on/off toggle (not just the 's' shortcut) hides
       every strip, and toggling back on shows them again from cache
       without a new network request. */
    const toggle = page.locator('#fr-summary-toggle-button');
    check('FR-29: a visible toggle button exists', (await toggle.count()) === 1);
    await toggle.click();
    await page.waitForFunction(() => document.body.classList.contains('fr-summaries-off'));
    const stripVisibleOff = await page.locator('.fr-summary-strip').first().isVisible();
    check('FR-29: toggling off hides summary cards', !stripVisibleOff);

    await toggle.click();
    await page.waitForFunction(() => !document.body.classList.contains('fr-summaries-off'));
    const stripVisibleOn = await page.locator('.fr-summary-strip').first().isVisible();
    check('FR-29: toggling back on shows summary cards again', stripVisibleOn);

    /* 4 -- the 's' keyboard shortcut does the same thing as the button. */
    await page.keyboard.press('s');
    await page.waitForFunction(() => document.body.classList.contains('fr-summaries-off'));
    check('FR-29: the \'s\' shortcut also toggles summaries off', true);
    await page.keyboard.press('s');
    await page.waitForFunction(() => !document.body.classList.contains('fr-summaries-off'));

    /* 5 -- the '?' help overlay lists the 's' shortcut. */
    await page.keyboard.press('?');
    await page.waitForSelector('#help-overlay:not([hidden])');
    const helpText = await page.textContent('#help-overlay');
    check('FR-29: the help overlay documents the \'s\' shortcut', /toggle hunk summaries/i.test(helpText), helpText);
    await page.keyboard.press('Escape');
  } finally {
    await browser.close();
    proc.kill();
  }

  console.log(failures ? `\n${failures} failure(s)` : '\nall checks passed');
  process.exit(failures ? 1 : 0);
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
