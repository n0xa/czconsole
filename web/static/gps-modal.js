// Shared GPS pill + detail modal for module pages. Include via
// <script src="/gps-modal.js"></script> on any page that has a #gps element.
// Injects its own modal overlay so module pages need no extra HTML.
(function () {
  const gpsEl = document.getElementById('gps');
  if (!gpsEl) return;

  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  overlay.hidden = true;
  overlay.innerHTML =
    '<div class="modal">' +
      '<div class="modal-head"><span>GPS</span><button id="gps-modal-close">✕</button></div>' +
      '<div id="gps-modal-body" class="modal-body"></div>' +
    '</div>';
  document.body.appendChild(overlay);

  const body = document.getElementById('gps-modal-body');
  document.getElementById('gps-modal-close').addEventListener('click', () => { overlay.hidden = true; });
  overlay.addEventListener('click', e => { if (e.target === overlay) overlay.hidden = true; });

  function kvRows(pairs) {
    return pairs.map(([k, v]) =>
      `<div class="kv"><span class="k">${k}</span><span>${v}</span></div>`).join('');
  }

  function satTable(sats) {
    if (!sats || !sats.length) return '';
    const rows = sats.slice().sort((a, b) => b.snr - a.snr).map(s => {
      const w = Math.max(0, Math.min(50, s.snr)) / 50 * 52;
      return `<tr><td class="${s.used ? 'used' : ''}">${s.prn}</td>` +
        `<td>${(s.el || 0).toFixed(0)}°</td><td>${(s.az || 0).toFixed(0)}°</td>` +
        `<td>${(s.snr || 0).toFixed(0)}</td>` +
        `<td style="text-align:left"><span class="snrbar" style="width:${w}px"></span></td></tr>`;
    }).join('');
    return `<table class="sat-tbl"><thead><tr>` +
      `<th>PRN</th><th>EL</th><th>AZ</th><th>SNR</th><th style="text-align:left">&nbsp;</th>` +
      `</tr></thead><tbody>${rows}</tbody></table>`;
  }

  gpsEl.addEventListener('click', async () => {
    body.innerHTML = '<div class="dim">reading gpsd…</div>';
    overlay.hidden = false;
    let d;
    try { d = await (await fetch('/api/gps')).json(); }
    catch (e) { body.innerHTML = '<div class="dim">gpsd unavailable</div>'; return; }
    if (!d.ok) {
      body.innerHTML = `<div class="dim">no fix — ${d.seen || 0} sats seen, ${d.used || 0} used</div>` + satTable(d.sats);
      return;
    }
    const kmh = d.speed * 3.6, mph = d.speed * 2.236936;
    const err = [d.eph ? d.eph.toFixed(1) + ' m horiz' : '', d.epv ? d.epv.toFixed(1) + ' m vert' : '']
      .filter(Boolean).join(', ') || '—';
    body.innerHTML = kvRows([
      ['Fix',        d.mode === 3 ? '3D' : '2D'],
      ['Latitude',   d.lat.toFixed(6) + '°'],
      ['Longitude',  d.lon.toFixed(6) + '°'],
      ['Altitude',   d.alt.toFixed(1) + ' m'],
      ['Speed',      `${kmh.toFixed(1)} km/h · ${mph.toFixed(1)} mph`],
      ['Track',      d.track.toFixed(0) + '°'],
      ['Climb',      d.climb.toFixed(1) + ' m/s'],
      ['Satellites', `${d.used} used / ${d.seen} seen`],
      ['Error',      err],
      ['Time',       d.time || '—'],
    ]) + satTable(d.sats);
  });

  async function pollGPS() {
    try {
      const s = await (await fetch('/api/sysinfo')).json();
      const g = s.gps;
      gpsEl.textContent = (g && g.ok)
        ? `GPS ${g.mode === 3 ? '3D' : '2D'}${g.sats ? ' · ' + g.sats : ''}`
        : 'GPS —';
      gpsEl.className = (g && g.ok) ? 'pill gps' : 'pill';
    } catch (e) {}
  }

  pollGPS();
  setInterval(pollGPS, 2000);
})();
