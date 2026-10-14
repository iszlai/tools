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

// goFnSource builds a Go file with one 23-line function whose body statement
// v10 is tenValue: 10 for the fixture's committed revision, 100 for its
// worktree-edited one. A default 3-line-context diff only shows a handful of
// lines around v10, leaving real, unchanged body on both sides for FR-8's
// "expand enclosing function" to splice in -- unlike a.txt's one-word edit,
// which is small enough that the whole file is already the hunk.
function goFnSource(tenValue) {
  const lines = ['package main', '', 'func Compute() int {'];
  for (let i = 0; i < 20; i++) lines.push(`\tv${i} := ${i === 10 ? tenValue : i}`);
  lines.push('\treturn v0', '}');
  return lines.join('\n') + '\n';
}

// buildRepo makes a repo with: a modified tracked file and a new untracked
// one (so the file queue has more than one entry and WorktreeDiff exercises
// both its tracked and untracked-synthesis paths), a 10k-line file with
// changes scattered through it for the performance budget below, and a small
// Go file whose one hunk sits in the middle of a function for FR-8.
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
  writeFileSync(path.join(dir, 'fn.go'), goFnSource(10));
  git(dir, env, 'add', 'a.txt', 'big.js', 'fn.go');
  git(dir, env, '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'init');

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline TWO\nline three\n');
  writeFileSync(path.join(dir, 'b.txt'), 'a brand new file\n');
  writeFileSync(path.join(dir, 'fn.go'), goFnSource(100));
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

// waitForCondition polls fn (which may itself be async, e.g. a fetch to the
// server) sequentially -- not page.waitForFunction's rAF-driven polling,
// which can fire overlapping evaluations of a slow async predicate and
// resolve on a stale one. Used below (FR-7) to wait on server-persisted
// state directly rather than guessing a fixed delay.
async function waitForCondition(fn, timeoutMs = 2000, intervalMs = 25) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await fn()) return true;
    if (Date.now() >= deadline) return false;
    await new Promise((r) => setTimeout(r, intervalMs));
  }
}

(async () => {
  const repoDir = buildRepo();
  const home = mkdtempSync(path.join(tmpdir(), 'flowreview-home-'));
  // let, not const: the FR-7 process-restart check below replaces both with
  // a freshly spawned process/URL over the same FLOWREVIEW_HOME.
  let { proc, url } = await serve(repoDir, home);
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
    check('files: the go function file is listed', paths.includes('fn.go'), paths.join(','));

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

    /* 12 -- FR-8: expanding a hunk's enclosing function splices extra
       context rows in without touching the hunk's own row anchors, and
       collapsing restores both the original rows and the scroll position. */
    await selectFileAndMeasure(page, 'fn.go');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const fnHunkId = await page.getAttribute('#diff-rows [data-hunk]', 'data-hunk');
    const changeCellSelector = `#diff-rows [data-hunk="${fnHunkId}"][data-side="new"].code.bg-add`;

    await page.evaluate(() => { document.getElementById('diff').scrollTop = 40; });
    const scrollBeforeExpand = await diffScrollTop();
    const rowCountBefore = await page.evaluate(() => state.rows.length);
    const anchorBefore = await page.locator(changeCellSelector).first().evaluate((el) => ({ ...el.dataset }));

    await page.locator(`.expand-btn[data-hunk="${fnHunkId}"]`).click();
    await page.waitForFunction((before) => state.rows.length > before, rowCountBefore, { timeout: 2000 });
    const rowCountExpanded = await page.evaluate(() => state.rows.length);
    check('expand: expanding a hunk adds context rows', rowCountExpanded > rowCountBefore,
      `${rowCountBefore} -> ${rowCountExpanded}`);

    const anchorExpanded = await page.locator(changeCellSelector).first().evaluate((el) => ({ ...el.dataset }));
    check('expand: the hunk\'s change-row anchor is unchanged after expanding',
      anchorExpanded.line === anchorBefore.line && anchorExpanded.position === anchorBefore.position &&
      anchorExpanded.hunk === anchorBefore.hunk,
      `before=${JSON.stringify(anchorBefore)} after=${JSON.stringify(anchorExpanded)}`);

    await page.locator(`.expand-btn[data-hunk="${fnHunkId}"]`).click();
    await page.waitForFunction((before) => state.rows.length === before, rowCountBefore, { timeout: 2000 });
    const scrollAfterCollapse = await diffScrollTop();
    check('expand: collapsing restores the original scroll position',
      scrollAfterCollapse === scrollBeforeExpand, `${scrollBeforeExpand} -> ${scrollAfterCollapse}`);

    const anchorCollapsed = await page.locator(changeCellSelector).first().evaluate((el) => ({ ...el.dataset }));
    check('expand: the hunk\'s change-row anchor is unchanged after collapsing',
      anchorCollapsed.line === anchorBefore.line && anchorCollapsed.position === anchorBefore.position &&
      anchorCollapsed.hunk === anchorBefore.hunk,
      `before=${JSON.stringify(anchorBefore)} collapsed=${JSON.stringify(anchorCollapsed)}`);
    /* ======================== FR-7: review queue, reviewed state, progress ======================== */

    /* 12 -- `r` toggles the current hunk (the one nearest above the top of
       the viewport) and both dims it and updates the header progress
       fraction. big.js has ~100 hunks, so marking its first one reviewed
       does not complete the file -- keeps this check independent of the
       completion/auto-advance behavior exercised in 13. */
    await selectFileAndMeasure(page, 'big.js'); // selectFile resets scrollTop to 0
    await page.waitForSelector('#diff-rows [data-hunk]');
    const countBefore12 = await page.textContent('#topbar-count');
    await page.keyboard.press('r');
    await page.waitForFunction(
      (before) => document.getElementById('topbar-count').textContent !== before,
      countBefore12, { timeout: 2000 },
    ).catch(() => {});
    const countAfter12 = await page.textContent('#topbar-count');
    check('FR-7: r toggles the current hunk and updates the header progress fraction',
      countAfter12 !== countBefore12, `${countBefore12} -> ${countAfter12}`);
    const dimmedCount = await page.locator('.hunk-header.hunk-reviewed').count();
    check('FR-7: a reviewed hunk header is visually dimmed', dimmedCount >= 1, String(dimmedCount));

    /* 13 -- `R` toggles every hunk of the whole file. b.txt is a one-hunk
       added file, so R both completes it (file-queue state flips to
       "reviewed") and triggers the completion flash + auto-advance to the
       next unreviewed file. */
    await selectFileAndMeasure(page, 'b.txt');
    await page.keyboard.press('R');
    await page.waitForFunction(() => {
      const el = document.querySelector('.file-row.selected');
      return el && el.classList.contains('reviewed');
    }, { timeout: 2000 }).catch(() => {});
    const bRowClass = await page.evaluate(() => {
      const rows = [...document.querySelectorAll('.file-row')];
      const b = rows.find((r) => r.querySelector('.path').textContent === 'b.txt');
      return b ? b.className : '';
    });
    check('FR-7: R marks every hunk of the file reviewed', /\breviewed\b/.test(bRowClass), bRowClass);

    const selectedPathBeforeAdvance = await page.locator('.file-row.selected .path').first().textContent();
    await page.waitForFunction(
      (prev) => {
        const el = document.querySelector('.file-row.selected .path');
        return el && el.textContent !== prev;
      },
      selectedPathBeforeAdvance,
      { timeout: 2000 },
    ).catch(() => {});
    const selectedPathAfterAdvance = await page.locator('.file-row.selected .path').first().textContent();
    check('FR-7: completing a file auto-advances to the next unreviewed one',
      selectedPathAfterAdvance !== selectedPathBeforeAdvance,
      `${selectedPathBeforeAdvance} -> ${selectedPathAfterAdvance}`);

    /* 14 -- `?` opens the keyboard-shortcut help overlay. */
    await page.keyboard.press('?');
    await page.waitForSelector('#help-overlay:not([hidden])');
    const helpText = await page.textContent('#help-overlay');
    check('FR-7: ? shows a help overlay listing the shortcuts',
      /hunk/.test(helpText) && /reviewed/.test(helpText), helpText);
    await page.keyboard.press('Escape');
    // Not waitForSelector: an element matching [hidden] is never "visible",
    // so that would wait for the state it is already in and time out.
    await page.waitForFunction(() => document.getElementById('help-overlay').hidden);

    /* 15 -- FR-7 accept: mark a hunk with r, reload the page AND restart the
       process (same FLOWREVIEW_HOME) -- state persists; progress counts
       in /api/session match what was marked.
       State checks below hit the server directly with node's own fetch,
       not page.evaluate: confirming persistence is exactly the server's
       job, independent of the browser, and page.waitForFunction's rAF-driven
       polling can overlap evaluations of a slow async predicate and resolve
       on a stale one (waitForCondition polls sequentially instead). */
    await selectFileAndMeasure(page, 'a.txt'); // single-hunk file, easy to reason about
    await page.waitForSelector('#diff-rows [data-hunk]');
    const aHunkId = await page.getAttribute('#diff-rows [data-hunk]', 'data-hunk');

    // Start from a known "unreviewed" state regardless of what earlier checks did.
    await fetch(`${url}api/state`, { method: 'POST', body: JSON.stringify({ hunk_id: aHunkId, reviewed: false }) });
    await waitForCondition(async () => {
      const s = await fetch(`${url}api/session`).then((r) => r.json());
      return !(s.hunk_states && s.hunk_states[aHunkId] === 'reviewed');
    });

    await page.reload();
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.keyboard.press('r'); // a.txt's one hunk is always "current" at scrollTop 0
    const marked = await waitForCondition(async () => {
      const s = await fetch(`${url}api/session`).then((r) => r.json());
      return !!(s.hunk_states && s.hunk_states[aHunkId] === 'reviewed');
    });
    check('FR-7 accept: r persists the hunk as reviewed', marked, 'timed out waiting for the server to see it reviewed');

    const sessBeforeRestart = await fetch(`${url}api/session`).then((r) => r.json());
    await page.reload(); // same process, reload the page
    await page.waitForSelector('.file-row');
    const sessAfterPageReload = await fetch(`${url}api/session`).then((r) => r.json());
    check('FR-7 accept: reviewed state survives a page reload',
      sessAfterPageReload.progress.reviewed_hunks === sessBeforeRestart.progress.reviewed_hunks && sessAfterPageReload.progress.reviewed_hunks > 0,
      `${JSON.stringify(sessBeforeRestart.progress)} -> ${JSON.stringify(sessAfterPageReload.progress)}`);

    proc.kill();
    await new Promise((resolve) => proc.once('exit', resolve));
    ({ proc, url } = await serve(repoDir, home)); // same FLOWREVIEW_HOME, a brand new process
    await page.goto(url);
    await page.waitForSelector('.file-row');
    const sessAfterRestart = await fetch(`${url}api/session`).then((r) => r.json());
    check('FR-7 accept: reviewed state survives a process restart (same FLOWREVIEW_HOME)',
      sessAfterRestart.progress.reviewed_hunks === sessBeforeRestart.progress.reviewed_hunks && sessAfterRestart.progress.reviewed_hunks > 0,
      `${JSON.stringify(sessBeforeRestart.progress)} -> ${JSON.stringify(sessAfterRestart.progress)}`);

    /* ======================== FR-9: comment drafts ======================== */

    /* 16 -- inline draft: click the line-number gutter to open a compose
       card, type, save; the card renders through mdlite. a.txt's single
       hunk changes its line 2 ("line two" -> "line TWO"). */
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno').click();
    await page.waitForSelector('#comment-new textarea');
    await page.fill('#comment-new textarea', 'needs a **look**');
    await page.click('#comment-new [data-action="save"]');
    await page.waitForSelector('#comment-list .comment-card');
    const draftBodyHTML = await page.locator('#comment-list .comment-card .body-view').first().innerHTML();
    check('FR-9: a saved inline draft renders through mdlite',
      /<strong>look<\/strong>/.test(draftBodyHTML), draftBodyHTML);
    const draftHasMarker = await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno .marker-dot.draft').count();
    check('FR-9: the line carrying a draft shows a gutter marker', draftHasMarker >= 1, String(draftHasMarker));

    /* 17 -- FR-9 accept: draft -> reload -> still there. */
    await page.reload();
    await page.waitForSelector('.file-row');
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#comment-list .comment-card');
    const afterReloadText = await page.textContent('#comment-list .comment-card .body-view');
    check('FR-9 accept: a draft survives a page reload', /look/.test(afterReloadText), afterReloadText);

    /* 18 -- FR-9 accept: stale draft flagged after a fixture refresh. Change
       the hunk the draft is anchored to (a second edit to the same line) and
       trigger POST /api/reload -- the endpoint this slice adds so a running
       server notices the working tree moved without a process restart --
       then confirm the comment shows the stale warning. */
    writeFileSync(path.join(repoDir, 'a.txt'), 'line one\nline TWO changed again\nline three\n');
    const reloadResp = await fetch(`${url}api/reload`, { method: 'POST' }).then((r) => r.json());
    check('FR-9: POST /api/reload reconciles against the changed fixture',
      reloadResp.progress.total_hunks >= 1, JSON.stringify(reloadResp.progress));
    await page.reload();
    await page.waitForSelector('.file-row');
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#comment-list .comment-card.stale');
    const staleWarning = await page.textContent('#comment-list .comment-card.stale .stale-warning');
    check('FR-9 accept: a stale draft is flagged with a warning', /changed/.test(staleWarning), staleWarning);

    /* 19 -- re-anchor: click Re-anchor, then click a line in the diff; the
       comment is re-upserted with a fresh anchor and loses its stale status
       (handleUpsertComment always resets to "draft" on any upsert). */
    await page.click('#comment-list .comment-card.stale [data-action="reanchor"]');
    await page.waitForSelector('.comment-card.reanchoring');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno').click();
    const unstaled = await waitForCondition(async () => {
      const s = await fetch(`${url}api/session`).then((r) => r.json());
      return s.comments.length === 1 && s.comments[0].status !== 'stale';
    });
    check('FR-9: re-anchoring a stale comment clears the stale flag', unstaled, 'timed out waiting for a non-stale status');

    /* 20 -- remote comments: stub GET /api/remote-comments (kept simple per
       the slice's own note -- no fake GitHub API needed) and confirm a
       non-outdated one renders inline as a gutter marker at its anchor,
       while an outdated one -- no live line left to anchor it to -- renders
       read-only in the side panel instead. */
    await page.route('**/api/remote-comments', (route) => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify([
        { id: 1, author: 'reviewer', body: 'looks fine to me', path: 'a.txt', line: 2, side: 'new', created_at: new Date().toISOString() },
        { id: 2, author: 'reviewer', body: 'this was about an old line', path: 'a.txt', line: 1, side: 'new', created_at: new Date().toISOString(), outdated: true },
      ]),
    }));
    await page.reload();
    await page.waitForSelector('.file-row');
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const remoteMarker = await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno .marker-dot.remote').count();
    check('FR-9: a non-outdated remote comment renders inline at its anchor', remoteMarker >= 1, String(remoteMarker));
    const outdatedCard = await page.locator('#remote-comment-list .remote-comment-card').count();
    check('FR-9: an outdated remote comment renders read-only in the side panel', outdatedCard >= 1, String(outdatedCard));
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
