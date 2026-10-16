const $ = id => document.getElementById(id);
let run = null;
const bridge = event => { if (window.parent !== window) window.parent.postMessage({ type: 'miniplay', ...event }, '*'); };
async function refresh() {
  const game = $('game').value;
  const data = await (await fetch(`/api/leaderboard?game=${game}&user=${encodeURIComponent($('user').value)}`)).json();
  $('board').textContent = data.leaders.map((r,i) => `${i+1}. ${r.user}: ${r.score}`).join('\n') || 'No scores yet';
}
$('start').onclick = async () => {
  try {
    const game = $('game').value;
    const data = await (await fetch('/api/start', { method:'POST', headers:{'content-type':'application/json'}, body:JSON.stringify({user:$('user').value,game}) })).json();
    if (!data.token) throw new Error(data.error);
    run = { ...data, game, events:[], started:performance.now() };
    $('start').disabled = true; $('target').disabled = false; $('status').textContent = 'Watch for the flash';
    bridge({ event:'start', game });
    const interval = game === 'rhythm' ? 500 : 900;
    const timer = setInterval(() => {
      $('target').classList.add('active');
      setTimeout(() => $('target').classList.remove('active'), game === 'rhythm' ? 120 : 170);
    }, interval);
    setTimeout(async () => {
      clearInterval(timer); $('target').disabled = true; $('start').disabled = false;
      const durationMs = Math.round(performance.now() - run.started);
      const tolerance = game === 'rhythm' ? 120 : 170;
      const hits = new Set(run.events.map(e => ({beat: Math.round(e.ms/interval), ms:e.ms})).filter(e => e.beat > 0 && e.beat <= 20 && Math.abs(e.ms - e.beat*interval) <= tolerance).map(e => e.beat));
      const score = hits.size * (game === 'rhythm' ? 10 : 15);
      const response = await (await fetch('/api/submit', { method:'POST', headers:{'content-type':'application/json'}, body:JSON.stringify({token:run.token, events:run.events, score, durationMs}) })).json();
      $('status').textContent = response.error || `Score: ${response.score}`;
      bridge({ event:'finish', game, score:response.score, error:response.error });
      run = null; refresh();
    }, 10000);
  } catch (e) { $('status').textContent = e.message; $('start').disabled = false; }
};
$('target').onclick = () => {
  if (!run) return;
  const ms = Math.round(performance.now() - run.started);
  run.events.push({ ms });
  bridge({ event:'progress', game:run.game, taps:run.events.length });
};
$('game').onchange = refresh;
refresh();
