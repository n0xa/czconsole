// Wardrive module UI. Polls api/status; START/STOP drive the kismet capture.
const $ = (id) => document.getElementById(id);
const API = 'api';

function fmtUptime(s) {
  s = Math.floor(s || 0);
  const m = Math.floor(s / 60), ss = s % 60;
  return `${m}:${String(ss).padStart(2, '0')}`;
}

function cryptIcon(c) {
  if (!c || /open/i.test(c) || c === '') return '<span class="lock open">○</span>';
  return '<span class="lock">🔒</span>';
}

async function loadIfaces() {
  try {
    const d = await (await fetch(`${API}/ifaces`)).json();
    $('iface').innerHTML = (d.ifaces || []).map(i => `<option>${i}</option>`).join('')
      || '<option disabled>no wifi adapters</option>';
  } catch (e) {}
}

function setRunning(run, iface) {
  $('idle-controls').style.display = run ? 'none' : 'flex';
  $('run-controls').style.display = run ? 'flex' : 'none';
  $('feed-card').style.display = run ? 'block' : 'none';
  $('export-card').style.display = run ? 'flex' : 'none';
  $('badge').textContent = run ? '● RECORDING' : 'IDLE';
  $('badge').className = 'badge ' + (run ? 'run' : 'idle');
  if (run) $('iface-tag').textContent = iface || '';
}

async function poll() {
  let s;
  try { s = await (await fetch(`${API}/status`)).json(); }
  catch (e) { return; }

  setRunning(!!s.running, s.iface);
  if (!s.running) {
    $('timer').textContent = '';
    ['c-aps', 'c-cli', 'c-new'].forEach(id => $(id).textContent = '—');
    return;
  }
  // Capturing, but kismet's REST hasn't answered (box under load). The capture
  // is alive and still logging — say so rather than letting the stale counters
  // read as "dead".
  $('timer').textContent = `session ${fmtUptime(s.uptime_s)}`
    + (s.stats_ok === false ? ' · stats catching up…' : '');
  $('c-aps').textContent = s.aps ?? '—';
  $('c-cli').textContent = s.clients ?? '—';
  $('c-new').textContent = s.new_per_min ?? '—';

  $('feed').innerHTML = (s.feed || []).map(f => {
    const hidden = f.name === '<hidden>' ? ' hidden' : '';
    return `<div class="ssid"><span class="nm${hidden}">${f.name}</span>
      ${cryptIcon(f.crypt)}<span class="rssi">${f.sig || ''}</span></div>`;
  }).join('') || '<div class="ssid"><span class="nm dim">scanning…</span></div>';
}

$('start').addEventListener('click', async () => {
  const iface = $('iface').value;
  if (!iface) return;
  $('msg').className = 'msg'; $('msg').textContent = `starting kismet on ${iface}…`;
  try {
    const r = await fetch(`${API}/start?iface=${encodeURIComponent(iface)}`, { method: 'POST' });
    if (!r.ok) throw new Error(await r.text());
    $('msg').textContent = '';
    poll();
  } catch (e) { $('msg').className = 'msg err'; $('msg').textContent = 'start failed: ' + e; }
});

$('stop').addEventListener('click', async () => {
  $('msg').className = 'msg'; $('msg').textContent = 'stopping…';
  try {
    await fetch(`${API}/stop`, { method: 'POST' });
    $('msg').textContent = '';
    poll();
  } catch (e) { $('msg').className = 'msg err'; $('msg').textContent = 'stop failed: ' + e; }
});

// GPS pill mirrors the core dashboard's source.
async function gps() {
  try {
    const s = await (await fetch('/api/sysinfo')).json();
    const g = s.gps;
    $('gps').textContent = (g && g.ok) ? `GPS ${g.mode === 3 ? '3D' : '2D'}${g.sats ? ' · ' + g.sats : ''}` : 'GPS —';
    $('gps').className = (g && g.ok) ? 'pill gps' : 'pill';
  } catch (e) {}
}

loadIfaces(); poll(); gps();
setInterval(poll, 2000);
setInterval(gps, 2000);
