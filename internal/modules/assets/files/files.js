// Files module — phone file browser jailed to the device user's home.
// Navigation state is the current path relative to that home root.

const API = 'api';
let cur = '';                       // relative path, '' = root
const $ = (id) => document.getElementById(id);

function fmtSize(n) {
  if (n < 1024) return n + ' B';
  const u = ['K', 'M', 'G', 'T'];
  let i = -1; do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + u[i];
}

function fmtDate(unix) {
  if (!unix) return '';
  const d = new Date(unix * 1000);
  const p = (x) => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function parent(path) {
  const i = path.replace(/\/$/, '').lastIndexOf('/');
  return i < 0 ? '' : path.slice(0, i);
}

async function load(path) {
  cur = path;
  let data;
  try {
    const r = await fetch(`${API}/list?path=${encodeURIComponent(path)}`);
    if (!r.ok) throw new Error(await r.text());
    data = await r.json();
  } catch (e) {
    $('flist').innerHTML = `<div class="frow"><span class="nm" style="color:#ff5a5a">${e}</span></div>`;
    return;
  }

  $('root').textContent = '~' + (data.root ? '/' + data.root : '');
  $('crumbs').textContent = '/' + (data.path || '');

  const rows = [];
  if (path) {
    rows.push(`<div class="frow up" data-dir="${encodeURIComponent(parent(path))}">
      <span class="ic">↰</span><span class="nm">..</span></div>`);
  }
  for (const e of data.entries) {
    const child = (path ? path + '/' : '') + e.name;
    if (e.dir) {
      rows.push(`<div class="frow dir" data-dir="${encodeURIComponent(child)}">
        <span class="ic">📁</span><span class="nm">${e.name}</span>
        <span class="meta">${fmtDate(e.mtime)}</span></div>`);
    } else {
      const enc = encodeURIComponent(child);
      rows.push(`<div class="frow file">
        <a class="open" href="${API}/download?path=${enc}" target="_blank" rel="noopener">
          <span class="ic">·</span><span class="nm">${e.name}</span>
          <span class="meta">${fmtSize(e.size)} · ${fmtDate(e.mtime)}</span></a>
        <a class="dl" href="${API}/download?path=${enc}&dl=1" title="download">⬇</a></div>`);
    }
  }
  $('flist').innerHTML = rows.join('') || '<div class="frow"><span class="nm dim">empty</span></div>';

  $('flist').querySelectorAll('.frow[data-dir]').forEach(el =>
    el.addEventListener('click', () => load(decodeURIComponent(el.dataset.dir))));
}

$('up').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const files = $('file').files;
  if (!files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('file', f);
  const msg = $('msg');
  msg.className = 'msg'; msg.textContent = `uploading ${files.length} file(s)…`;
  try {
    const r = await fetch(`${API}/upload?path=${encodeURIComponent(cur)}`, { method: 'POST', body: fd });
    if (!r.ok) throw new Error(await r.text());
    const j = await r.json();
    msg.textContent = `uploaded ${j.saved} file(s)`;
    $('file').value = '';
    load(cur);
  } catch (e) {
    msg.className = 'msg err'; msg.textContent = 'upload failed: ' + e;
  }
});

load('');
