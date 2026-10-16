import type { GameId } from './logic';

export type GameEvent =
  | { type: 'miniplay'; version: 1; event: 'start'; game: GameId; runId: string }
  | { type: 'miniplay'; version: 1; event: 'progress'; game: GameId; taps: number }
  | { type: 'miniplay'; version: 1; event: 'finish'; game: GameId; score?: number; error?: string };

declare global {
  interface Window {
    MiniPlayBridge?: { postMessage(message: string): void };
    MiniPlayHost?: { configure(config: { userId: string; apiBase?: string }): void };
  }
}

export function emit(event: GameEvent): void {
  if (window.MiniPlayBridge) window.MiniPlayBridge.postMessage(JSON.stringify(event));
  else if (window.parent !== window) window.parent.postMessage(event, window.location.origin);
}

export function isGameEvent(value: unknown): value is GameEvent {
  if (!value || typeof value !== 'object') return false;
  const event = value as Partial<GameEvent>;
  return event.type === 'miniplay' && event.version === 1 && ['start', 'progress', 'finish'].includes(event.event || '') && ['rhythm', 'reflex'].includes(event.game || '');
}
