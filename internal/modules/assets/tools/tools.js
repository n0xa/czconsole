// Generic, spec-driven tool group UI. One page renders any group's tools from
// /etc/czconsole/tools.d specs (served at api/specs): a card grid, and per tool a
// setup/results page. Execution via api/start|stop|status (tool.Runner); output
// is read by the browser from the files-agent (added next).
const $ = id => document.getElementById(id);
const main = $('main');
let data = { tools: [], extras: [] };
let pollTimer = null;

async function getSpecs() {
  if (data.tools.length || data.extras.length) return data;
  data = await (await fetch('api/specs')).json();
  return data;
}
const bySlug = id => data.tools.find(s => s.id === id);

function stopPoll() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }

async function route() {
  stopPoll();
  await getSpecs();
  const id = location.hash.replace(/^#/, '');
  if (id && bySlug(id)) renderTool(bySlug(id));
  else renderGroup();
}

// ── group view: a card per tool ──────────────────────────────────────────────
function renderGroup() {
  $('back').setAttribute('href', '/');
  $('title').textContent = document.title.split(' —')[0].toUpperCase();
  const toolCards = data.tools.map(s => `
    <a class="toolcard${s.available ? '' : ' off'}" href="#${s.id}">
      <span class="dot" id="dot-${s.id}"></span>
      <b>${s.name}</b>
      <small>${s.available ? s.binary : s.binary + ' — not installed'}</small>
    </a>`).join('');
  // nested bespoke modules (e.g. Wardrive) link out to their own UI
  const extraCards = (data.extras || []).map(e => `
    <a class="toolcard${e.available ? '' : ' off'}" href="/m/${e.id}">
      <span class="dot" id="dot-${e.id}"></span>
      <b>${e.name}</b>
      <small>↗ open</small>
    </a>`).join('');
  main.innerHTML = `<div class="tools">${toolCards}${extraCards}</div>`;
  // live running dots — spec tools via this group's api, extras via their own
  const setDot = (id, running) => { const d = $(`dot-${id}`); if (d) d.className = 'dot' + (running ? ' run' : ''); };
  const refresh = async () => {
    for (const s of data.tools) {
      try { const st = await (await fetch(`api/status?tool=${s.id}`)).json(); setDot(s.id, st.running); } catch {}
    }
    for (const e of (data.extras || [])) {
      try { const st = await (await fetch(`/m/${e.id}/api/status`)).json(); setDot(e.id, st.running); } catch {}
    }
  };
  refresh();
  pollTimer = setInterval(refresh, 2000);
}

// ── tool view: setup form + controls ─────────────────────────────────────────
function renderTool(spec) {
  $('back').setAttribute('href', '#');
  $('title').textContent = spec.name.toUpperCase();

  const inputs = (spec.inputs || []).map(inp => {
    if (inp.type === 'checkbox') {
      return `<div class="check-row"><input type="checkbox" id="in-${inp.id}" ${inp.default === '1' ? 'checked' : ''}>
              <label for="in-${inp.id}">${inp.label}</label></div>`;
    }
    return `<div class="field"><label>${inp.label}</label>
            <input type="text" id="in-${inp.id}" value="${inp.default || ''}"
                   placeholder="${inp.placeholder || ''}" autocapitalize="off" autocomplete="off" spellcheck="false"></div>`;
  }).join('');

  main.innerHTML = `
    <section class="card">
      <div id="form">${inputs}
        <div class="controls"><button class="btn start" id="start">START</button></div>
      </div>
      <div id="run" style="display:none">
        <div class="controls"><button class="btn stop" id="stop">STOP</button></div>
      </div>
      <div class="msg" id="msg"></div>
    </section>
    <section class="card"><div id="results" class="dim" style="font-size:13px">loading…</div></section>`;

  $('start').onclick = () => start(spec);
  $('stop').onclick = () => stop(spec);

  renderResults(spec, false);
  let wasRunning = null;
  const refresh = async () => {
    let st;
    try { st = await (await fetch(`api/status?tool=${spec.id}`)).json(); } catch { return; }
    $('form').style.display = st.running ? 'none' : 'block';
    $('run').style.display = st.running ? 'block' : 'none';
    // re-render on start/finish; for a live text tool (rtl_433), keep re-reading
    // the growing output each poll for a live decode feed.
    const liveText = st.running && (spec.results || {}).kind === 'text';
    if (st.running !== wasRunning || liveText) { wasRunning = st.running; renderResults(spec, liveText); }
  };
  refresh();
  pollTimer = setInterval(refresh, 2000);
}

function values(spec) {
  const v = {};
  for (const inp of spec.inputs || []) {
    const el = $(`in-${inp.id}`);
    v[inp.id] = inp.type === 'checkbox' ? (el.checked ? '1' : '0') : el.value;
  }
  return v;
}
function setMsg(t, err) { const e = $('msg'); if (e) { e.textContent = t; e.className = 'msg' + (err ? ' err' : ''); } }

async function start(spec) {
  // required-field check
  for (const inp of spec.inputs || []) {
    if (inp.required && inp.type !== 'checkbox' && !$(`in-${inp.id}`).value.trim()) {
      return setMsg(inp.label + ' required', true);
    }
  }
  setMsg('starting…');
  try {
    const r = await fetch(`api/start?tool=${spec.id}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values(spec))
    });
    if (!r.ok) throw new Error(await r.text());
    setMsg('');
  } catch (e) { setMsg(e.message.trim(), true); }
}
async function stop(spec) {
  setMsg('stopping…');
  try { await fetch(`api/stop?tool=${spec.id}`, { method: 'POST' }); setMsg(''); }
  catch (e) { setMsg(e.message.trim(), true); }
}

// ── results: list ~/<tool> via the files-agent, render the newest per kind ────
const COLOR = { accent: '#39ff8a', dim: 'var(--dim)', title: 'var(--accent)', text: 'var(--fg)' };
const filesAPI = (op, rel) => `/m/files/api/${op}?path=${encodeURIComponent(rel)}`;
const esc = s => s.replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
const safeRe = p => { try { return new RegExp(p); } catch { return null; } };

async function renderResults(spec, live) {
  const box = $('results');
  if (!box) return;
  const res = spec.results || {};
  const suffix = res.file || '';

  let entries = [];
  try {
    const j = await (await fetch(filesAPI('list', spec.id))).json();
    entries = (j.entries || []).filter(e => !e.dir);
  } catch { box.innerHTML = '<span class="dim">no output dir yet</span>'; return; }

  // newest run by name — the embedded timestamp sorts lexically = chronologically
  const runs = entries.filter(e => !suffix || e.name.endsWith(suffix)).sort((a, b) => a.name < b.name ? 1 : -1);
  if (!runs.length) { box.innerHTML = '<span class="dim">no runs yet</span>'; return; }
  const primary = runs[0].name;
  const dl = name => filesAPI('download', spec.id + '/' + name);
  const head = `<div class="rhead">${new Date(runs[0].mtime * 1000).toLocaleString()}</div>`;

  if (res.kind === 'image') {
    const img = res.image ? primary.slice(0, -suffix.length) + res.image : '';
    if (img && entries.some(e => e.name === img)) {
      box.innerHTML = head + `<a class="dl" href="${dl(img)}" target="_blank" rel="noopener">🖼 view heatmap ↗ <span class="dim">(full resolution)</span></a>`;
    } else {
      box.innerHTML = head + `<span class="dim">no heatmap for this run</span><br><a class="dl" href="${dl(primary)}&dl=1">⤓ ${esc(primary)}</a>`;
    }
    return;
  }
  if (res.kind === 'path') {
    box.innerHTML = head + `<a class="dl" href="${dl(primary)}&dl=1">⤓ ${esc(primary)}</a>`;
    return;
  }

  // text: fetch + strip + colorize client-side
  let text = '';
  try { text = await (await fetch(dl(primary))).text(); } catch { box.innerHTML = head + '<span class="dim">(unreadable)</span>'; return; }
  const body = renderText(text, res);
  box.innerHTML = head + (body
    ? `<pre class="log">${body}</pre>`
    : `<span class="dim">${live ? 'listening for decodes…' : '(empty)'}</span>`);
  if (live) { const pre = box.querySelector('.log'); if (pre) pre.scrollTop = pre.scrollHeight; }
}

function renderText(text, res) {
  const strip = res.strip_prefix || '';
  const rules = (res.colorize || []).map(c => ({ re: safeRe(c.match), col: COLOR[c.color] || 'var(--fg)' }));
  const out = [];
  for (const raw of text.split('\n')) {
    const t = raw.replace(/\r$/, '');
    if (!t.trim()) continue;
    if (strip && t.trimStart().startsWith(strip)) continue;
    let col = 'var(--fg)';
    for (const r of rules) { if (r.re && r.re.test(t)) { col = r.col; break; } }
    out.push(`<span style="color:${col}">${esc(t)}</span>`);
  }
  return out.join('\n');
}

window.addEventListener('hashchange', route);
route();
