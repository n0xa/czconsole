// CZ Field Console dashboard. Polls /api/sysinfo + /api/modules and renders the
// glance row, adapter list, and capability-gated module tiles. Kept dependency-
// free (vanilla fetch + DOM) so it loads instantly on a phone over hotspot.

const ICONS = { radar:'📡', wave:'〰️', crosshair:'✛', folder:'📁', monitor:'🖥️', default:'▣' };
const $ = (id) => document.getElementById(id);

function gpsLabel(g) {
  if (!g || !g.ok) return ['GPS —', 'pill'];
  const mode = g.mode === 3 ? '3D' : '2D';
  const sats = g.sats ? ` · ${g.sats} sat` : '';
  return [`GPS ${mode}${sats}`, 'pill gps'];
}

function battLabel(b) {
  if (!b || !b.valid) return ['—', 'pill'];
  const cls = b.percent <= 15 ? 'pill warn' : 'pill ok';
  return [`${b.percent}%${b.charging ? ' chg' : ''}`, cls];
}

// Mirror procps uptime(1): "up 5 min", "up 1:27", "up 2 days, 4:05".
function fmtUptime(secs) {
  secs = Math.floor(secs || 0);
  const days = Math.floor(secs / 86400);
  let mins = Math.floor(secs / 60);
  const hours = Math.floor(mins / 60) % 24;
  mins %= 60;
  let s = 'up ';
  if (days) s += days + (days === 1 ? ' day, ' : ' days, ');
  s += hours ? `${hours}:${String(mins).padStart(2, '0')}` : `${mins} min`;
  return s;
}

async function refreshSysinfo() {
  let s;
  try { s = await (await fetch('/api/sysinfo')).json(); }
  catch (e) { $('foot').textContent = 'offline: ' + e; return; }

  const [gl, gc] = gpsLabel(s.gps); $('gps').textContent = gl; $('gps').className = gc;
  const [bl, bc] = battLabel(s.battery); $('batt').textContent = bl; $('batt').className = bc;

  $('glance').innerHTML = [
    [s.hostname || '—', 'HOST'],
    [(s.temp_c || 0).toFixed(0) + '°C', 'TEMP'],
    [s.mem_avail_mb, 'MB FREE'],
    [(s.load1 ?? 0).toFixed(2), 'LOAD'],
  ].map(([n, l]) => `<div class="g"><div class="n">${n}</div><div class="l">${l}</div></div>`).join('');

  $('adapters').innerHTML = (s.adapters || []).map(a => {
    const caps = [];
    if (a.mon_cap) caps.push('<span class="cap mon">+mon</span>');
    if (a.inject)  caps.push('<span class="cap inj">+inj</span>');
    const state = a.monitor ? 'monitor' : a.up ? 'up' : 'down';
    const stCls = a.monitor ? 'st warn' : a.up ? 'st ok' : 'st';
    const title = a.driver ? ` title="driver: ${a.driver}"` : '';
    return `<div class="r" data-adapter="${a.name}"${title}>
      <span>${a.name} <span class="sub">${a.type}</span> ${caps.join(' ')}</span>
      <span class="${stCls}">(${state})</span></div>`;
  }).join('') || '<span class="dim">none</span>';

  if (s.gps && s.gps.ok) {
    $('foot').textContent = `${s.gps.lat.toFixed(5)}, ${s.gps.lon.toFixed(5)} · ${fmtUptime(s.uptime_s)}`;
  } else {
    $('foot').textContent = fmtUptime(s.uptime_s);
  }
}

async function refreshModules() {
  let mods;
  try { mods = await (await fetch('/api/modules')).json(); }
  catch (e) { return; }

  // Live run-state → tile dot class (set by the backend for stateful modules).
  const ST = { running: 'run', stopped: 'stop', failed: 'fail', unknown: 'need', warn: 'warn' };
  $('modules').innerHTML = mods.map(m => {
    const ic = ICONS[m.icon] || ICONS.default;
    const off = m.available ? '' : ' off';
    let tag;
    if (!m.available) {
      tag = `<span class="tag need">${(m.missing && m.missing[0]) || 'unavailable'}</span>`;
    } else if (m.status && ST[m.status.state]) {
      tag = `<span class="tag ${ST[m.status.state]}">● ${m.status.label || m.status.state}</span>`;
    } else {
      tag = '<span class="tag live">● ready</span>';
    }
    const href = m.available ? `/m/${m.id}` : 'javascript:void(0)';
    return `<a class="tile${off}" href="${href}">${tag}
      <div class="ic">${ic}</div>
      <div class="nm">${m.name}</div>
      <div class="ds">${m.description || ''}</div></a>`;
  }).join('');
}

function refreshMirror() {
  const img = $('mirror');
  img.onerror = () => { img.removeAttribute('src'); $('mirror-msg').textContent = 'LCD mirror unavailable'; };
  img.onload  = () => { $('mirror-msg').textContent = 'tap to refresh'; };
  img.src = '/api/display.jpg?ts=' + Date.now();
}

$('mirror').addEventListener('click', refreshMirror);

// ── detail modals ──────────────────────────────────────────────────────────

function openModal(title, html) {
  $('modal-title').textContent = title;
  $('modal-body').innerHTML = html;
  $('modal').hidden = false;
}
function closeModal() { $('modal').hidden = true; }
$('modal-close').addEventListener('click', closeModal);
$('modal').addEventListener('click', e => { if (e.target.id === 'modal') closeModal(); });

const kvRows = (pairs) => pairs
  .map(([k, v]) => `<div class="kv"><span class="k">${k}</span><span>${v}</span></div>`).join('');

function fmtBytes(n) {
  if (n < 1024) return n + ' B';
  const u = ['K', 'M', 'G', 'T']; let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(1) + u[i];
}

function satTable(sats) {
  if (!sats || !sats.length) return '';
  const rows = sats.slice().sort((a, b) => b.snr - a.snr).map(s => {
    const w = Math.max(0, Math.min(50, s.snr)) / 50 * 52;
    return `<tr><td class="${s.used ? 'used' : ''}">${s.prn}</td>
      <td>${(s.el || 0).toFixed(0)}°</td><td>${(s.az || 0).toFixed(0)}°</td>
      <td>${(s.snr || 0).toFixed(0)}</td>
      <td style="text-align:left"><span class="snrbar" style="width:${w}px"></span></td></tr>`;
  }).join('');
  return `<table class="sat-tbl"><thead><tr>
    <th>PRN</th><th>EL</th><th>AZ</th><th>SNR</th><th style="text-align:left">&nbsp;</th>
    </tr></thead><tbody>${rows}</tbody></table>`;
}

$('gps').addEventListener('click', async () => {
  openModal('GPS', '<div class="dim">reading gpsd…</div>');
  let d;
  try { d = await (await fetch('/api/gps')).json(); }
  catch (e) { $('modal-body').innerHTML = '<div class="dim">gpsd unavailable</div>'; return; }
  if (!d.ok) {
    $('modal-body').innerHTML = `<div class="dim">no fix — ${d.seen || 0} sats seen, ${d.used || 0} used</div>` + satTable(d.sats);
    return;
  }
  const kmh = (d.speed * 3.6), mph = (d.speed * 2.236936);
  const err = [d.eph ? d.eph.toFixed(1) + ' m horiz' : '', d.epv ? d.epv.toFixed(1) + ' m vert' : '']
    .filter(Boolean).join(', ') || '—';
  $('modal-body').innerHTML = kvRows([
    ['Fix', (d.mode === 3 ? '3D' : '2D')],
    ['Latitude', d.lat.toFixed(6) + '°'],
    ['Longitude', d.lon.toFixed(6) + '°'],
    ['Altitude', d.alt.toFixed(1) + ' m'],
    ['Speed', `${kmh.toFixed(1)} km/h · ${mph.toFixed(1)} mph`],
    ['Track', d.track.toFixed(0) + '°'],
    ['Climb', d.climb.toFixed(1) + ' m/s'],
    ['Satellites', `${d.used} used / ${d.seen} seen`],
    ['Error', err],
    ['Time', d.time || '—'],
  ]) + satTable(d.sats);
});

async function showAdapter(name) {
  openModal(name, '<div class="dim">reading…</div>');
  let d;
  try { d = await (await fetch('/api/adapter?name=' + encodeURIComponent(name))).json(); }
  catch (e) { $('modal-body').innerHTML = '<div class="dim">unavailable</div>'; return; }
  const caps = [];
  if (d.monitor) caps.push('monitor');
  if (d.mon_cap) caps.push('+mon');
  if (d.inject) caps.push('+inj');
  const rxtra = [d.rx_errors ? d.rx_errors + ' err' : '', d.rx_dropped ? d.rx_dropped + ' drop' : ''].filter(Boolean).join(' · ');
  const txtra = [d.tx_errors ? d.tx_errors + ' err' : '', d.tx_dropped ? d.tx_dropped + ' drop' : ''].filter(Boolean).join(' · ');
  $('modal-body').innerHTML = kvRows([
    ['Type', d.type + (d.driver ? ` (${d.driver})` : '')],
    ['State', d.state || '—'],
    ['MAC', d.mac || '—'],
    ['MTU', d.mtu],
    ['Link', d.speed_mbps > 0 ? d.speed_mbps + ' Mbps' : '—'],
    ['Capabilities', caps.join(' ') || '—'],
    ['Addresses', (d.ips && d.ips.length) ? d.ips.join('<br>') : '—'],
    ['RX', `${fmtBytes(d.rx_bytes)} · ${d.rx_packets} pkt${rxtra ? ' · ' + rxtra : ''}`],
    ['TX', `${fmtBytes(d.tx_bytes)} · ${d.tx_packets} pkt${txtra ? ' · ' + txtra : ''}`],
  ]);
}

$('adapters').addEventListener('click', e => {
  const row = e.target.closest('[data-adapter]');
  if (row) showAdapter(row.dataset.adapter);
});

refreshSysinfo(); refreshModules(); refreshMirror();
setInterval(refreshSysinfo, 2000);
setInterval(refreshModules, 10000);
// Auto-refresh the LCD mirror on the same slow cadence as the tiles — keeps the
// on-screen view current for hands-free / on-the-go glancing without much
// bandwidth (one small framebuffer JPEG per cycle). Tap still refreshes now.
setInterval(refreshMirror, 10000);
