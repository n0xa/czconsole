// SDR Sweep module UI. Two mutually exclusive capture cards: rtl_power and rtl_433.
const $ = id => document.getElementById(id);
const API = 'api';

function outOfRange(v, min, max) { return isNaN(v) || v < min || v > max; }

function setMode(card, running) {
  const form = card === 'pwr' ? 'pwr-form' : 'r433-form';
  const run  = card === 'pwr' ? 'pwr-run'  : 'r433-run';
  const badge = card === 'pwr' ? 'pwr-badge' : 'r433-badge';
  const label = card === 'pwr' ? (running ? '● SWEEPING' : 'IDLE')
                                : (running ? '● SCANNING' : 'IDLE');
  $(form).style.display  = running ? 'none'  : 'block';
  $(run).style.display   = running ? 'block' : 'none';
  $(badge).textContent   = label;
  $(badge).className     = 'badge ' + (running ? 'run' : 'idle');
}

function setMsg(card, text, err) {
  const el = $(card + '-msg');
  el.textContent = text;
  el.className = 'msg' + (err ? ' err' : '');
}

async function poll() {
  let s;
  try { s = await (await fetch(`${API}/status`)).json(); } catch { return; }
  const pwrRun  = !!(s.rtlpower && s.rtlpower.running);
  const rdrRun  = !!(s.rtl433  && s.rtl433.running);
  const either  = pwrRun || rdrRun;
  const present = !!s.device_present;

  setMode('pwr',  pwrRun);
  setMode('r433', rdrRun);

  $('pwr-start').disabled  = either || !present;
  $('r433-start').disabled = either || !present;

  if (!present && !either) {
    setMsg('pwr',  'No RTL-SDR dongle detected');
    setMsg('r433', 'No RTL-SDR dongle detected');
  } else if (!either) {
    setMsg('pwr',  '');
    setMsg('r433', '');
  }
}

// ── rtl_power ──────────────────────────────────────────────────────────────

$('pwr-start').addEventListener('click', async () => {
  const low  = parseInt($('pwr-low').value,  10);
  const high = parseInt($('pwr-high').value, 10);
  const bin  = parseInt($('pwr-bin').value,  10);
  const crop = parseInt($('pwr-crop').value, 10);
  const gain = parseInt($('pwr-gain').value, 10);
  const intg = parseInt($('pwr-int').value,  10);
  const dur  = parseInt($('pwr-dur').value,  10);

  if (outOfRange(low,  27, 1700))  return setMsg('pwr', 'Low MHz must be 27–1700', true);
  if (outOfRange(high, 27, 1700))  return setMsg('pwr', 'High MHz must be 27–1700', true);
  if (high < low)                  return setMsg('pwr', 'High MHz must be ≥ Low MHz', true);
  if (outOfRange(bin,  1, 1000))   return setMsg('pwr', 'Bin KHz must be 1–1000', true);
  if (outOfRange(crop, 0, 90))     return setMsg('pwr', 'Crop must be 0–90', true);
  if (outOfRange(gain, 0, 50))     return setMsg('pwr', 'Gain must be 0–50', true);
  if (outOfRange(intg, 1, 600))    return setMsg('pwr', 'Integration must be 1–600 s', true);
  if (outOfRange(dur,  0, 86400))  return setMsg('pwr', 'Duration must be 0–86400 s', true);

  setMsg('pwr', 'starting sweep…');
  try {
    const r = await fetch(`${API}/rtlpower/start`, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        low_mhz: low, high_mhz: high, bin_khz: bin, crop, gain,
        integration: intg, duration: dur, heatmap: $('pwr-hm').checked
      })
    });
    if (!r.ok) throw new Error(await r.text());
    setMsg('pwr', '');
    poll();
  } catch (e) { setMsg('pwr', e.message.trim(), true); }
});

$('pwr-stop').addEventListener('click', async () => {
  setMsg('pwr', 'stopping…');
  try {
    await fetch(`${API}/rtlpower/stop`, {method: 'POST'});
    setMsg('pwr', '');
    poll();
  } catch (e) { setMsg('pwr', e.message.trim(), true); }
});

// ── rtl_433 ────────────────────────────────────────────────────────────────

$('r433-start').addEventListener('click', async () => {
  const freq = parseFloat($('r433-freq').value);
  const bw   = parseInt($('r433-bw').value,   10);
  const gain = parseInt($('r433-gain').value,  10);
  const dur  = parseInt($('r433-dur').value,   10);

  if (isNaN(freq) || freq < 27 || freq > 1700) return setMsg('r433', 'Freq MHz must be 27–1700', true);
  if (outOfRange(bw,   1, 1000))  return setMsg('r433', 'Bandwidth KHz must be 1–1000', true);
  if (outOfRange(gain, 0, 50))    return setMsg('r433', 'Gain must be 0–50', true);
  if (outOfRange(dur,  0, 86400)) return setMsg('r433', 'Duration must be 0–86400 s', true);

  setMsg('r433', 'starting scanner…');
  try {
    const r = await fetch(`${API}/rtl433/start`, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({freq_mhz: freq, bw_khz: bw, gain, duration: dur})
    });
    if (!r.ok) throw new Error(await r.text());
    setMsg('r433', '');
    poll();
  } catch (e) { setMsg('r433', e.message.trim(), true); }
});

$('r433-stop').addEventListener('click', async () => {
  setMsg('r433', 'stopping…');
  try {
    await fetch(`${API}/rtl433/stop`, {method: 'POST'});
    setMsg('r433', '');
    poll();
  } catch (e) { setMsg('r433', e.message.trim(), true); }
});

poll();
setInterval(poll, 2000);
