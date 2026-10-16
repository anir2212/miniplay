import Phaser from 'phaser';
import { emit } from './bridge';
import { replay, targets, RUN_MS, type GameId, type Tap } from './logic';
import './style.css';

type Started = { token: string; seed: string; runId: string };
type Score = { score: number; user: string; game: GameId };
const byId = <T extends HTMLElement>(id: string): T => document.getElementById(id) as T;
const userInput = byId<HTMLInputElement>('user');
const gameInput = byId<HTMLSelectElement>('game');
const startButton = byId<HTMLButtonElement>('start');
const status = byId<HTMLElement>('status');
const board = byId<HTMLOListElement>('leaderboard');
let apiBase = '';
let active: { game: GameId; started: Started; at: number; taps: Tap[]; finishing: boolean } | null = null;
let live: WebSocket | null = null;
let liveGeneration = 0;

window.MiniPlayHost = {
  configure(config) {
    if (/^[A-Za-z0-9_-]{1,40}$/.test(config.userId)) { userInput.value = config.userId; userInput.readOnly = true; }
    if (config.apiBase && /^https?:\/\//.test(config.apiBase)) apiBase = config.apiBase.replace(/\/$/, '');
    void refresh();
    connectLive();
  },
};

async function request<T>(path: string, data?: object): Promise<T> {
  const response = await fetch(`${apiBase}${path}`, data ? { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(data) } : undefined);
  const result = await response.json() as T & { error?: string };
  if (!response.ok) throw new Error(result.error || `HTTP ${response.status}`);
  return result;
}

class GameScene extends Phaser.Scene {
  private target!: Phaser.GameObjects.Graphics;
  private header!: Phaser.GameObjects.Text;
  private hint!: Phaser.GameObjects.Text;

  constructor() { super('MiniPlay'); }
  create(): void {
    this.cameras.main.setBackgroundColor('#14233d');
    this.target = this.add.graphics();
    this.header = this.add.text(180, 55, 'Pulse Field', { fontSize: '27px', color: '#ffffff' }).setOrigin(0.5);
    this.hint = this.add.text(180, 480, 'Tap the center as the pulse appears', { fontSize: '16px', color: '#b6cae0', align: 'center', wordWrap: { width: 320 } }).setOrigin(0.5);
    this.input.on('pointerdown', (pointer: Phaser.Input.Pointer) => {
      if (!active || active.finishing) return;
      const ms = Math.round(performance.now() - active.at);
      if (ms > RUN_MS + 250) return;
      active.taps.push({ ms, x: Math.round(pointer.x / 360 * 1000), y: Math.round(pointer.y / 520 * 1000) });
      if (active.taps.length % 3 === 0) emit({ type: 'miniplay', version: 1, event: 'progress', game: active.game, taps: active.taps.length });
    });
  }
  update(): void {
    this.target.clear();
    if (!active) {
      this.drawCircle(180, 260, 70, 0x526381, 0.6);
      return;
    }
    const elapsed = performance.now() - active.at;
    const game = active.game;
    this.header.setText(game === 'rhythm' ? 'Pulse Field' : 'Comet Catch');
    this.hint.setText(game === 'rhythm' ? 'Tap the center when it glows' : 'Catch the glowing comet');
    const nearest = targets(game, active.started.seed).find(target => Math.abs(target.ms - elapsed) <= (game === 'rhythm' ? 125 : 210));
    if (nearest) this.drawCircle(nearest.x * 0.36, nearest.y * 0.52, game === 'rhythm' ? 74 : 38, game === 'rhythm' ? 0xffd36b : 0x7de9d5, 1);
    else if (game === 'rhythm') this.drawCircle(180, 260, 70, 0x526381, 0.6);
    if (elapsed >= RUN_MS && !active.finishing) void finish();
  }
  private drawCircle(x: number, y: number, radius: number, color: number, alpha: number): void {
    this.target.fillStyle(color, alpha).fillCircle(x, y, radius).lineStyle(3, 0xffffff, 0.4).strokeCircle(x, y, radius);
  }
}

new Phaser.Game({ type: Phaser.AUTO, parent: 'canvas', width: 360, height: 520, backgroundColor: '#14233d', scale: { mode: Phaser.Scale.FIT, autoCenter: Phaser.Scale.CENTER_BOTH }, scene: GameScene, render: { antialias: true } });

async function refresh(): Promise<void> {
  try {
    const game = gameInput.value as GameId;
    const result = await request<{ leaders: Array<{ user: string; score: number }> }>(`/api/leaderboards?game=${game}&period=all&user=${encodeURIComponent(userInput.value)}`);
    renderBoard(result.leaders);
  } catch (error) { status.textContent = `Leaderboard unavailable: ${(error as Error).message}`; }
}

function renderBoard(leaders: Array<{ user: string; score: number }>): void {
  board.replaceChildren(...leaders.map(row => { const li = document.createElement('li'); li.textContent = `${row.user}: ${row.score}`; return li; }));
}

function connectLive(): void {
  liveGeneration += 1;
  const generation = liveGeneration;
  live?.close();
  const game = gameInput.value as GameId;
  const url = new URL(`${apiBase}/api/boards/live?game=${game}&period=all&user=${encodeURIComponent(userInput.value)}`, location.href);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  live = new WebSocket(url);
  live.onmessage = event => {
    try {
      const message = JSON.parse(event.data) as { type: string; game: GameId; board: { leaders: Array<{ user: string; score: number }> } };
      if (generation === liveGeneration && message.type === 'board' && message.game === game && Array.isArray(message.board?.leaders)) renderBoard(message.board.leaders);
    } catch { /* Ignore malformed live updates; the REST refresh remains available. */ }
  };
  live.onclose = () => { if (generation === liveGeneration) setTimeout(() => { if (generation === liveGeneration) connectLive(); }, 2000); };
}

async function finish(): Promise<void> {
  const run = active;
  if (!run || run.finishing) return;
  run.finishing = true;
  const durationMs = Math.round(performance.now() - run.at);
  try {
    const score = replay(run.game, run.started.seed, run.taps);
    const result = await request<Score>('/api/scores', { token: run.started.token, events: run.taps, score, durationMs });
    status.textContent = `Finished ${run.game}: ${result.score} points`;
    emit({ type: 'miniplay', version: 1, event: 'finish', game: run.game, score: result.score });
  } catch (error) {
    status.textContent = `Score rejected: ${(error as Error).message}`;
    emit({ type: 'miniplay', version: 1, event: 'finish', game: run.game, error: (error as Error).message });
  } finally { active = null; startButton.disabled = false; void refresh(); }
}

startButton.onclick = async () => {
  if (active) return;
  try {
    const game = gameInput.value as GameId;
    const started = await request<Started>('/api/sessions', { user: userInput.value, game });
    active = { game, started, at: performance.now(), taps: [], finishing: false };
    startButton.disabled = true;
    status.textContent = 'Run started';
    emit({ type: 'miniplay', version: 1, event: 'start', game, runId: started.runId });
  } catch (error) { status.textContent = `Could not start: ${(error as Error).message}`; }
};
gameInput.onchange = () => { void refresh(); connectLive(); };
void refresh();
connectLive();
