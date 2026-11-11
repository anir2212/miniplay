import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { replay, targets, type GameId, type Tap } from '../src/logic.ts';

const TOKEN = process.env.MINIPLAY_ADMIN_TOKEN ?? '';
const TTL_MS = Number(process.env.MINIPLAY_FLAG_TTL_SECONDS ?? 5) * 1000;
const RESULT = process.env.MINIPLAY_E2E_RESULT;
const SHOTS = process.env.MINIPLAY_E2E_SHOTS;
const sleep = (ms: number) => new Promise(done => setTimeout(done, ms));
const results: Record<string, unknown> = {};

type Run = { user: string; token: string; runId: string; seed: string; taps: Tap[]; base: number; issuedAt: number };

async function startRun(request: APIRequestContext, user: string, game: GameId = 'rhythm'): Promise<Run> {
  const response = await request.post('/api/sessions', { data: { user, game } });
  expect(response.status()).toBe(200);
  const started = await response.json() as { token: string; runId: string; seed: string };
  const taps = targets(game, started.seed).slice(0, 3).map(target => ({ ms: target.ms, x: target.x, y: target.y }));
  return { user, token: started.token, runId: started.runId, seed: started.seed, taps, base: replay(game, started.seed, taps), issuedAt: Date.now() };
}

async function submit(request: APIRequestContext, run: Run, claimed = run.base) {
  const wait = run.issuedAt + 9600 - Date.now(); // the server refuses a 10 s run that came back sooner
  if (wait > 0) await sleep(wait);
  const response = await request.post('/api/scores', { data: { token: run.token, events: run.taps, score: claimed, durationMs: 10000 } });
  return { status: response.status(), body: await response.json() as { score?: number; baseScore?: number; multiplier?: number; flagVersion?: number; error?: string } };
}

async function signIn(page: Page, token = TOKEN) {
  await page.goto('/admin');
  await page.getByLabel('Admin token').fill(token);
  await page.getByRole('button', { name: 'Sign in' }).click();
}

test.describe.configure({ mode: 'serial' });

test.afterAll(() => {
  if (RESULT) writeFileSync(RESULT, JSON.stringify(results, null, 2));
});

test('the admin API and page expose nothing without the token', async ({ page, request }) => {
  const routes = ['/api/admin/games', '/api/admin/stats', '/api/admin/flagged-runs', '/api/admin/audit', '/api/admin/runs/anything'];
  const statuses: Record<string, number[]> = {};
  for (const route of routes) {
    const missing = await request.get(route);
    const wrong = await request.get(route, { headers: { Authorization: 'Bearer not-the-token' } });
    statuses[route] = [missing.status(), wrong.status()];
    expect(statuses[route]).toEqual([401, 401]);
  }
  const put = await request.put('/api/admin/games/rhythm/double-points', { data: { doublePoints: true, expectedVersion: 1 } });
  expect(put.status()).toBe(401);
  results.unauthenticated = { routes: statuses, putWithoutToken: put.status() };

  await signIn(page, 'not-the-token');
  await expect(page.getByRole('alert')).toHaveText('The server refused that token.');
  await expect(page.getByText('Games and events')).toHaveCount(0);
  results.wrongTokenInBrowser = 'stayed on sign-in; "The server refused that token."';
});

test('a toggle changes accepted scores, follows the flag rule and is audited', async ({ page, request }) => {
  const policy: string[] = [];
  page.on('console', message => { if (/Content Security Policy/i.test(message.text())) policy.push(message.text()); });
  page.on('pageerror', error => policy.push(String(error)));
  await signIn(page);
  const pulse = page.locator('article.card', { hasText: 'Pulse Field' }).first();
  await expect(pulse).toContainText('Double points off');
  await expect(pulse).toContainText('Flag version 1');

  // Four runs start while the flag is off. Submitting browser-flag-off loads the service's cache.
  const beforeToggle = await startRun(request, 'browser-before-toggle');
  const insideBound = await startRun(request, 'browser-inside-bound');
  const loadsCache = await startRun(request, 'browser-flag-off');
  const claimsDoubled = await startRun(request, 'browser-claims-doubled');
  const off = await submit(request, loadsCache);
  expect(off.status).toBe(200);
  expect(off.body).toMatchObject({ score: loadsCache.base, baseScore: loadsCache.base, multiplier: 1, flagVersion: 1 });
  const cacheLoadedAt = Date.now();

  await page.getByRole('button', { name: 'Turn double points on for Pulse Field' }).click();
  await expect(pulse).toContainText('Double points on');
  await expect(pulse).toContainText('Flag version 2');
  await expect(page.getByRole('status')).toContainText('saved as flag version 2');
  const toggledAt = Date.now();

  // Inside the bound the service may still hold version 1, and the record says so.
  const stale = await submit(request, insideBound);
  const staleAfterMs = Date.now() - cacheLoadedAt;
  const staleAfterToggleMs = Date.now() - toggledAt;
  expect(staleAfterMs).toBeLessThan(TTL_MS);
  expect(stale.status).toBe(200);
  expect(stale.body).toMatchObject({ score: insideBound.base, multiplier: 1, flagVersion: 1 });

  // From the bound on, the new state applies, even to a run signed before the toggle.
  await sleep(Math.max(0, cacheLoadedAt + TTL_MS + 100 - Date.now()));
  const doubled = await submit(request, beforeToggle);
  expect(doubled.status).toBe(200);
  expect(doubled.body).toMatchObject({ score: beforeToggle.base * 2, baseScore: beforeToggle.base, multiplier: 2, flagVersion: 2 });

  // A resubmission does not apply the flag again.
  const resubmitted = await submit(request, beforeToggle);
  expect(resubmitted.status).toBe(409);

  // Replay still checks the base score: claiming the doubled number is a mismatch.
  const claimed = await submit(request, claimsDoubled, claimsDoubled.base * 2);
  expect(claimed.status).toBe(422);
  expect(claimed.body.error).toBe('score mismatch');

  results.flagRule = {
    flagOff: off.body,
    insideBound: { ...stale.body, msAfterCacheLoad: staleAfterMs, msAfterToggle: staleAfterToggleMs },
    afterBoundSignedBeforeToggle: doubled.body,
    resubmissionStatus: resubmitted.status,
    claimedDoubledScore: claimed,
  };

  // The portal explains the doubled score from the stored row and the audit row.
  await page.getByLabel('Run ID').fill(beforeToggle.runId);
  await page.getByRole('button', { name: 'Explain', exact: true }).click();
  const explanation = page.getByTestId('explanation');
  await expect(explanation).toContainText(`${beforeToggle.base} x 2 = ${beforeToggle.base * 2}`);
  await expect(explanation).toContainText('Flag version 2 was set to on by local-demo-admin');
  results.explanation = await explanation.innerText();

  // Turning it off applies after the bound, to a run that started while it was on.
  const startedWhileOn = await startRun(request, 'browser-started-while-on');
  await page.getByRole('button', { name: 'Turn double points off for Pulse Field' }).click();
  await expect(pulse).toContainText('Flag version 3');
  await sleep(TTL_MS + 100);
  const afterOff = await submit(request, startedWhileOn);
  expect(afterOff.body).toMatchObject({ score: startedWhileOn.base, multiplier: 1, flagVersion: 3 });
  results.signedWhileOnSubmittedAfterOff = afterOff.body;

  const audit = page.locator('section', { hasText: 'Audit log' });
  await page.getByRole('button', { name: 'Refresh now' }).click();
  await expect(audit.getByRole('row', { name: /off \(v1\) on \(v2\)/ })).toBeVisible();
  await expect(audit.getByRole('row', { name: /on \(v2\) off \(v3\)/ })).toBeVisible();
  await expect(audit.getByRole('button')).toHaveCount(0);
  const auditApi = await request.get('/api/admin/audit', { headers: { Authorization: `Bearer ${TOKEN}` } });
  results.auditApiDelete = (await request.delete('/api/admin/audit', { headers: { Authorization: `Bearer ${TOKEN}` } })).status();
  expect(results.auditApiDelete).toBe(405);
  results.auditEntries = ((await auditApi.json()) as { entries: unknown[] }).entries.length;

  // Live stats and their labels, from the real tables.
  const stats = page.locator('section', { hasText: 'Live stats' });
  await expect(stats.getByText('Direct from PostgreSQL')).toBeVisible();
  await expect(stats.getByRole('row', { name: /^Pulse Field/ })).toContainText('1'); // one doubled run
  results.statsRow = await stats.getByRole('row', { name: /^Pulse Field/ }).first().innerText();
  results.outboxLine = await stats.getByText('Events waiting in the outbox').innerText();
  results.aggregateRows = await stats.locator('table').nth(1).locator('tbody tr').count();

  // Runs raised by the real scanner, with evidence and review wording.
  const review = page.locator('section', { hasText: 'Runs raised for review' });
  await expect(review.getByText('A review signal, not a verdict.')).toBeVisible();
  const raised = review.locator('article.card');
  expect(await raised.count()).toBeGreaterThan(0);
  await expect(raised.first()).toContainText('Share of maximum base score');
  results.raisedRunsShown = await raised.count();
  results.firstRaisedRun = await raised.first().innerText();
  expect(await review.innerText()).not.toMatch(/cheater|cheated|banned|guilty/i);

  if (SHOTS) await page.screenshot({ path: join(SHOTS, 'portal_desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await sleep(200);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  results.horizontalOverflowAt390px = overflow;
  if (SHOTS) await page.screenshot({ path: join(SHOTS, 'portal_phone_width.png') });

  expect(policy).toEqual([]);
  results.contentSecurityPolicyOrPageErrors = policy.length;

  const headers = (await request.get('/admin')).headers();
  expect(headers['content-security-policy']).toContain("default-src 'self'");
  results.portalPageHeldNoData = !(await (await request.get('/admin')).text()).includes('synthetic');
});

test('the game page shows the multiplier the server applied', async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${TOKEN}` };
  const games = await (await request.get('/api/admin/games', { headers: auth })).json() as { games: Array<{ id: string; flag: { version: number } }> };
  const version = games.games.find(game => game.id === 'rhythm')!.flag.version;
  const on = await request.put('/api/admin/games/rhythm/double-points', { headers: auth, data: { doublePoints: true, expectedVersion: version } });
  expect(on.status()).toBe(200);
  await sleep(TTL_MS + 100);

  await page.goto('/');
  await page.locator('#user').fill('browser-player');
  await expect(page.locator('#canvas canvas')).toBeVisible();
  const box = (await page.locator('#canvas canvas').boundingBox())!;
  const session = page.waitForResponse('**/api/sessions');
  await page.getByRole('button', { name: 'Start' }).click();
  const started = await (await session).json() as { seed: string };
  const at = Date.now();
  let clicks = 0;
  for (const target of targets('rhythm', started.seed)) {
    const wait = at + target.ms - Date.now();
    if (wait > 0) await sleep(wait);
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    clicks++;
  }
  // Finishing refreshes the board, and that refresh must not overwrite the result.
  const status = page.getByRole('status');
  await expect(status).toContainText(/Finished rhythm: \d+ points/, { timeout: 15000 });
  const text = await status.innerText();
  const match = /Finished rhythm: (\d+) points \((\d+) x 2, double points\)/.exec(text);
  expect(match, text).not.toBeNull();
  expect(Number(match![1])).toBe(Number(match![2]) * 2);
  expect(Number(match![2])).toBeGreaterThan(0);
  results.realGameInBrowser = { clicks, status: text };

  const after = await (await request.get('/api/admin/games', { headers: auth })).json() as { games: Array<{ id: string; flag: { version: number } }> };
  const off = await request.put('/api/admin/games/rhythm/double-points', { headers: auth, data: { doublePoints: false, expectedVersion: after.games.find(game => game.id === 'rhythm')!.flag.version } });
  expect(off.status()).toBe(200);
});
