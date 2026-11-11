export type GameId = 'rhythm' | 'reflex';

export type Flag = { game: GameId; doublePoints: boolean; version: number; updatedAt: string };
export type Game = { id: GameId; title: string; flag: Flag };
export type GamesResponse = { games: Game[]; flagMaxStalenessSeconds: number };
export type FlagChangeResponse = { flag: Flag; changed: boolean; flagMaxStalenessSeconds: number };

export type GameLiveStats = { game: GameId; accepted: number; rejected: number; doubled: number; players: number; lastAcceptedAt: string | null };
export type DailyStat = { game: GameId; day: string; accepted: number; rejected: number; scoreSum: number; medianScore: number };
export type LiveStats = {
  generatedAt: string;
  windowStart: string;
  games: GameLiveStats[];
  outboxPending: number;
  oldestPendingAt: string | null;
  aggregates: DailyStat[];
};

export type FlaggedRun = {
  runId: string;
  user: string;
  game: GameId;
  isolationScore: number;
  zScore: number;
  reasons: string[];
  evidence: Record<string, number>;
  flaggedAt: string;
  score: number;
  baseScore: number;
  multiplier: number;
};

export type FlagAuditValue = { doublePoints: boolean; version: number };
export type AuditEntry = {
  id: number;
  actor: string;
  action: string;
  game: GameId;
  before: FlagAuditValue;
  after: FlagAuditValue;
  details: Record<string, string> | null;
  at: string;
};

export type RunExplanation = {
  runId: string;
  user: string;
  game: GameId;
  score: number;
  baseScore: number;
  multiplier: number;
  flagVersion: number;
  durationMs: number;
  submittedAt: string;
  flagChange: AuditEntry | null;
};

export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/**
 * The token lives only in the caller's memory and is sent on each request. The
 * server decides what it allows; nothing here checks the token's contents.
 */
export function adminApi(token: string) {
  async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(path, { ...init, headers: { ...init.headers, Authorization: `Bearer ${token}` } });
    const text = await response.text();
    let body: { error?: string } = {};
    try { body = text ? JSON.parse(text) : {}; } catch { /* A proxy error page is not JSON. */ }
    if (!response.ok) throw new ApiError(response.status, body.error || `HTTP ${response.status}`);
    return body as T;
  }
  return {
    games: () => call<GamesResponse>('/api/admin/games'),
    setDoublePoints: (game: GameId, doublePoints: boolean, expectedVersion: number) => call<FlagChangeResponse>(`/api/admin/games/${game}/double-points`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ doublePoints, expectedVersion }),
    }),
    stats: () => call<LiveStats>('/api/admin/stats'),
    flaggedRuns: () => call<{ runs: FlaggedRun[] }>('/api/admin/flagged-runs'),
    audit: () => call<{ entries: AuditEntry[] }>('/api/admin/audit'),
    explain: (runId: string) => call<RunExplanation>(`/api/admin/runs/${encodeURIComponent(runId)}`),
  };
}

export type AdminApi = ReturnType<typeof adminApi>;
