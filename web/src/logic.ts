export type GameId = 'rhythm' | 'reflex';
export type Tap = { ms: number; x: number; y: number };
export type Target = { ms: number; x: number; y: number };
export const RUN_MS = 10000;

export function hashSeed(seed: string): number {
  let hash = 2166136261;
  for (let i = 0; i < seed.length; i++) hash = Math.imul(hash ^ seed.charCodeAt(i), 16777619);
  return hash >>> 0;
}

export function targets(game: GameId, seed: string): Target[] {
  let state = hashSeed(seed) || 1;
  const rand = (): number => { state ^= state << 13; state ^= state >>> 17; state ^= state << 5; return state >>> 0; };
  const count = game === 'rhythm' ? 18 : 13;
  const interval = game === 'rhythm' ? 500 : 700;
  return Array.from({ length: count }, (_, index) => {
    const jitter = rand() % (game === 'rhythm' ? 61 : 101);
    const x = game === 'rhythm' ? 500 : 150 + rand() % 701;
    const y = game === 'rhythm' ? 500 : 150 + rand() % 701;
    return { ms: 450 + index * interval + jitter, x, y };
  });
}

export function replay(game: GameId, seed: string, taps: Tap[]): number {
  const used = new Set<number>();
  let score = 0;
  for (const tap of taps) {
    if (!Number.isInteger(tap.ms) || tap.ms < 0 || tap.ms > RUN_MS + 250 || !Number.isInteger(tap.x) || !Number.isInteger(tap.y) || tap.x < 0 || tap.x > 1000 || tap.y < 0 || tap.y > 1000) throw new Error('invalid tap');
    if (game === 'rhythm' && Math.hypot(tap.x - 500, tap.y - 500) > 220) continue;
    const window = game === 'rhythm' ? 110 : 180;
    const radius = game === 'rhythm' ? 220 : 110;
    const index = targets(game, seed).findIndex((target, index) => !used.has(index) && Math.abs(target.ms - tap.ms) <= window && Math.hypot(target.x - tap.x, target.y - tap.y) <= radius);
    if (index >= 0) { used.add(index); score += game === 'rhythm' ? 10 : 15; }
  }
  return score;
}
