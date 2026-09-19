// Real browser regression test for the expanded-trace error index, run against
// the live MetaTube Admin on Kraken. Covers the specification's acceptance
// cases: one failed provider in an otherwise successful run, multiple distinct
// failures, a zero-error run, summary-to-event focus, collapse/reopen, refresh
// while open, auto-expansion, and both the video and actor tabs.
const { chromium } = require('playwright-core');

const BASE = process.env.ADMIN_BASE || 'http://192.168.10.166:8080';
const CHROME = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const stamp = Date.now().toString(36);
const results = [];

function check(name, ok, extra) {
  results.push({ name, ok });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${extra ? `  [${String(extra).slice(0, 220)}]` : ''}`);
}

async function api(path, method = 'GET', body) {
  const response = await fetch(BASE + path, {
    method,
    headers: { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let json = null;
  try { json = await response.json(); } catch (e) { /* no body */ }
  return { status: response.status, json };
}

const created = [];
async function seedTrace(spec) {
  const started = await api('/admin/api/traces/start', 'POST', spec.start);
  if (started.status !== 200) throw new Error(`start ${spec.start.trace_id}: HTTP ${started.status}`);
  created.push(spec.start.trace_id);
  if (spec.events.length) {
    const posted = await api(`/admin/api/traces/${encodeURIComponent(spec.start.trace_id)}/events`, 'POST', { events: spec.events });
    if (posted.status !== 200) throw new Error(`events ${spec.start.trace_id}: HTTP ${posted.status}`);
  }
  const finished = await api(`/admin/api/traces/${encodeURIComponent(spec.start.trace_id)}/finish`, 'POST', spec.finish);
  if (finished.status !== 200) throw new Error(`finish ${spec.start.trace_id}: HTTP ${finished.status}`);
}

async function seedAll(filter) {
  const ids = {
    okTrace: `e2e-${stamp}-video-ok`,
    multiTrace: `e2e-${stamp}-video-multi`,
    cleanTrace: `e2e-${stamp}-video-clean`,
    actorTrace: `e2e-${stamp}-actor-err`,
  };

  await seedTrace({
    start: { trace_id: ids.okTrace, kind: 'video', operation: 'identify', query: `${filter} OK`, run_id: `e2e-run-${stamp}-1`, client_name: 'e2e-verifier' },
    events: [
      { component: 'provider', stage: 'provider_started', provider: 'JavLibrary', duration_ms: 12, message: 'attempt 1' },
      { component: 'provider', stage: 'provider_failed', provider: 'JavLibrary', level: 'error', http_status: 404, attempt: 2, duration_ms: 431, message: 'JavLibrary returned 404' },
      { component: 'provider', stage: 'provider_completed', provider: 'JavBus', duration_ms: 220, message: '1 result via fallback' },
      { component: 'metatube', stage: 'result_selected', duration_ms: 1, message: 'selected JavBus' },
    ],
    finish: { status: 'succeeded', selected_provider: 'JavBus', selected_provider_id: 'E2E-001', result_count: 1, error_code: 'provider_failed', error_message: 'JavLibrary returned 404' },
  });

  await seedTrace({
    start: { trace_id: ids.multiTrace, kind: 'video', operation: 'lookup', query: `${filter} MULTI`, run_id: `e2e-run-${stamp}-2`, client_name: 'e2e-verifier' },
    events: [
      { component: 'provider', stage: 'provider_failed', provider: 'JavBus', level: 'error', http_status: 429, attempt: 1, duration_ms: 88, message: 'JavBus rate limited' },
      { component: 'provider', stage: 'provider_timeout', provider: 'JavLibrary', level: 'error', duration_ms: 12000, message: 'JavLibrary deadline exceeded' },
    ],
    finish: { status: 'partial', result_count: 0 },
  });

  await seedTrace({
    start: { trace_id: ids.cleanTrace, kind: 'video', operation: 'lookup', query: `${filter} CLEAN`, run_id: `e2e-run-${stamp}-3`, client_name: 'e2e-verifier' },
    events: [
      { component: 'provider', stage: 'provider_completed', provider: 'JavBus', duration_ms: 9120, message: 'slow but successful' },
      { component: 'metatube', stage: 'result_selected', duration_ms: 2, message: 'selected JavBus' },
    ],
    finish: { status: 'succeeded', selected_provider: 'JavBus', selected_provider_id: 'E2E-002', result_count: 1 },
  });

  await seedTrace({
    start: { trace_id: ids.actorTrace, kind: 'actor', operation: 'identify', query: `${filter} ACTOR`, run_id: `e2e-run-${stamp}-4`, client_name: 'e2e-verifier' },
    events: [
      { component: 'provider', stage: 'provider_failed', provider: 'GFriends', level: 'error', http_status: 403, duration_ms: 305, message: 'GFriends blocked the request' },
      { component: 'provider', stage: 'provider_completed', provider: 'JavBus', duration_ms: 180, message: 'actor matched' },
    ],
    finish: { status: 'succeeded', selected_provider: 'JavBus', selected_provider_id: 'E2E-ACTOR', result_count: 1 },
  });

  return ids;
}

(async () => {
  const filter = `E2E-${stamp}`;
  const ids = await seedAll(filter);

  // The detail payload the drawer reads must keep carrying the error fields.
  const detail = await api(`/admin/api/traces/${encodeURIComponent(ids.okTrace)}`);
  const traceData = (detail.json && detail.json.data && detail.json.data.trace) || {};
  const events = traceData.events || [];
  const failed = events.find((event) => event.stage === 'provider_failed');
  check('trace API exposes the error fields the index reads',
    Boolean(failed) && failed.http_status === 404 && failed.provider === 'JavLibrary' &&
    typeof failed.sequence === 'number' && failed.attempt === 2 && failed.level === 'error',
    JSON.stringify(failed));

  const browser = await chromium.launch({ executablePath: CHROME, headless: true });
  const page = await browser.newPage({ viewport: { width: 1400, height: 1000 } });
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));
  await page.goto(`${BASE}/admin`, { waitUntil: 'domcontentloaded' });

  const openTrace = async (kind, traceId) => {
    const prefix = kind === 'actor' ? 'traceActor' : 'traceVideo';
    await page.click(`[data-tab="${prefix}"]`);
    await page.fill(`#${prefix}Q`, filter);
    await page.click(`#${prefix}Refresh`);
    await page.waitForSelector(`#${prefix}Rows tr[data-trace="${traceId}"]`, { timeout: 20000 });
    await page.click(`#${prefix}Rows tr[data-trace="${traceId}"]`);
    // Wait until the drawer actually shows this trace, not the previous one.
    await page.waitForFunction((args) => {
      const drawer = document.querySelector(args.sel);
      return Boolean(drawer && drawer.textContent.includes(args.id) && drawer.querySelector('[data-error-index]'));
    }, { sel: `#${prefix}Drawer`, id: traceId }, { timeout: 20000 });
    // The run tree loads asynchronously; wait for it so step assertions are real.
    await page.waitForSelector(`#${prefix}Drawer .run-tree, #${prefix}Drawer .run-head .note.error`, { timeout: 20000 }).catch(() => {});
    await page.waitForTimeout(400);
    return prefix;
  };

  // 1. One failed provider inside an otherwise successful run.
  let prefix = await openTrace('video', ids.okTrace);
  let index = await page.textContent(`#${prefix}Drawer [data-error-index]`);
  check('one failed provider is named with provider, stage, and HTTP status',
    /JavLibrary · provider_failed · HTTP 404/.test(index), index);
  check('the entry shows message, attempt, duration, and the reason',
    /JavLibrary returned 404/.test(index) && /attempt 2/.test(index) && /431/.test(index) && /level=error/.test(index), index);
  check('a successful run with a provider failure is not labelled a failed run',
    /run succeeded with provider error\(s\)/.test(index) && !/run failed/.test(index), index);
  const entries = await page.$$(`#${prefix}Drawer [data-error-entry]`);
  check('a summary and timeline duplicate of the same failure is listed once', entries.length === 1, `entries=${entries.length}`);
  const openSteps = await page.$$(`#${prefix}Drawer .step[open]`);
  const allSteps = await page.$$(`#${prefix}Drawer .step`);
  check('the failing step was expanded automatically in the run tree',
    openSteps.length >= 1, `openSteps=${openSteps.length} steps=${allSteps.length}`);

  // 2. Summary-to-event focus.
  await page.click(`#${prefix}Drawer [data-focus-error]`);
  await page.waitForTimeout(600);
  const focus = await page.evaluate((p) => {
    const active = document.activeElement;
    return {
      focusedIsEvent: Boolean(active && active.classList && active.classList.contains('timeline-event')),
      flashed: document.querySelectorAll(`#${p}Drawer .timeline-event.focus-flash`).length,
      stepFlashed: document.querySelectorAll(`#${p}Drawer .step.focus-flash`).length,
      entryHighlighted: document.querySelectorAll(`#${p}Drawer .error-entry[data-focused="true"]`).length,
      status: (document.querySelector(`#${p}StatusLine`) || {}).textContent || '',
    };
  }, prefix);
  check('focus moves to the exact timeline event', focus.focusedIsEvent && focus.flashed === 1, JSON.stringify(focus));
  check('the chosen error entry is highlighted', focus.entryHighlighted === 1, JSON.stringify(focus));
  await page.screenshot({ path: `/tmp/adminnext-e2e/focus-${stamp}.png` });

  // 3. Refresh while the drawer is open keeps the index.
  await page.click(`#${prefix}Refresh`);
  await page.waitForTimeout(1600);
  const afterRefresh = await page.$$(`#${prefix}Drawer [data-error-index]`);
  check('refreshing the list keeps the open drawer and its error index', afterRefresh.length === 1, `found=${afterRefresh.length}`);

  // 4. Collapse and reopen.
  const rowSelector = `#${prefix}Rows tr[data-trace="${ids.okTrace}"]`;
  await page.click(rowSelector);
  await page.waitForTimeout(400);
  const collapsed = await page.$$(`#${prefix}Drawer [data-error-index]`);
  check('clicking the expanded job collapses it', collapsed.length === 0, `found=${collapsed.length}`);
  await page.click(rowSelector);
  await page.waitForSelector(`#${prefix}Drawer [data-error-index]`, { timeout: 20000 });
  check('reopening the job restores the error index', true);

  // 5. Multiple distinct failures.
  prefix = await openTrace('video', ids.multiTrace);
  index = await page.textContent(`#${prefix}Drawer [data-error-index]`);
  const multiEntries = await page.$$(`#${prefix}Drawer [data-error-entry]`);
  check('multiple distinct failures are listed separately', multiEntries.length === 2, `entries=${multiEntries.length}`);
  check('distinct failures name both providers and statuses',
    /JavBus · provider_failed · HTTP 429/.test(index) && /JavLibrary · provider_timeout/.test(index), index);
  check('a partial run is labelled as partial', /run partial · failures recorded/.test(index), index);

  // 6. Zero-error run, including a slow but successful provider call.
  prefix = await openTrace('video', ids.cleanTrace);
  index = await page.textContent(`#${prefix}Drawer [data-error-index]`);
  const cleanEntries = await page.$$(`#${prefix}Drawer [data-error-entry]`);
  check('a slow but successful provider call is not an error', cleanEntries.length === 0, index);
  check('a zero-error run shows an explicit clean index',
    /0 errors/.test(index) && /no errors/.test(index) && /No failure recorded for this trace\./.test(index), index);

  // 7. The actor tab uses the same index.
  prefix = await openTrace('actor', ids.actorTrace);
  index = await page.textContent(`#${prefix}Drawer [data-error-index]`);
  check('the actor trace tab shows the same error index',
    /GFriends · provider_failed · HTTP 403/.test(index) && /1 error/.test(index), index);

  check('no JavaScript errors were raised by the page', pageErrors.length === 0, pageErrors.join(' | '));

  await browser.close();

  for (const traceId of created) {
    await api(`/admin/api/traces/${encodeURIComponent(traceId)}`, 'DELETE');
  }

  const failedChecks = results.filter((result) => !result.ok);
  console.log('' + (results.length - failedChecks.length) + '/' + results.length + ' checks passed');
  if (failedChecks.length) {
    console.log('failed checks:');
    for (const result of failedChecks) console.log(' - ' + result.name);
    process.exitCode = 1;
  }
})().catch((error) => {
  console.error('E2E_ERROR', error && error.stack ? error.stack : error);
  process.exitCode = 2;
});
