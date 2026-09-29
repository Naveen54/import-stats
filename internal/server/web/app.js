'use strict';

let REPORT = null;
let filter = '';
let busy = false;

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const el = (tag, attrs = {}, ...children) => {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const c of children) {
    if (c === null || c === undefined) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
};

const matches = (...values) =>
  !filter || values.some((v) => String(v || '').toLowerCase().includes(filter));

async function load() {
  const res = await fetch('/api/report');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  apply(await res.json());
}

function apply(report) {
  REPORT = report;
  renderHeader();
  renderCards();
  renderAll();
}

/* ---------- re-analysis ---------- */

async function reanalyze() {
  if (busy) return;
  busy = true;
  const btn = $('#reanalyze');
  btn.disabled = true;
  btn.classList.add('spinning');
  const previous = btn.textContent;
  btn.textContent = '↻ Analysing…';
  const started = Date.now();

  try {
    const res = await fetch('/api/reanalyze', { method: 'POST' });
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
    const before = REPORT && REPORT.summary;
    apply(body);
    toast(changeSummary(before, body.summary), 'ok');
  } catch (err) {
    toast(`Re-analysis failed: ${err.message}`, 'error');
  } finally {
    // Keep the spinner visible briefly so a fast run still reads as an action.
    const wait = Math.max(0, 350 - (Date.now() - started));
    setTimeout(() => {
      btn.disabled = false;
      btn.classList.remove('spinning');
      btn.textContent = previous;
      busy = false;
    }, wait);
  }
}

// changeSummary describes what moved between two runs.
function changeSummary(before, after) {
  if (!before) return 'Analysis complete.';
  const deltas = [
    ['files', after.filesAnalyzed - before.filesAnalyzed],
    ['imports', after.totalImports - before.totalImports],
    ['packages', after.uniquePackages - before.uniquePackages],
  ].filter(([, d]) => d !== 0)
    .map(([label, d]) => `${d > 0 ? '+' : ''}${d} ${label}`);
  return deltas.length
    ? `Re-analysed in ${after.durationMs} ms — ${deltas.join(', ')}`
    : `Re-analysed in ${after.durationMs} ms — no changes`;
}

let toastTimer = null;
function toast(message, kind) {
  const node = $('#toast');
  node.textContent = message;
  node.className = `toast show ${kind || ''}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { node.className = 'toast'; }, 4000);
}

/* ---------- timestamps ---------- */

// formatTimestamp renders an RFC3339 timestamp in the viewer's locale.
function formatTimestamp(iso) {
  if (!iso) return 'unknown time';
  const d = new Date(iso);
  if (isNaN(d)) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
}

function relativeTime(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '';
  const secs = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000));
  if (secs < 10) return 'just now';
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}

// refreshRelativeTime keeps the "x min ago" label current without refetching.
function refreshRelativeTime() {
  if (!REPORT) return;
  const rel = relativeTime(REPORT.summary.generatedAt);
  $('#generated-rel').textContent = rel ? ` · ${rel}` : '';
}
setInterval(refreshRelativeTime, 15000);

function renderHeader() {
  const s = REPORT.summary;
  $('#project-name').textContent = s.projectName || 'import-stats';
  $('#entry-line').textContent = `${s.entry} · ${s.projectRoot}`;
  $('#generated-abs').textContent =
    `Analysed ${formatTimestamp(s.generatedAt)} in ${s.durationMs} ms`;
  $('#generated-line').title = s.generatedAt || '';
  refreshRelativeTime();
  document.title = `${s.projectName || 'importstats'} — import stats`;
}

function card(value, label, cls) {
  return el('div', { class: `card ${cls || ''}` },
    el('div', { class: 'value', text: String(value) }),
    el('div', { class: 'label', text: label }));
}

function renderCards() {
  const s = REPORT.summary;
  const cards = $('#cards');
  cards.replaceChildren(
    card(s.uniquePackages, 'npm packages'),
    card(s.packageImports, 'package imports'),
    card(s.filesAnalyzed, `files reached (of ${s.srcFiles})`),
    card(s.localImports, 'internal imports'),
    card(s.styleImports + s.assetImports, 'style & asset imports'),
    card(s.orphanFiles, 'unreachable files', s.orphanFiles ? 'warn' : 'good'),
    card(s.cycles, 'circular groups', s.cycles ? 'danger' : 'good'),
    card(s.unusedDependencies, 'unused deps', s.unusedDependencies ? 'warn' : 'good'),
    card(s.undeclaredDependencies, 'undeclared deps', s.undeclaredDependencies ? 'danger' : 'good'),
  );
}

function renderAll() {
  renderPackages();
  renderSymbols();
  renderFiles();
  renderAssets();
  renderOrphans();
  renderCycles();
  renderDeps();
  renderInsights();
  renderUnusedExports();
  renderRules();
  renderWarnings();
}

// formatSize renders installed package weight; packages absent from
// node_modules report nothing rather than a misleading zero.
function formatSize(bytes) {
  if (!bytes) return '—';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = bytes;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++; }
  return `${u === 0 ? v : v.toFixed(1)} ${units[u]}`;
}

/* ---------- insights ---------- */

function renderInsights() {
  const list = (REPORT.insights || []).filter((i) =>
    matches(i.id, i.subject, i.message, (i.evidence || []).join(' ')));
  const box = $('#insight-list');

  if (!list.length) {
    box.replaceChildren(el('div', { class: 'empty', text: 'No insights — nothing worth flagging.' }));
    return;
  }
  box.replaceChildren(...list.map((i) =>
    el('div', { class: `insight ${i.severity}` },
      el('div', { class: 'insight-head' },
        el('span', { class: `tag ${i.severity}`, text: i.id }),
        el('span', { class: 'name', text: i.subject })),
      el('p', { class: 'insight-msg', text: i.message }),
      (i.evidence && i.evidence.length)
        ? el('ul', { class: 'insight-evidence' },
          ...i.evidence.map((e) => el('li', { class: 'path', text: e })))
        : null)));
}

/* ---------- unused exports ---------- */

function renderUnusedExports() {
  const list = (REPORT.unusedExports || []).filter((e) => matches(e.file, e.name, e.kind));
  const body = $('#export-table tbody');

  if (!list.length) {
    body.replaceChildren(el('tr', {}, el('td', {
      colspan: '4', class: 'empty',
      text: (REPORT.unusedExports || []).length ? 'No exports match the filter.' : 'Every export is imported somewhere.',
    })));
    return;
  }
  body.replaceChildren(...list.map((e) =>
    el('tr', { title: e.reason || '' },
      el('td', { class: 'path', text: e.file }),
      el('td', { class: 'num', text: e.line }),
      el('td', {}, el('span', { class: 'name', text: e.name })),
      el('td', {}, el('span', { class: 'tag', text: e.kind })))));
}

/* ---------- architecture rules ---------- */

function renderRules() {
  const all = REPORT.ruleViolations || [];
  const list = all.filter((v) => matches(v.rule, v.from, v.to));
  const body = $('#rule-table tbody');

  if (!list.length) {
    body.replaceChildren(el('tr', {}, el('td', {
      colspan: '4', class: 'empty',
      text: all.length ? 'No violations match the filter.' : 'No rule violations. Pass --rule to enforce layering.',
    })));
    return;
  }
  body.replaceChildren(...list.map((v) =>
    el('tr', {},
      el('td', {}, el('code', { class: 'rule', text: v.rule })),
      el('td', { class: 'path', text: v.from }),
      el('td', { class: 'num', text: v.line }),
      el('td', { class: 'path', text: v.to }))));
}

/* ---------- packages ---------- */

function sortedPackages() {
  const mode = $('#pkg-sort').value;
  const hideBuiltin = $('#hide-builtin').checked;
  let list = REPORT.packages.filter((p) => !(hideBuiltin && p.depType === 'builtin'));
  list = list.filter((p) =>
    matches(p.name, p.depType, p.version, (p.symbols || []).map((s) => s.name).join(' '),
      p.sites.map((s) => s.file).join(' ')));
  const cmp = {
    imports: (a, b) => b.imports - a.imports || a.name.localeCompare(b.name),
    files: (a, b) => b.files - a.files || a.name.localeCompare(b.name),
    size: (a, b) => (b.sizeBytes || 0) - (a.sizeBytes || 0) || a.name.localeCompare(b.name),
    name: (a, b) => a.name.localeCompare(b.name),
  }[mode];
  return list.slice().sort(cmp);
}

function renderPackages() {
  const list = sortedPackages();
  const max = list.reduce((m, p) => Math.max(m, p.imports), 1);
  const body = $('#pkg-table tbody');

  if (!list.length) {
    body.replaceChildren(el('tr', {}, el('td', { colspan: '8', class: 'empty', text: 'No packages match the filter.' })));
    return;
  }

  body.replaceChildren(...list.map((p, i) => {
    const kinds = Object.entries(p.kinds || {})
      .sort((a, b) => b[1] - a[1])
      .map(([k, v]) => `${k}×${v}`)
      .join(', ');
    return el('tr', { class: 'clickable', onclick: () => openPackage(p) },
      el('td', { class: 'num', text: i + 1 }),
      el('td', {}, el('span', { class: 'name', text: p.name }),
        p.subpaths && p.subpaths.length
          ? el('span', { class: 'muted', text: `  +${p.subpaths.length} subpath${p.subpaths.length > 1 ? 's' : ''}` })
          : null),
      el('td', { class: 'num', text: p.imports }),
      el('td', { class: 'num', text: p.files }),
      el('td', {}, el('div', { class: 'bar' },
        el('span', { style: `width:${Math.round((p.imports / max) * 100)}%` }))),
      el('td', { class: 'num muted', title: p.fileCount ? `${p.fileCount} files installed` : '', text: formatSize(p.sizeBytes) }),
      el('td', { class: 'muted', text: p.version || '—' }),
      el('td', {}, el('span', { class: `tag ${p.depType.replace('?', '')}`, text: p.depType })),
      el('td', { class: 'muted', text: kinds }));
  }));
}

function openPackage(p) {
  const byFile = new Map();
  for (const s of p.sites) {
    if (!byFile.has(s.file)) byFile.set(s.file, []);
    byFile.get(s.file).push(s);
  }
  const rows = Array.from(byFile.entries()).sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]));

  const build = (needle) => {
    const filtered = rows.filter(([file, sites]) =>
      !needle || file.toLowerCase().includes(needle) ||
      sites.some((s) => (s.symbols || []).join(' ').toLowerCase().includes(needle)));
    const table = el('table', { class: 'data' },
      el('thead', {}, el('tr', {},
        el('th', { text: 'File' }), el('th', { text: 'Line' }),
        el('th', { text: 'Kind' }), el('th', { text: 'Specifier' }), el('th', { text: 'Imported symbols' }))),
      el('tbody', {}, ...filtered.flatMap(([file, sites]) =>
        sites.map((s) => el('tr', {},
          el('td', { class: 'path', text: file }),
          el('td', { class: 'num', text: s.line }),
          el('td', {}, el('span', { class: 'tag', text: s.kind })),
          el('td', { class: 'path muted', text: s.specifier }),
          el('td', { class: 'path muted', text: prettySymbols(s.symbols) }))))));
    if (!filtered.length) return el('div', { class: 'empty', text: 'No matching files.' });
    return table;
  };

  openDrawer(p.name,
    `${p.imports} import statements across ${p.files} files` +
    (p.version ? ` · declared ${p.version}` : '') +
    (p.subpaths && p.subpaths.length ? ` · subpaths: ${p.subpaths.join(', ')}` : ''),
    build, p.entryChain);
}

function prettySymbols(symbols) {
  if (!symbols || !symbols.length) return '—';
  return symbols
    .map((s) => (s.startsWith('default:') ? `${s.slice(8)} (default)` : s.startsWith('*:') ? `* as ${s.slice(2)}` : s))
    .join(', ');
}

/* ---------- symbols ---------- */

function renderSymbols() {
  const host = $('#symbol-list');
  const list = REPORT.packages
    .filter((p) => (p.symbols || []).length)
    .filter((p) => matches(p.name, (p.symbols || []).map((s) => s.name).join(' ')));
  if (!list.length) {
    host.replaceChildren(el('div', { class: 'empty', text: 'No symbols match the filter.' }));
    return;
  }
  host.replaceChildren(...list.map((p) =>
    el('div', { class: 'block' },
      el('h4', { text: `${p.name}  ·  ${p.symbols.length} distinct bindings` }),
      el('div', { class: 'chips' }, ...p.symbols.map((s) =>
        el('span', { class: 'chip' },
          el('span', { text: prettySymbols([s.name]) }),
          el('b', { text: ` ${s.count}` })))))));
}

/* ---------- internal files ---------- */

function renderFiles() {
  const mode = $('#file-sort').value;
  const cmp = {
    imports: (a, b) => b.imports - a.imports || a.file.localeCompare(b.file),
    fanIn: (a, b) => b.fanIn - a.fanIn || a.file.localeCompare(b.file),
    fanOut: (a, b) => b.fanOut - a.fanOut || a.file.localeCompare(b.file),
    file: (a, b) => a.file.localeCompare(b.file),
  }[mode];
  const list = REPORT.internalFiles.filter((f) => matches(f.file)).slice().sort(cmp);
  const body = $('#file-table tbody');
  if (!list.length) {
    body.replaceChildren(el('tr', {}, el('td', { colspan: '5', class: 'empty', text: 'No files match the filter.' })));
    return;
  }
  body.replaceChildren(...list.map((f, i) =>
    el('tr', { class: 'clickable', onclick: () => openFile(f) },
      el('td', { class: 'num', text: i + 1 }),
      el('td', { class: 'path', text: f.file }),
      el('td', { class: 'num', text: f.imports }),
      el('td', { class: 'num', text: f.fanIn }),
      el('td', { class: 'num', text: f.fanOut }))));
}

function openFile(f) {
  const build = (needle) => {
    const sites = (f.sites || []).filter((s) => !needle || s.file.toLowerCase().includes(needle));
    if (!sites.length) return el('div', { class: 'empty', text: 'Not imported by any reachable file.' });
    return el('table', { class: 'data' },
      el('thead', {}, el('tr', {},
        el('th', { text: 'Imported by' }), el('th', { text: 'Line' }),
        el('th', { text: 'Kind' }), el('th', { text: 'Specifier' }), el('th', { text: 'Symbols' }))),
      el('tbody', {}, ...sites.map((s) => el('tr', {},
        el('td', { class: 'path', text: s.file }),
        el('td', { class: 'num', text: s.line }),
        el('td', {}, el('span', { class: 'tag', text: s.kind })),
        el('td', { class: 'path muted', text: s.specifier }),
        el('td', { class: 'path muted', text: prettySymbols(s.symbols) })))));
  };
  openDrawer(f.file, `imported ${f.imports} times by ${f.fanIn} files · imports ${f.fanOut} modules`,
    build, f.entryChain);
}

/* ---------- assets ---------- */

function renderAssets() {
  const list = (REPORT.assets || []).filter((a) => matches(a.name, a.depType));
  const body = $('#asset-table tbody');
  if (!list.length) {
    body.replaceChildren(el('tr', {}, el('td', { colspan: '4', class: 'empty', text: 'No style or asset imports.' })));
    return;
  }
  body.replaceChildren(...list.map((a) =>
    el('tr', { class: 'clickable', onclick: () => openPackage(a) },
      el('td', { class: 'name', text: a.name }),
      el('td', { class: 'num', text: a.imports }),
      el('td', { class: 'num', text: a.files }),
      el('td', {}, el('span', { class: 'tag', text: a.depType })))));
}

/* ---------- orphans / cycles / deps / warnings ---------- */

function renderOrphans() {
  const list = (REPORT.orphans || []).filter((o) => matches(o));
  const host = $('#orphan-list');
  host.replaceChildren(...(list.length
    ? list.map((o) => el('li', { text: o }))
    : [el('li', { class: 'empty', text: 'Every source file under src is reachable.' })]));
}

function renderCycles() {
  const list = (REPORT.cycles || []).filter((c) => matches(c.join(' ')));
  const host = $('#cycle-list');
  if (!list.length) {
    host.replaceChildren(el('div', { class: 'empty', text: 'No circular dependencies found.' }));
    return;
  }
  host.replaceChildren(...list.map((c, i) =>
    el('div', { class: 'block' },
      el('h4', { text: `Cycle ${i + 1} — ${c.length} modules` }),
      el('div', { class: 'cycle-path' }, ...c.flatMap((f, idx) =>
        [el('span', { text: idx ? ' → ' : '' }), document.createTextNode(f)])))));
}

function renderDeps() {
  const unused = (REPORT.unusedDependencies || []).filter((d) => matches(d));
  const undeclared = (REPORT.undeclaredDependencies || []).filter((d) => matches(d));
  $('#unused-count').textContent = unused.length;
  $('#undeclared-count').textContent = undeclared.length;
  $('#unused-list').replaceChildren(...(unused.length
    ? unused.map((d) => el('li', { text: d }))
    : [el('li', { class: 'empty', text: 'Every declared dependency is imported.' })]));
  $('#undeclared-list').replaceChildren(...(undeclared.length
    ? undeclared.map((d) => el('li', { text: d }))
    : [el('li', { class: 'empty', text: 'No undeclared imports.' })]));
}

function renderWarnings() {
  const list = (REPORT.warnings || []).filter((w) => matches(w.file, w.specifier, w.hint, w.type));
  const body = $('#warn-table tbody');
  if (!list.length) {
    body.replaceChildren(el('tr', {}, el('td', { colspan: '5', class: 'empty', text: 'Every import resolved cleanly.' })));
    return;
  }
  body.replaceChildren(...list.map((w) =>
    el('tr', {},
      el('td', { class: 'path', text: w.file }),
      el('td', { class: 'num', text: w.line }),
      el('td', { class: 'path', text: w.specifier || '(dynamic)' }),
      el('td', {}, el('span', { class: 'tag', text: w.type })),
      el('td', { class: 'muted', text: w.hint || '' }))));
}

/* ---------- drawer ---------- */

let drawerBuilder = null;

function openDrawer(title, subtitle, builder, chain) {
  drawerBuilder = builder;
  $('#drawer-title').textContent = title;
  $('#drawer-sub').textContent = subtitle;
  renderChain(chain, title);
  $('#drawer-search').value = '';
  $('#drawer-body').replaceChildren(builder(''));
  $('#drawer').classList.add('open');
  $('#scrim').classList.add('open');
}

// renderChain shows the shortest import path from the entry, answering
// "why is this in my app?". The final hop is the subject itself, which is
// already the drawer title, so packages get it appended explicitly.
function renderChain(chain, subject) {
  const box = $('#drawer-chain');
  if (!chain || !chain.length) {
    box.replaceChildren();
    box.classList.remove('shown');
    return;
  }
  const hops = chain.slice();
  if (hops[hops.length - 1] !== subject) hops.push(subject);

  const nodes = [el('span', { class: 'chain-label', text: 'Reached via' })];
  hops.forEach((hop, i) => {
    if (i > 0) nodes.push(el('span', { class: 'chain-arrow', text: '→' }));
    nodes.push(el('span', {
      class: i === hops.length - 1 ? 'chain-hop last' : 'chain-hop',
      title: hop,
      text: shortPath(hop),
    }));
  });
  box.replaceChildren(...nodes);
  box.classList.add('shown');
}

// shortPath trims long module paths to their last two segments so a deep
// chain still fits on screen; the full path stays in the title attribute.
function shortPath(p) {
  const parts = String(p).split('/');
  return parts.length <= 2 ? p : '…/' + parts.slice(-2).join('/');
}

function closeDrawer() {
  $('#drawer').classList.remove('open');
  $('#scrim').classList.remove('open');
  drawerBuilder = null;
}

/* ---------- wiring ---------- */

$$('.tab').forEach((tab) => {
  tab.addEventListener('click', () => {
    $$('.tab').forEach((t) => t.classList.remove('active'));
    $$('.panel').forEach((p) => p.classList.remove('active'));
    tab.classList.add('active');
    $(`#panel-${tab.dataset.tab}`).classList.add('active');
  });
});

$('#global-search').addEventListener('input', (e) => {
  filter = e.target.value.trim().toLowerCase();
  renderAll();
});
$('#pkg-sort').addEventListener('change', renderPackages);
$('#hide-builtin').addEventListener('change', renderPackages);
$('#file-sort').addEventListener('change', renderFiles);
$('#reanalyze').addEventListener('click', reanalyze);
$('#drawer-close').addEventListener('click', closeDrawer);
$('#scrim').addEventListener('click', closeDrawer);
$('#drawer-search').addEventListener('input', (e) => {
  if (drawerBuilder) $('#drawer-body').replaceChildren(drawerBuilder(e.target.value.trim().toLowerCase()));
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') closeDrawer();
  if (document.activeElement.tagName === 'INPUT') return;
  if (e.key === '/') {
    e.preventDefault();
    $('#global-search').focus();
  }
  if (e.key === 'r' || e.key === 'R') {
    e.preventDefault();
    reanalyze();
  }
});

load().catch((err) => {
  document.body.append(el('div', { class: 'empty', text: `Failed to load report: ${err}` }));
});
