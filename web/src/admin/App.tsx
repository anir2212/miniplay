import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import { adminApi, ApiError, type AdminApi, type AuditEntry, type FlaggedRun, type Game, type LiveStats, type RunExplanation } from './api';
import { accountsUnderReview, appliesBy, evidenceRows, flagOrigin, formatTime, GAME_TITLES, reasonLabel, scoreArithmetic } from './logic';

type Snapshot = { games: Game[]; maxStaleness: number; stats: LiveStats; runs: FlaggedRun[]; audit: AuditEntry[]; readAt: Date };

async function loadSnapshot(api: AdminApi): Promise<Snapshot> {
  const [games, stats, flagged, audit] = await Promise.all([api.games(), api.stats(), api.flaggedRuns(), api.audit()]);
  return { games: games.games, maxStaleness: games.flagMaxStalenessSeconds, stats, runs: flagged.runs, audit: audit.entries, readAt: new Date() };
}

function SignIn({ onSignIn }: { onSignIn: (token: string, snapshot: Snapshot) => void }) {
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      onSignIn(token, await loadSnapshot(adminApi(token)));
    } catch (failure) {
      const status = failure instanceof ApiError ? failure.status : 0;
      if (status === 401) setError('The server refused that token.');
      else if (status === 503) setError('The admin service is not configured or its database is unavailable.');
      else setError('Could not reach the admin service.');
    } finally { setBusy(false); }
  }
  return (
    <main className="signin">
      <h1>MiniPlay admin</h1>
      <p>Enter the admin token. The server checks it on every request. This page keeps it in memory only, so reloading signs you out.</p>
      <form onSubmit={event => void submit(event)}>
        <label htmlFor="token">Admin token</label>
        <input id="token" type="password" autoComplete="off" value={token} onChange={event => setToken(event.target.value)} required />
        <button type="submit" disabled={busy || !token}>{busy ? 'Checking' : 'Sign in'}</button>
      </form>
      {error && <p className="notice notice-error" role="alert">{error}</p>}
      <p className="muted">Local demo credential: one shared token from <code>MINIPLAY_ADMIN_TOKEN</code>, not per-person accounts.</p>
    </main>
  );
}

function GamesPanel({ games, maxStaleness, api, onChanged }: { games: Game[]; maxStaleness: number; api: AdminApi; onChanged: () => Promise<void> }) {
  const [busy, setBusy] = useState('');
  const [message, setMessage] = useState<{ text: string; error: boolean } | null>(null);
  async function toggle(game: Game) {
    const next = !game.flag.doublePoints;
    setBusy(game.id);
    setMessage(null);
    try {
      const result = await api.setDoublePoints(game.id, next, game.flag.version);
      setMessage({
        error: false,
        text: result.changed
          ? `${game.title}: double points ${next ? 'on' : 'off'}, saved as flag version ${result.flag.version}. Every submission from ${formatTime(appliesBy(result.flag.updatedAt, result.flagMaxStalenessSeconds))} uses it; until then the score service may still use the previous state.`
          : `${game.title}: nothing changed.`,
      });
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) setMessage({ error: true, text: `${game.title}: the flag changed since this page read it, so nothing was saved. The page now shows the current state.` });
      else if (failure instanceof ApiError && failure.status === 401) throw failure;
      else setMessage({ error: true, text: `${game.title}: not saved. ${(failure as Error).message}` });
    } finally {
      setBusy('');
      await onChanged();
    }
  }
  return (
    <section aria-labelledby="games-title">
      <h2 id="games-title">Games and events</h2>
      <p className="muted">Double points multiply the replay-validated base score by 2. The state the score service holds when it accepts a submission decides; the state when the run started does not. Its cached copy is at most {maxStaleness} s old.</p>
      <div className="cards">
        {games.map(game => (
          <article className="card" key={game.id}>
            <h3>{game.title}</h3>
            <p className={game.flag.doublePoints ? 'state state-on' : 'state'}>Double points {game.flag.doublePoints ? 'on' : 'off'}</p>
            <p className="muted">Flag version {game.flag.version}, changed {formatTime(game.flag.updatedAt)}</p>
            <button type="button" disabled={busy !== ''} onClick={() => void toggle(game)}>
              Turn double points {game.flag.doublePoints ? 'off' : 'on'} for {game.title}
            </button>
          </article>
        ))}
      </div>
      {message && <p className={message.error ? 'notice notice-error' : 'notice'} role="status">{message.text}</p>}
    </section>
  );
}

function StatsPanel({ stats }: { stats: LiveStats }) {
  return (
    <section aria-labelledby="stats-title">
      <h2 id="stats-title">Live stats</h2>
      <h3>Direct from PostgreSQL</h3>
      <p className="muted">Counted from the scores and rejections tables for the last 24 hours. This page re-reads them every 5 seconds; it is polling, not a stream. Read at {formatTime(stats.generatedAt)}.</p>
      <div className="table-wrap">
        <table>
          <thead><tr><th scope="col">Game</th><th scope="col">Accepted</th><th scope="col">Rejected</th><th scope="col">Accepted with double points</th><th scope="col">Players</th><th scope="col">Last accepted</th></tr></thead>
          <tbody>
            {stats.games.map(row => (
              <tr key={row.game}><th scope="row">{GAME_TITLES[row.game]}</th><td>{row.accepted}</td><td>{row.rejected}</td><td>{row.doubled}</td><td>{row.players}</td><td>{formatTime(row.lastAcceptedAt)}</td></tr>
            ))}
          </tbody>
        </table>
      </div>
      <p>Events waiting in the outbox: <strong>{stats.outboxPending}</strong>{stats.oldestPendingAt ? `, oldest from ${formatTime(stats.oldestPendingAt)}` : ''}.</p>
      <h3>Daily totals from the stats worker</h3>
      <p className="muted">Read from daily_stats, which only moves while the outbox publisher, Kafka and the stats worker are running, so it can lag the figures above. Score sums and medians include the double-points multiplier.</p>
      {stats.aggregates.length === 0
        ? <p>No daily totals yet. The stats worker has not processed any events.</p>
        : (
          <div className="table-wrap">
            <table>
              <thead><tr><th scope="col">Day (UTC)</th><th scope="col">Game</th><th scope="col">Accepted</th><th scope="col">Rejected</th><th scope="col">Score sum</th><th scope="col">Median score</th></tr></thead>
              <tbody>
                {stats.aggregates.map(row => (
                  <tr key={`${row.day}-${row.game}`}><td>{row.day.slice(0, 10)}</td><td>{GAME_TITLES[row.game]}</td><td>{row.accepted}</td><td>{row.rejected}</td><td>{row.scoreSum}</td><td>{row.medianScore}</td></tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
    </section>
  );
}

function ReviewPanel({ runs, onExplain }: { runs: FlaggedRun[]; onExplain: (runId: string) => void }) {
  const accounts = useMemo(() => accountsUnderReview(runs), [runs]);
  return (
    <section aria-labelledby="review-title">
      <h2 id="review-title">Runs raised for review</h2>
      <p className="notice">
        <strong>A review signal, not a verdict.</strong> An offline scan raised these runs because their numbers look unusual. A strong player can appear here, and a careful bot may not.
        On synthetic data it found 22 of 80 injected bots, with 1 false alarm among 200 clean runs. It has never been checked against real players. Nothing on this page changes an account or a score.
      </p>
      {runs.length === 0
        ? <p>No runs raised. The offline scan needs at least 30 accepted runs per game, and it only runs when someone starts it.</p>
        : (
          <>
            <p>Accounts with raised runs: {accounts.map(account => `${account.user} (${account.runs})`).join(', ')}.</p>
            <div className="review-list">
              {runs.map(run => (
                <article className="card" key={run.runId}>
                  <h3>{run.user} <span className="muted">{GAME_TITLES[run.game]}</span></h3>
                  <p className="muted">Raised {formatTime(run.flaggedAt)}</p>
                  <ul>{run.reasons.map(reason => <li key={reason}>{reasonLabel(reason)}</li>)}</ul>
                  <dl>
                    <dt>Score</dt><dd>{scoreArithmetic(run)}</dd>
                    <dt>Isolation score</dt><dd>{run.isolationScore.toFixed(3)}</dd>
                    <dt>Base-score z-score</dt><dd>{run.zScore.toFixed(2)}</dd>
                    {evidenceRows(run.evidence).map(row => <div key={row.label}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}
                  </dl>
                  <button type="button" className="link" onClick={() => onExplain(run.runId)}>Explain run {run.runId}</button>
                </article>
              ))}
            </div>
          </>
        )}
    </section>
  );
}

function ExplainPanel({ api, runId, setRunId }: { api: AdminApi; runId: string; setRunId: (value: string) => void }) {
  const [run, setRun] = useState<RunExplanation | null>(null);
  const [error, setError] = useState('');
  const explain = useCallback(async (id: string) => {
    setRun(null);
    setError('');
    try { setRun(await api.explain(id)); } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) throw failure;
      setError((failure as Error).message);
    }
  }, [api]);
  useEffect(() => { if (runId) void explain(runId); }, [runId, explain]);
  const [draft, setDraft] = useState('');
  return (
    <section aria-labelledby="explain-title">
      <h2 id="explain-title">Explain a score</h2>
      <form className="inline" onSubmit={event => { event.preventDefault(); if (draft === runId) void explain(draft); else setRunId(draft); }}>
        <label htmlFor="run-id">Run ID</label>
        <input id="run-id" value={draft} onChange={event => setDraft(event.target.value)} autoComplete="off" required />
        <button type="submit">Explain</button>
      </form>
      {error && <p className="notice notice-error" role="alert">{error}</p>}
      {run && (
        <div className="card" data-testid="explanation">
          <p className="score-line">{scoreArithmetic(run)}</p>
          <p>{run.user} on {GAME_TITLES[run.game]}, accepted {formatTime(run.submittedAt)}, run {run.runId}.</p>
          <p>{flagOrigin(run)}</p>
        </div>
      )}
    </section>
  );
}

function AuditPanel({ entries }: { entries: AuditEntry[] }) {
  const value = (state: { doublePoints: boolean; version: number }) => `${state.doublePoints ? 'on' : 'off'} (v${state.version})`;
  return (
    <section aria-labelledby="audit-title">
      <h2 id="audit-title">Audit log</h2>
      <p className="muted">Newest 50 entries. Read-only: the API has no way to edit or remove an entry, and a database trigger refuses both. The actor is the server's configured name for the one shared token, not a verified person.</p>
      {entries.length === 0
        ? <p>No admin changes yet.</p>
        : (
          <div className="table-wrap">
            <table>
              <thead><tr><th scope="col">When</th><th scope="col">Actor</th><th scope="col">Action</th><th scope="col">Game</th><th scope="col">Before</th><th scope="col">After</th></tr></thead>
              <tbody>
                {entries.map(entry => (
                  <tr key={entry.id}><td>{formatTime(entry.at)}</td><td>{entry.actor}</td><td><code>{entry.action}</code></td><td>{GAME_TITLES[entry.game] ?? entry.game}</td><td>{value(entry.before)}</td><td>{value(entry.after)}</td></tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
    </section>
  );
}

export function App({ pollMs = 5000 }: { pollMs?: number }) {
  const [session, setSession] = useState<{ token: string; snapshot: Snapshot } | null>(null);
  const [problem, setProblem] = useState('');
  const [explainId, setExplainId] = useState('');
  const token = session?.token ?? '';
  const api = useMemo(() => adminApi(token), [token]);

  const refresh = useCallback(async () => {
    try {
      const snapshot = await loadSnapshot(api);
      setSession(current => (current ? { ...current, snapshot } : current));
      setProblem('');
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) { setSession(null); return; }
      setProblem(`Could not refresh; these figures are from the last good read. ${(failure as Error).message}`);
    }
  }, [api]);

  useEffect(() => {
    if (!token || pollMs <= 0) return;
    const timer = setInterval(() => void refresh(), pollMs);
    return () => clearInterval(timer);
  }, [token, pollMs, refresh]);

  if (!session) return <SignIn onSignIn={(value, snapshot) => setSession({ token: value, snapshot })} />;
  const { snapshot } = session;
  return (
    <div className="page">
      <header>
        <div>
          <h1>MiniPlay admin</h1>
          <p className="muted">Local prototype with demo players. Last read {formatTime(snapshot.readAt)}.</p>
        </div>
        <div className="actions">
          <button type="button" onClick={() => void refresh()}>Refresh now</button>
          <button type="button" onClick={() => setSession(null)}>Sign out</button>
        </div>
      </header>
      {problem && <p className="notice notice-error" role="alert">{problem}</p>}
      <GamesPanel games={snapshot.games} maxStaleness={snapshot.maxStaleness} api={api} onChanged={refresh} />
      <StatsPanel stats={snapshot.stats} />
      <ReviewPanel runs={snapshot.runs} onExplain={setExplainId} />
      <ExplainPanel api={api} runId={explainId} setRunId={setExplainId} />
      <AuditPanel entries={snapshot.audit} />
    </div>
  );
}
