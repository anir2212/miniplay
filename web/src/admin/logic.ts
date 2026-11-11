import type { FlaggedRun, GameId, RunExplanation } from './api';

export const GAME_TITLES: Record<GameId, string> = { rhythm: 'Pulse Field', reflex: 'Comet Catch' };

const REASON_LABELS: Record<string, string> = {
  'isolation-forest outlier': 'Unusual mix of run features (isolation forest)',
  'high-score z-score': 'Base score far above this game\'s average (z-score of 3 or more)',
};

export function reasonLabel(reason: string): string {
  return REASON_LABELS[reason] ?? reason;
}

const EVIDENCE_LABELS: Array<[key: string, label: string, format: (value: number) => string]> = [
  ['scoreFraction', 'Share of maximum base score', value => `${Math.round(value * 100)}%`],
  ['durationSeconds', 'Run length', value => `${value.toFixed(1)} s`],
  ['meanTimingErrorMs', 'Mean timing error', value => `${value.toFixed(1)} ms`],
  ['cadenceCV', 'Tap rhythm variation (CV)', value => value.toFixed(2)],
  ['tapCount', 'Taps', value => String(value)],
];

/** Known evidence in a fixed order, then anything newer the scanner added. */
export function evidenceRows(evidence: Record<string, number>): Array<{ label: string; value: string }> {
  const known = new Set(EVIDENCE_LABELS.map(([key]) => key));
  const rows = EVIDENCE_LABELS.filter(([key]) => key in evidence).map(([key, label, format]) => ({ label, value: format(evidence[key]) }));
  for (const key of Object.keys(evidence).sort()) if (!known.has(key)) rows.push({ label: key, value: String(evidence[key]) });
  return rows;
}

export type AccountSummary = { user: string; runs: number; latest: string };

/** Accounts with at least one raised run, most runs first, then name. */
export function accountsUnderReview(runs: FlaggedRun[]): AccountSummary[] {
  const accounts = new Map<string, AccountSummary>();
  for (const run of runs) {
    const current = accounts.get(run.user);
    if (!current) accounts.set(run.user, { user: run.user, runs: 1, latest: run.flaggedAt });
    else accounts.set(run.user, { user: run.user, runs: current.runs + 1, latest: run.flaggedAt > current.latest ? run.flaggedAt : current.latest });
  }
  return [...accounts.values()].sort((a, b) => b.runs - a.runs || a.user.localeCompare(b.user));
}

export function scoreArithmetic(row: { score: number; baseScore: number; multiplier: number }): string {
  return `${row.baseScore} x ${row.multiplier} = ${row.score}`;
}

/** The time by which the score service must be using a change. */
export function appliesBy(updatedAt: string, maxStalenessSeconds: number): Date {
  return new Date(new Date(updatedAt).getTime() + maxStalenessSeconds * 1000);
}

/** How the recorded flag version came about, in words. */
export function flagOrigin(run: RunExplanation): string {
  if (run.flagChange) {
    return `Flag version ${run.flagVersion} was set to ${run.flagChange.after.doublePoints ? 'on' : 'off'} by ${run.flagChange.actor} at ${formatTime(run.flagChange.at)}.`;
  }
  if (run.flagVersion === 0) return 'Stored before flag versions existed, so it counts as multiplier 1.';
  if (run.flagVersion === 1) return 'Flag version 1 is the initial state: off, with no admin change.';
  return `No audit row records flag version ${run.flagVersion}. That should not happen; check the audit table.`;
}

export function formatTime(value: string | Date | null): string {
  if (!value) return 'none';
  const date = value instanceof Date ? value : new Date(value);
  return date.toLocaleString('en-SG', { dateStyle: 'medium', timeStyle: 'medium' });
}
