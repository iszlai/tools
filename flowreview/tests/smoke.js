'use strict';
// Drives the real flowreview binary in headless Chromium. Builds a temp git
// repo with changes (a few small files plus one 10k-line file), starts
// `bin/flowreview --no-open` against it, and exercises the UI FR-5/FR-6
// built: the file queue, the virtualized side-by-side diff with its anchor
// data attributes, hunk/file keyboard navigation, the session summary panel,
// and the PLAN.md section 8 performance budget -- everything the Go tests
// cannot see because it only exists in the browser.
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
const PAINT_BUDGET_MS = 1000; // PLAN.md section 8: <1s session open / file first paint
const SWITCH_BUDGET_MS = 100; // PLAN.md section 8: <100ms file switch
const LONGTASK_BUDGET_MS = 50; // PLAN.md section 8: no janky frame while scrolling
const BIG_FILE_LINES = 10000;

let failures = 0;
function check(name, cond, detail = '') {
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  -- ' + detail}`);
  if (!cond) failures++;
}

function git(dir, env, ...args) {
  execFileSync('git', args, { cwd: dir, env: { ...process.env, ...env } });
}

// buildRepo makes a repo with: a modified tracked file and a new untracked
// one (so the file queue has more than one entry and WorktreeDiff exercises
// both its tracked and untracked-synthesis paths), plus a 10k-line file with
// changes scattered through it, for the performance budget below.
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
  const bigLines = [];
  for (let i = 0; i < BIG_FILE_LINES; i++) bigLines.push(`const line${i} = ${i};`);
  writeFileSync(path.join(dir, 'big.js'), bigLines.join('\n') + '\n');
  git(dir, env, 'add', 'a.txt', 'big.js');
  git(dir, env, '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'init');

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline TWO\nline three\n');
  writeFileSync(path.join(dir, 'b.txt'), 'a brand new file\n');
  // Scatter ~100 single-line edits through the big file -- enough hunks to
  // make virtualization matter across the whole scroll, not so many that
  // every line is a hunk (which no real diff looks like).
  for (let i = 0; i < BIG_FILE_LINES; i += 100) {
    bigLines[i] = `const line${i} = ${i} * 2; // changed`;
  }
  writeFileSync(path.join(dir, 'big.js'), bigLines.join('\n') + '\n');
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

// selectFileAndMeasure clicks the file-row whose path contains pathSubstr
// and returns the milliseconds from click until its rows have both arrived
// (the GET /api/file/{id}/rows response) and painted (one settled frame
// after that), for the first-paint and file-switch budgets below.
async function selectFileAndMeasure(page, pathSubstr) {
  const row = page.locator('.file-row', { hasText: pathSubstr });
  const t0 = Date.now();
  await Promise.all([
    page.waitForResponse((r) => /\/api\/file\/.*\/rows/.test(r.url())),
    row.click(),
  ]);
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  return Date.now() - t0;
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
    check('files: the 10k-line file is listed', paths.includes('big.js'), paths.join(','));

    /* 2 -- the session panel shows real progress, not a placeholder */
    const summary = await page.textContent('#session-summary');
    check('session: the summary reports hunk progress', /hunks reviewed/.test(summary), summary);

    /* 3 -- selecting a.txt renders its rows side by side, with a paired
       change row (the fixture's one-word edit collapses del+add into one
       row -- see render.pairBlock) */
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    // .bg-add on the new side only happens for an "add" or "change" row;
    // this fixture's only such row is the paired change (its one-word edit).
    const changeCell = page.locator('#diff-rows [data-side="new"].code.bg-add').first();
    await changeCell.waitFor();
    const anchor = await changeCell.evaluate((el) => ({ ...el.dataset }));
    check('rows: a changed line carries file/side/line/hunk/position',
      !!anchor.file && anchor.side === 'new' && !!anchor.line && !!anchor.hunk && !!anchor.position,
      JSON.stringify(anchor));

    /* 4 -- the DOM's anchors are not just present, they are exactly what
       the server rendered for this file: compare a few cells against
       GET /api/file/{id}/rows directly. */
    const aFileId = anchor.file;
    const apiRows = await page.evaluate((id) => fetch(`/api/file/${id}/rows`).then((r) => r.json()), aFileId);
    const changeRow = apiRows.find((r) => r.kind === 'change');
    check('rows: a change row exists in the API response', !!changeRow, JSON.stringify(apiRows));
    if (changeRow) {
      // .bg-del/.bg-add narrow to the change row's own cells: every cell in
      // the hunk shares the same data-hunk, but only the change row's old
      // side is tinted bg-del and its new side bg-add.
      const oldCell = await page.locator(`#diff-rows [data-hunk="${changeRow.hunk_id}"][data-side="old"].code.bg-del`).first().evaluate((el) => ({ ...el.dataset }));
      const newCell = await page.locator(`#diff-rows [data-hunk="${changeRow.hunk_id}"][data-side="new"].code.bg-add`).first().evaluate((el) => ({ ...el.dataset }));
      check('rows: DOM old-side anchor matches the API row exactly',
        Number(oldCell.line) === changeRow.old_no && Number(oldCell.position) === changeRow.old_position,
        `DOM=${JSON.stringify(oldCell)} API old_no/old_position=${changeRow.old_no}/${changeRow.old_position}`);
      check('rows: DOM new-side anchor matches the API row exactly',
        Number(newCell.line) === changeRow.new_no && Number(newCell.position) === changeRow.new_position,
        `DOM=${JSON.stringify(newCell)} API new_no/new_position=${changeRow.new_no}/${changeRow.new_position}`);
    }

    /* 5 -- j/k move the file selection */
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

    /* 6 -- n/p jump between hunks, independent of which file (big.js, with
       ~100 scattered single-line hunks, is the file that actually exercises
       this -- a.txt only has one). */
    await selectFileAndMeasure(page, 'big.js');
    const diffScrollTop = () => page.evaluate(() => document.getElementById('diff').scrollTop);
    const beforeN = await diffScrollTop();
    await page.keyboard.press('n');
    const afterN1 = await diffScrollTop();
    check('keyboard: n jumps forward to a hunk', afterN1 > beforeN, `${beforeN} -> ${afterN1}`);

    await page.keyboard.press('n');
    const afterN2 = await diffScrollTop();
    check('keyboard: n again jumps further forward', afterN2 > afterN1, `${afterN1} -> ${afterN2}`);

    await page.keyboard.press('p');
    const afterP = await diffScrollTop();
    check('keyboard: p jumps back to the previous hunk', afterP === afterN1, `${afterN2} -> ${afterP}, want ${afterN1}`);

    /* 7 -- marking a hunk reviewed updates the file queue's badge */
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const hunkId = await page.getAttribute('#diff-rows [data-hunk]', 'data-hunk');
    await page.evaluate((hid) => fetch('/api/state', {
      method: 'POST',
      body: JSON.stringify({ hunk_id: hid, reviewed: true }),
    }), hunkId);
    await page.reload();
    await page.waitForSelector('.file-row');
    const badgeCount = await page.locator('.file-row .reviewed-badge').count();
    check('review state: a fully reviewed file shows a badge after reload', badgeCount >= 1, String(badgeCount));

    /* 8 -- the API behind the UI responds */
    const health = await page.evaluate(() => fetch('/api/health').then((r) => r.json()));
    check('api: /api/health reports ok', health.ok === true, JSON.stringify(health));

    /* 9 -- performance budget: first paint of the 10k-line file */
    const bigPaintMs = await selectFileAndMeasure(page, 'big.js');
    console.log(`big.js select -> first paint: ${bigPaintMs}ms (budget ${PAINT_BUDGET_MS}ms)`);
    check('performance: 10k-line file first paint is under budget', bigPaintMs < PAINT_BUDGET_MS, `${bigPaintMs}ms`);
    await page.waitForSelector('#diff-rows [data-hunk]');

    /* 10 -- performance budget: switching between two already-opened,
       normal-sized files is near instant (the render cache hit) */
    await selectFileAndMeasure(page, 'b.txt');
    const switchMs = await selectFileAndMeasure(page, 'a.txt');
    console.log(`a.txt <-> b.txt switch: ${switchMs}ms (budget ${SWITCH_BUDGET_MS}ms)`);
    check('performance: file switch between two normal files is under budget', switchMs < SWITCH_BUDGET_MS, `${switchMs}ms`);

    /* 11 -- performance budget: scrolling the 10k-line file top to bottom
       produces no long task over the budget */
    await selectFileAndMeasure(page, 'big.js');
    await page.evaluate(() => {
      window.__longTasks = [];
      const obs = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) window.__longTasks.push(entry.duration);
      });
      obs.observe({ entryTypes: ['longtask'] });
      window.__longTaskObserver = obs;
    });
    await page.evaluate(() => new Promise((resolve) => {
      const diff = document.getElementById('diff');
      const maxScroll = diff.scrollHeight - diff.clientHeight;
      const steps = 80;
      let i = 0;
      function step() {
        diff.scrollTop = Math.round((maxScroll * i) / steps);
        i++;
        if (i <= steps) {
          requestAnimationFrame(step);
        } else {
          setTimeout(resolve, 150); // let the last scheduled render settle
        }
      }
      requestAnimationFrame(step);
    }));
    const longTasks = await page.evaluate(() => {
      window.__longTaskObserver.disconnect();
      return window.__longTasks;
    });
    const worstLongTask = longTasks.length ? Math.max(...longTasks) : 0;
    console.log(`scrolling big.js: ${longTasks.length} long task(s), worst ${worstLongTask.toFixed(1)}ms (budget ${LONGTASK_BUDGET_MS}ms)`);
    check('performance: no long task over budget while scrolling the 10k-line file',
      longTasks.every((d) => d <= LONGTASK_BUDGET_MS), JSON.stringify(longTasks));
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
