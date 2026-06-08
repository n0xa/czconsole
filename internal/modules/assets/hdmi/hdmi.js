// HDMI Desktop module UI. Polls api/status and drives lightdm via api/enable
// and api/disable. The light maps state→colour: green=running, grey=stopped,
// red=enabled-but-down, amber=unknown.
const $ = (id) => document.getElementById(id);
const API = 'api';

const VIEW = {
  running:  { cls: 'green', txt: 'RUNNING',  sub: 'HDMI desktop is up' },
  stopped:  { cls: 'grey',  txt: 'STOPPED',  sub: 'HDMI disabled' },
  degraded: { cls: 'red',   txt: 'NOT RUNNING', sub: 'enabled/down (failed)' },
  unknown:  { cls: 'amber', txt: 'UNKNOWN',  sub: 'could not read unit state' },
};

let busy = false;

function render(s) {
  const v = VIEW[s.state] || VIEW.unknown;
  $('light').className = 'light ' + v.cls;
  $('stxt').textContent = v.txt;
  $('sub').textContent = v.sub;
  // Disable the button that wouldn't change anything (or while an action runs).
  $('enable').disabled = busy || s.state === 'running';
  $('disable').disabled = busy || s.state === 'stopped';
}

async function poll() {
  try {
    const s = await (await fetch(`${API}/status`)).json();
    render(s);
  } catch (e) { /* leave last state; transient under load */ }
}

async function sysFree() {
  try {
    const s = await (await fetch('/api/sysinfo')).json();
    if (s && s.mem_avail_mb != null) $('ufree').textContent = `${s.mem_avail_mb} MB free`;
  } catch (e) {}
}

async function act(path, label) {
  if (busy) return;
  busy = true;
  $('msg').className = 'msg'; $('msg').textContent = `${label}…`;
  ['enable', 'disable'].forEach(id => $(id).disabled = true);
  try {
    const r = await fetch(`${API}/${path}`, { method: 'POST' });
    const body = await r.text();
    if (!r.ok) throw new Error(body);
    let warn = '';
    try { warn = (JSON.parse(body).warn) || ''; } catch (e) {}
    $('msg').textContent = warn || '';
    $('msg').className = warn ? 'msg err' : 'msg';
  } catch (e) {
    $('msg').className = 'msg err';
    $('msg').textContent = `${label} failed: ${e.message || e}`;
  } finally {
    busy = false;
    poll(); sysFree();
  }
}

$('enable').addEventListener('click', () => act('enable', 'starting HDMI desktop'));
$('disable').addEventListener('click', () => act('disable', 'stopping HDMI desktop'));

poll(); sysFree();
setInterval(poll, 3000);
setInterval(sysFree, 3000);
