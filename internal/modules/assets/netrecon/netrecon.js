// Net Recon (nmap) module UI. A free-form options field drives the shared
// nmap.Core; the most recent parsed results render below — the same two shapes
// the LCD shows (single-host port table / multi-host list).
const $ = id => document.getElementById(id);
const API = 'api';

function setMode(running) {
  $('form').style.display = running ? 'none' : 'block';
  $('run').style.display = running ? 'block' : 'none';
  $('badge').textContent = running ? '● SCANNING' : 'IDLE';
  $('badge').className = 'badge ' + (running ? 'run' : 'idle');
}
function setMsg(t, err) {
  const e = $('msg'); e.textContent = t; e.className = 'msg' + (err ? ' err' : '');
}

// cleanArgs mirrors the LCD: strip the wrapper plumbing (program name,
// --privileged, -oA <prefix>) to show just the operator's own options.
function cleanArgs(args) {
  if (!args) return '';
  const f = args.trim().split(/\s+/), out = [];
  for (let i = 0; i < f.length; i++) {
    if (i === 0) continue;
    if (f[i] === '--privileged' || f[i] === '--unprivileged') continue;
    if (f[i] === '-oA') { i++; continue; }
    out.push(f[i]);
  }
  return out.join(' ');
}

function cell(text, cls) {
  const td = document.createElement('td');
  td.textContent = text;
  if (cls) td.className = cls;
  return td;
}

function renderResult(res) {
  if (!res || !res.hosts) { $('results').style.display = 'none'; return; }
  $('results').style.display = 'block';
  $('res-opts').textContent = cleanArgs(res.args) || '(scan)';
  $('res-when').textContent = res.when ? new Date(res.when).toLocaleString() : '';

  const up = res.hosts.filter(h => h.up);
  const body = $('res-body');
  body.innerHTML = '';

  if (up.length === 0) { body.textContent = 'no hosts up'; return; }

  const sum = document.createElement('div');
  sum.className = 'res-sum';
  const table = document.createElement('table');
  table.className = 'res';

  // Single host with ports → PORT / STATE / SERVICE table.
  if (up.length === 1 && (up[0].ports || []).length) {
    const h = up[0];
    sum.textContent = h.addr + ' up' + (h.closed ? '  ·  ' + h.closed + ' closed' : '');
    for (const p of h.ports) {
      const tr = document.createElement('tr');
      const cls = p.state === 'open' ? 'open' : (p.state.includes('filtered') ? 'filtered' : '');
      tr.append(cell(p.num + '/' + p.proto), cell(p.state, cls), cell(p.service || ''));
      table.appendChild(tr);
    }
  } else {
    // Many hosts (or no ports, e.g. -sn) → host list with open ports.
    sum.textContent = up.length + ' host' + (up.length > 1 ? 's' : '') + ' up';
    for (const h of up) {
      const open = (h.ports || []).filter(p => p.state === 'open').map(p => p.num).join(', ');
      const tr = document.createElement('tr');
      tr.append(cell(h.addr, 'addr'), cell(open));
      table.appendChild(tr);
    }
  }
  body.append(sum, table);
}

async function poll() {
  let s;
  try { s = await (await fetch(`${API}/status`)).json(); } catch { return; }
  setMode(!!s.running);
  if (s.running) $('run-subject').textContent = s.options || '';
  renderResult(s.result);
}

$('start').addEventListener('click', async () => {
  const opts = $('opts').value.trim();
  if (!opts) return setMsg('enter scan options', true);
  setMsg('starting scan…');
  try {
    const r = await fetch(`${API}/start`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ options: opts, log_errors: $('logerr').checked })
    });
    if (!r.ok) throw new Error(await r.text());
    setMsg(''); poll();
  } catch (e) { setMsg(e.message.trim(), true); }
});

$('stop').addEventListener('click', async () => {
  setMsg('stopping…');
  try { await fetch(`${API}/stop`, { method: 'POST' }); setMsg(''); poll(); }
  catch (e) { setMsg(e.message.trim(), true); }
});

poll();
setInterval(poll, 2000);
