import { test, expect } from '@playwright/test';

test('both original games start and emit a typed host event', async ({ page }) => {
  await page.addInitScript(() => {
    (window as unknown as { MiniPlayBridge: { postMessage: (value: string) => void }; bridgeEvents: string[] }).bridgeEvents = [];
    (window as unknown as { MiniPlayBridge: { postMessage: (value: string) => void }; bridgeEvents: string[] }).MiniPlayBridge = {
      postMessage(value) { (window as unknown as { bridgeEvents: string[] }).bridgeEvents.push(value); },
    };
  });
  await page.route('**/api/leaderboards**', route => route.fulfill({ json: { leaders: [] } }));
  await page.route('**/api/sessions', route => route.fulfill({ json: { token: 'test-token', seed: 'sample', runId: 'test-run' } }));
  await page.goto('/');
  await expect(page.locator('#canvas canvas')).toBeVisible();
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(page.getByRole('status')).toHaveText('Run started');
  await expect.poll(async () => page.evaluate(() => (window as unknown as { bridgeEvents: string[] }).bridgeEvents.map(JSON.parse))).toContainEqual({ type: 'miniplay', version: 1, event: 'start', game: 'rhythm', runId: 'test-run' });
  await page.reload();
  await page.locator('#game').selectOption('reflex');
  await page.getByRole('button', { name: 'Start' }).click();
  await expect.poll(async () => page.evaluate(() => (window as unknown as { bridgeEvents: string[] }).bridgeEvents.map(JSON.parse))).toContainEqual({ type: 'miniplay', version: 1, event: 'start', game: 'reflex', runId: 'test-run' });
});
