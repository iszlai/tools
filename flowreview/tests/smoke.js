'use strict';
// Drives the real flowreview binary in headless Chromium. Builds a temp git
// repo with changes, starts `bin/flowreview --no-open` against it, and
// exercises the UI shell FR-5 built: the file queue, the unified diff rows
// with their anchor data attributes, keyboard navigation, and the session
// summary panel -- everything the Go tests cannot see because it only exists
// in the browser.
//
//   npm install playwright-core   (or rely on ~/node_modules, see taskhound)
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers npx playwright-core install chromium-headless-shell
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke.js
//
// It needs Playwright's own chromium: corporate-managed Chrome refuses to be
// automated ("remote debugging is disallowed by the system admin").

const { chromium } = require('playwright-core');
const { execFileSync, spawn } = require('child_process');
const { mkdtempSync, writeFileSync } = require('fs');
const { tmpdir } = require('os');
const path = require('path');

const BIN = path.resolve(__dirname, '..', 'bin', 'flowreview');
const PAINT_BUDGET_MS = 1000; // PLAN.md section 8: <1s session open

let failures = 0;
function check(name, cond, detail = '') {
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  -- ' + detail}`);
  if (!cond) failures++;
}

function git(dir, env, ...args) {
  execFileSync('git', args, { cwd: dir, env: { ...process.env, ...env } });
}

// buildRepo makes a small repo with a modified tracked file and a new
// untracked one, so the file queue has more than one entry and WorktreeDiff
// exercises both its tracked and untracked-synthesis paths.
function buildRepo() {
  const dir = mkdtempSync(path.join(tmpdir(), 'flowreview-smoke-'));
  const env = {
    GIT_AUTHOR_NAME: 'Smoke', GIT_AUTHOR_EMAIL: 'smoke@example.com',
    GIT_COMMITTER_NAME: 'Smoke', GIT_COMMITTER_EMAIL: 'smoke@example.com',
  };
  git(dir, env, 'init', '-q', '-b', 'main', '.');
  git(dir, env, 'config', 'user.name', 'Smoke');
  git(dir, env, 'config', 'user.email', 'smoke@example.com');

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline two\nline three\n');
  git(dir, env, 'add', 'a.txt');
  git(dir, env, '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'init');

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline TWO\nline three\n');
  writeFileSync(path.join(dir, 'b.txt'), 'a brand new file\n');
  return dir;
}

// serve starts flowreview --no-open against repoDir and resolves once it has
// printed its URL (the first line on stdout, per FR-5's contract).
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
  const home = mkdtempSync(path.join(tmpdir(), 'flowreview-home-'));
  const { proc, url } = await serve(repoDir, home);
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });

  try {
    const t0 = Date.now();
    await page.goto(url);
    await page.waitForSelector('.file-row');
    const paintMs = Date.now() - t0;
    console.log(`session open -> first paint: ${paintMs}ms (budget ${PAINT_BUDGET_MS}ms)`);
    check('performance: first paint is under budget', paintMs < PAINT_BUDGET_MS, `${paintMs}ms`);

    /* 1 -- the file queue shows every changed file */
    const paths = await page.$$eval('.file-row .path', (els) => els.map((e) => e.textContent));
    check('files: the modified file is listed', paths.includes('a.txt'), paths.join(','));
    check('files: the added file is listed', paths.includes('b.txt'), paths.join(','));

    /* 2 -- the session panel shows real progress, not a placeholder */
    const summary = await page.textContent('#session-summary');
    check('session: the summary reports hunk progress', /hunks reviewed/.test(summary), summary);

    /* 3 -- clicking a file renders its rows with exact anchors */
    await page.click('.file-row[data-file]');
    await page.waitForSelector('#diff tr[data-hunk]');
    const addRow = page.locator('#diff tr.row-add').first();
    await addRow.waitFor();
    const anchor = await addRow.evaluate((el) => ({ ...el.dataset }));
    check('rows: an added line carries file/side/line/hunk/position',
      !!anchor.file && anchor.side === 'new' && !!anchor.line && !!anchor.hunk && !!anchor.position,
      JSON.stringify(anchor));

    /* 4 -- j/k move the file selection */
    const selectedPath = () => page.locator('.file-row.selected .path').first().textContent();
    const before = await selectedPath();
    await page.keyboard.press('j');
    await page.waitForFunction((prev) => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent !== prev;
    }, before, { timeout: 2000 }).catch(() => {});
    const after = await selectedPath();
    check('keyboard: j moves to the next file', after !== before, `${before} -> ${after}`);

    await page.keyboard.press('k');
    await page.waitForFunction((prev) => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent === prev;
    }, before, { timeout: 2000 }).catch(() => {});
    const back = await selectedPath();
    check('keyboard: k moves back to the previous file', back === before, `${back} vs ${before}`);

    /* 5 -- marking a hunk reviewed updates the file queue's badge */
    await page.click('#diff tr[data-hunk]');
    const hunkId = await page.getAttribute('#diff tr[data-hunk]', 'data-hunk');
    await page.evaluate((hid) => fetch('/api/state', {
      method: 'POST',
      body: JSON.stringify({ hunk_id: hid, reviewed: true }),
    }), hunkId);
    await page.reload();
    await page.waitForSelector('.file-row');
    const badgeCount = await page.locator('.file-row .reviewed-badge').count();
    check('review state: a fully reviewed file shows a badge after reload', badgeCount >= 1, String(badgeCount));

    /* 6 -- the API behind the UI responds */
    const health = await page.evaluate(() => fetch('/api/health').then((r) => r.json()));
    check('api: /api/health reports ok', health.ok === true, JSON.stringify(health));
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
