import { describe, expect, it } from 'vitest';
import type { FlaggedRun, RunExplanation } from './api';
import { accountsUnderReview, appliesBy, evidenceRows, flagOrigin, reasonLabel, scoreArithmetic } from './logic';

const run = (user: string, flaggedAt: string): FlaggedRun => ({ runId: `${user}-${flaggedAt}`, user, game: 'rhythm', isolationScore: 0.6, zScore: 1, reasons: [], evidence: {}, flaggedAt, score: 10, baseScore: 10, multiplier: 1 });
const explanation = (changes: Partial<RunExplanation>): RunExplanation => ({ runId: 'r', user: 'alice', game: 'rhythm', score: 20, baseScore: 10, multiplier: 2, flagVersion: 2, durationMs: 10000, submittedAt: '2026-11-06T10:00:00Z', flagChange: null, ...changes });

describe('accountsUnderReview', () => {
  it('counts raised runs per account, most first, with the latest time', () => {
    expect(accountsUnderReview([run('bob', '2026-11-01T00:00:00Z'), run('alice', '2026-11-02T00:00:00Z'), run('alice', '2026-11-03T00:00:00Z')])).toEqual([
      { user: 'alice', runs: 2, latest: '2026-11-03T00:00:00Z' },
      { user: 'bob', runs: 1, latest: '2026-11-01T00:00:00Z' },
    ]);
  });
  it('is empty without runs', () => expect(accountsUnderReview([])).toEqual([]));
});

describe('evidenceRows', () => {
  it('gives each known measure a label and unit, in a fixed order', () => {
    expect(evidenceRows({ tapCount: 12, scoreFraction: 0.5, durationSeconds: 10.04 })).toEqual([
      { label: 'Share of maximum base score', value: '50%' },
      { label: 'Run length', value: '10.0 s' },
      { label: 'Taps', value: '12' },
    ]);
  });
  it('keeps evidence it has no label for', () => expect(evidenceRows({ newMeasure: 3 })).toEqual([{ label: 'newMeasure', value: '3' }]));
});

describe('wording', () => {
  it('describes reasons without claiming cheating', () => {
    for (const reason of ['isolation-forest outlier', 'high-score z-score']) expect(reasonLabel(reason)).not.toMatch(/cheat|bot|fraud/i);
    expect(reasonLabel('something new')).toBe('something new');
  });
  it('shows the arithmetic behind a score', () => expect(scoreArithmetic({ score: 30, baseScore: 15, multiplier: 2 })).toBe('15 x 2 = 30'));
  it('adds the staleness bound to the change time', () => expect(appliesBy('2026-11-06T10:00:00Z', 5).toISOString()).toBe('2026-11-06T10:00:05.000Z'));
});

describe('flagOrigin', () => {
  it('names the admin change behind a version', () => {
    const flagChange = { id: 1, actor: 'local-demo-admin', action: 'x', game: 'rhythm' as const, before: { doublePoints: false, version: 1 }, after: { doublePoints: true, version: 2 }, details: null, at: '2026-11-06T10:00:00Z' };
    expect(flagOrigin(explanation({ flagChange }))).toMatch(/version 2 was set to on by local-demo-admin/);
  });
  it('explains the initial version and rows older than versions', () => {
    expect(flagOrigin(explanation({ flagVersion: 1 }))).toMatch(/initial state/);
    expect(flagOrigin(explanation({ flagVersion: 0 }))).toMatch(/before flag versions existed/);
  });
  it('says so when the audit row is missing', () => expect(flagOrigin(explanation({ flagVersion: 9 }))).toMatch(/No audit row/));
});
