import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const TOKEN = 'a-long-local-demo-token-for-tests-only';

type Handler = (init: RequestInit | undefined) => { status: number; body: unknown };

function json(status: number, body: unknown) { return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }); }

const flag = (game: string, doublePoints: boolean, version: number) => ({ game, doublePoints, version, updatedAt: '2026-11-06T10:00:00Z' });

function backend() {
  const state = { rhythm: flag('rhythm', false, 1), reflex: flag('reflex', true, 4) };
  const routes: Record<string, Handler> = {
    'GET /api/admin/games': () => ({ status: 200, body: { games: [{ id: 'reflex', title: 'Comet Catch', flag: state.reflex }, { id: 'rhythm', title: 'Pulse Field', flag: state.rhythm }], flagMaxStalenessSeconds: 5 } }),
    'GET /api/admin/stats': () => ({ status: 200, body: { generatedAt: '2026-11-06T10:00:00Z', windowStart: '2026-11-05T10:00:00Z', outboxPending: 7, oldestPendingAt: '2026-11-06T09:00:00Z', aggregates: [],
      games: [{ game: 'rhythm', accepted: 12, rejected: 3, doubled: 2, players: 5, lastAcceptedAt: '2026-11-06T09:59:00Z' }, { game: 'reflex', accepted: 0, rejected: 0, doubled: 0, players: 0, lastAcceptedAt: null }] } }),
    'GET /api/admin/flagged-runs': () => ({ status: 200, body: { runs: [{ runId: 'run-flagged', user: 'alice', game: 'rhythm', isolationScore: 0.71, zScore: 3.4, reasons: ['high-score z-score'], flaggedAt: '2026-11-06T09:00:00Z', score: 300, baseScore: 150, multiplier: 2,
      evidence: { scoreFraction: 0.83, durationSeconds: 10, meanTimingErrorMs: 12.5, cadenceCV: 0.1, tapCount: 15 } }] } }),
    'GET /api/admin/audit': () => ({ status: 200, body: { entries: [{ id: 9, actor: 'local-demo-admin', action: 'feature_flag.double_points.set', game: 'reflex', before: { doublePoints: false, version: 3 }, after: { doublePoints: true, version: 4 }, details: {}, at: '2026-11-06T08:00:00Z' }] } }),
    'GET /api/admin/runs/run-flagged': () => ({ status: 200, body: { runId: 'run-flagged', user: 'alice', game: 'rhythm', score: 300, baseScore: 150, multiplier: 2, flagVersion: 4, durationMs: 10000, submittedAt: '2026-11-06T09:00:00Z',
      flagChange: { id: 9, actor: 'local-demo-admin', action: 'feature_flag.double_points.set', game: 'rhythm', before: { doublePoints: false, version: 3 }, after: { doublePoints: true, version: 4 }, details: {}, at: '2026-11-06T08:00:00Z' } } }),
    'GET /api/admin/runs/absent': () => ({ status: 404, body: { error: 'no accepted run with that ID' } }),
    'PUT /api/admin/games/rhythm/double-points': init => {
      const request = JSON.parse(String(init?.body)) as { doublePoints: boolean; expectedVersion: number };
      if (request.expectedVersion !== state.rhythm.version) return { status: 409, body: { error: 'flag changed since it was read', flag: state.rhythm } };
      state.rhythm = flag('rhythm', request.doublePoints, state.rhythm.version + 1);
      return { status: 200, body: { flag: state.rhythm, changed: true, flagMaxStalenessSeconds: 5 } };
    },
  };
  const calls: Array<{ key: string; authorization: string | null; body?: unknown }> = [];
  let acceptToken = true;
  const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${path}`;
    const authorization = new Headers(init?.headers).get('Authorization');
    calls.push({ key, authorization, body: init?.body });
    if (!acceptToken || authorization !== `Bearer ${TOKEN}`) return json(401, { error: 'admin token required' });
    const handler = routes[key];
    if (!handler) return json(404, { error: `no route ${key}` });
    const { status, body } = handler(init);
    return json(status, body);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { state, routes, calls, refuseFromNowOn: () => { acceptToken = false; } };
}

/** Node's own storage global can hide jsdom's, so each test installs a recording one. */
function recordingStorage() {
  const items = new Map<string, string>();
  return { get length() { return items.size; }, clear: () => items.clear(), getItem: (key: string) => items.get(key) ?? null, setItem: (key: string, value: string) => { items.set(key, value); }, removeItem: (key: string) => { items.delete(key); }, key: () => null };
}

async function signIn(user: ReturnType<typeof userEvent.setup>, value = TOKEN) {
  await user.type(screen.getByLabelText('Admin token'), value);
  await user.click(screen.getByRole('button', { name: 'Sign in' }));
}

describe('admin portal', () => {
  beforeEach(() => { vi.stubGlobal('localStorage', recordingStorage()); vi.stubGlobal('sessionStorage', recordingStorage()); });
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

  it('shows no data until the server accepts the token', async () => {
    const server = backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user, 'wrong-token');
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'The server refused that token.');
    expect(screen.queryByText('Games and events')).toBeNull();
    expect(screen.queryByText('alice')).toBeNull();
    expect(server.calls.every(call => call.authorization === 'Bearer wrong-token')).toBe(true);
  });

  it('sends the token on every request and never stores it', async () => {
    const server = backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    await screen.findByText('Games and events');
    expect(server.calls.length).toBeGreaterThanOrEqual(4);
    expect(server.calls.every(call => call.authorization === `Bearer ${TOKEN}`)).toBe(true);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(document.cookie).toBe('');
  });

  it('returns to sign-in when the server stops accepting the token', async () => {
    const server = backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    await screen.findByText('Games and events');
    server.refuseFromNowOn();
    await user.click(screen.getByRole('button', { name: 'Refresh now' }));
    await screen.findByLabelText('Admin token');
    expect(screen.queryByText('alice')).toBeNull();
  });

  it('toggles a flag with the version it read and reports when the change applies', async () => {
    const server = backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    await user.click(await screen.findByRole('button', { name: 'Turn double points on for Pulse Field' }));
    await screen.findByRole('button', { name: 'Turn double points off for Pulse Field' });
    const put = server.calls.find(call => call.key.startsWith('PUT'));
    expect(JSON.parse(String(put?.body))).toEqual({ doublePoints: true, expectedVersion: 1 });
    const status = screen.getByRole('status');
    expect(status.textContent).toMatch(/saved as flag version 2/);
    expect(status.textContent).toMatch(/may still use the previous state/);
  });

  it('reports a conflict, saves nothing and shows the current state', async () => {
    const server = backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    const button = await screen.findByRole('button', { name: 'Turn double points on for Pulse Field' });
    server.state.rhythm = flag('rhythm', true, 2); // another admin got there first
    await user.click(button);
    expect((await screen.findByRole('status')).textContent).toMatch(/nothing was saved/);
    await screen.findByRole('button', { name: 'Turn double points off for Pulse Field' });
    expect(server.state.rhythm.version).toBe(2);
  });

  it('labels where each live figure comes from', async () => {
    backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    const panel = within((await screen.findByText('Live stats')).closest('section') as HTMLElement);
    expect(panel.getByText('Direct from PostgreSQL')).toBeTruthy();
    expect(panel.getByText(/polling, not a stream/)).toBeTruthy();
    expect(panel.getByText(/only moves while the outbox publisher, Kafka and the stats worker are running/)).toBeTruthy();
    expect(panel.getByText(/No daily totals yet/)).toBeTruthy();
    expect(panel.getByRole('row', { name: /Pulse Field 12 3 2 5/ })).toBeTruthy();
    expect(panel.getByText('7')).toBeTruthy();
  });

  it('shows daily totals when the stats worker has written them', async () => {
    const server = backend();
    const live = server.routes['GET /api/admin/stats'](undefined).body as Record<string, unknown>;
    server.routes['GET /api/admin/stats'] = () => ({ status: 200, body: { ...live, aggregates: [{ game: 'rhythm', day: '2026-11-06T00:00:00Z', accepted: 40, rejected: 2, scoreSum: 2730, medianScore: 60 }] } });
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    const panel = within((await screen.findByText('Live stats')).closest('section') as HTMLElement);
    expect(panel.getByRole('row', { name: '2026-11-06 Pulse Field 40 2 2730 60' })).toBeTruthy();
    expect(panel.queryByText(/No daily totals yet/)).toBeNull();
  });

  it('shows raised runs with evidence as review signals, never as verdicts', async () => {
    backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    const section = (await screen.findByText('Runs raised for review')).closest('section') as HTMLElement;
    const panel = within(section);
    expect(panel.getByText('A review signal, not a verdict.')).toBeTruthy();
    expect(panel.getByText(/22 of 80 injected bots, with 1 false alarm among 200 clean runs/)).toBeTruthy();
    expect(panel.getByText('150 x 2 = 300')).toBeTruthy();
    expect(panel.getByText('83%')).toBeTruthy();
    expect(panel.getByText('12.5 ms')).toBeTruthy();
    expect(panel.getByText(/alice \(1\)/)).toBeTruthy();
    expect(section.textContent).not.toMatch(/cheater|cheated|caught|banned|guilty/i);
    expect(panel.queryByRole('button', { name: /ban|suspend|remove|delete/i })).toBeNull();
  });

  it('explains a score from the stored numbers and the audit row', async () => {
    backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    await user.click(await screen.findByRole('button', { name: 'Explain run run-flagged' }));
    const card = await screen.findByTestId('explanation');
    expect(card.textContent).toMatch(/150 x 2 = 300/);
    expect(card.textContent).toMatch(/version 4 was set to on by local-demo-admin/);
    await user.type(screen.getByLabelText('Run ID'), 'absent');
    await user.click(screen.getByRole('button', { name: 'Explain' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/no accepted run/);
  });

  it('shows the audit log with no way to change it', async () => {
    backend();
    const user = userEvent.setup();
    render(<App pollMs={0} />);
    await signIn(user);
    const panel = within((await screen.findByText('Audit log')).closest('section') as HTMLElement);
    expect(panel.getByRole('row', { name: /local-demo-admin feature_flag.double_points.set Comet Catch off \(v3\) on \(v4\)/ })).toBeTruthy();
    expect(panel.queryAllByRole('button')).toHaveLength(0);
  });

  it('polls while signed in and keeps the last figures through a failure', async () => {
    const server = backend();
    const user = userEvent.setup();
    render(<App pollMs={20} />);
    await signIn(user);
    await screen.findByText('Games and events');
    const before = server.calls.length;
    await waitFor(() => expect(server.calls.length).toBeGreaterThan(before + 4));
    server.routes['GET /api/admin/stats'] = () => ({ status: 503, body: { error: 'admin data unavailable' } });
    expect((await screen.findByRole('alert')).textContent).toMatch(/last good read/);
    expect(screen.getByText('Live stats')).toBeTruthy();
  });
});
