'use strict';

let REPORT = { projects: [], rows: [], driftCount: 0, sharedCount: 0, uniqueCount: 0 };
let sort = { key: 'default', dir: 'asc' };
const filters = { search: '', drift: false, shared: false, unique: false };

const $ = (sel) => document.querySelector(sel);

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const child of children) {
    if (child === null || child === undefined) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return node;
}

async function load() {
  const res = await fetch('/api/comparison');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  REPORT = normalizeReport(await res.json());
  render();
}

function normalizeReport(report) {
  const rep = report || {};
  return {
    generatedAt: rep.generatedAt || '',
    projects: Array.isArray(rep.projects) ? rep.projects.slice().sort((a, b) => textCmp(a.name, b.name)) : [],
    rows: Array.isArray(rep.rows) ? rep.rows.slice() : [],
    driftCount: Number(rep.driftCount) || 0,
    sharedCount: Number(rep.sharedCount) || 0,
    uniqueCount: Number(rep.uniqueCount) || 0,
  };
}

function render() {
  renderHeader();
  renderCards();
  renderFailures();
  renderMatrix();
}

function renderHeader() {
  const count = REPORT.projects.length;
  const when = formatTimestamp(REPORT.generatedAt);
  $('#subtitle').textContent = count ? `${count} project${count === 1 ? '' : 's'} compared · ${when}` : 'No projects compared';
}

function formatTimestamp(iso) {
  if (!iso) return 'not generated yet';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function card(value, label, cls) {
  return el('div', { class: `card ${cls || ''}` },
    el('div', { class: 'value', text: String(value) }),
    el('div', { class: 'label', text: label }));
}

function renderCards() {
  const distinct = REPORT.rows.length;
  $('#cards').replaceChildren(
    card(REPORT.projects.length, 'projects compared'),
    card(distinct, 'distinct packages'),
    card(REPORT.driftCount, 'version drift', REPORT.driftCount ? 'danger' : 'good'),
    card(REPORT.sharedCount, 'shared core'),
    card(REPORT.uniqueCount, 'unique to one app', REPORT.uniqueCount ? 'warn' : 'good'),
  );
}

function renderFailures() {
  const failed = REPORT.projects.filter((p) => p.error);
  const box = $('#failures');
  const list = $('#failure-list');
  box.hidden = failed.length === 0;
  list.replaceChildren(...failed.map((p) =>
    el('li', {}, el('span', { class: 'name', text: p.name || '(unnamed project)' }), document.createTextNode(` — ${p.error}`))));
}

function okProjects() {
  return REPORT.projects.filter((p) => !p.error).map((p) => p.name);
}

function rowSeverity(row) {
  if (!row || !row.drift) return 'none';
  const majors = new Set();
  const minors = new Set();
  for (const version of row.versions || []) {
    const sem = parseSemver(version);
    if (sem) {
      majors.add(sem.major);
      minors.add(`${sem.major}.${sem.minor}`);
    }
  }
  if (majors.size > 1) return 'major';
  if (minors.size > 1 || (row.versions || []).length > 1) return 'minor';
  return 'minor';
}

function parseSemver(value) {
  if (!value) return null;
  const m = String(value).trim().match(/^[^0-9]*(\d+)(?:\.(\d+))?(?:\.(\d+))?/);
  if (!m) return null;
  return { major: Number(m[1]), minor: Number(m[2] || 0), patch: Number(m[3] || 0) };
}

function renderMatrix() {
  const projects = REPORT.projects.slice();
  renderHead(projects);
  const rows = filteredRows(projects);
  const body = $('#matrix-body');
  $('#matrix-note').textContent = `${rows.length} of ${REPORT.rows.length} packages shown. Drifting rows sort first by default.`;
  if (!rows.length) {
    body.replaceChildren(el('tr', {}, el('td', { class: 'empty', colspan: String(projects.length + 3), text: emptyText() })));
    return;
  }
  body.replaceChildren(...rows.map((row) => renderRow(row, projects)));
}

function renderHead(projects) {
  const head = $('#matrix-head');
  const cells = [
    el('th', { class: 'sticky-col' }, sortButton('package', 'Package')),
    el('th', { class: 'num' }, sortButton('used', 'Used')),
    el('th', {}, sortButton('drift', 'Drift')),
  ];
  for (const p of projects) cells.push(el('th', {}, sortButton(`project:${p.name}`, p.name || '(unnamed)')));
  head.replaceChildren(el('tr', {}, ...cells));
}

function sortButton(key, label) {
  const active = sort.key === key;
  const suffix = active ? (sort.dir === 'asc' ? ' ↑' : ' ↓') : '';
  return el('button', { class: `sort-btn ${active ? 'active' : ''}`, type: 'button', onclick: () => setSort(key) }, `${label}${suffix}`);
}

function setSort(key) {
  if (sort.key === key) sort.dir = sort.dir === 'asc' ? 'desc' : 'asc';
  else sort = { key, dir: key === 'package' ? 'asc' : 'desc' };
  renderMatrix();
}

function filteredRows(projects) {
  const successfulCount = okProjects().length;
  let rows = REPORT.rows.filter((row) => {
    if (filters.search && !String(row.package || '').toLowerCase().includes(filters.search)) return false;
    if (filters.drift && !row.drift) return false;
    if (filters.shared && (!successfulCount || row.used !== successfulCount)) return false;
    if (filters.unique && row.used !== 1) return false;
    return true;
  });
  rows = rows.slice().sort((a, b) => compareRows(a, b, projects));
  return rows;
}

function compareRows(a, b, projects) {
  const dir = sort.dir === 'asc' ? 1 : -1;
  let result = 0;
  if (sort.key === 'default') result = defaultCompare(a, b);
  else if (sort.key === 'package') result = textCmp(a.package, b.package) * dir;
  else if (sort.key === 'used') result = numericCmp(a.used, b.used, a.package, b.package) * dir;
  else if (sort.key === 'drift') result = severityRank(rowSeverity(a)) - severityRank(rowSeverity(b));
  else if (sort.key.startsWith('project:')) {
    const name = sort.key.slice('project:'.length);
    result = numericCmp(cellImports(a, name), cellImports(b, name), a.package, b.package) * dir;
  }
  if (result !== 0) return result;
  return defaultCompare(a, b);
}

function defaultCompare(a, b) {
  const sev = severityRank(rowSeverity(b)) - severityRank(rowSeverity(a));
  if (sev !== 0) return sev;
  if (Boolean(a.drift) !== Boolean(b.drift)) return a.drift ? -1 : 1;
  if ((a.used || 0) !== (b.used || 0)) return (b.used || 0) - (a.used || 0);
  if ((a.imports || 0) !== (b.imports || 0)) return (b.imports || 0) - (a.imports || 0);
  return textCmp(a.package, b.package);
}

function severityRank(sev) {
  if (sev === 'major') return 2;
  if (sev === 'minor') return 1;
  return 0;
}

function numericCmp(a, b, an, bn) {
  if ((a || 0) !== (b || 0)) return (a || 0) - (b || 0);
  return textCmp(an, bn);
}

function textCmp(a, b) {
  return String(a || '').localeCompare(String(b || ''), undefined, { sensitivity: 'base' });
}

function cellImports(row, project) {
  const cell = row.cells && row.cells[project];
  return cell && cell.present ? Number(cell.imports) || 0 : -1;
}

function renderRow(row, projects) {
  const sev = rowSeverity(row);
  const cls = sev === 'major' ? 'drift-major' : sev === 'minor' ? 'drift-minor' : '';
  const cells = [
    el('td', { class: 'sticky-col' },
      el('div', { class: 'name', text: row.package || '(unnamed package)' }),
      row.drift ? el('span', { class: `badge ${sev}`, text: sev === 'major' ? 'major drift' : 'version drift' }) : null),
    el('td', { class: 'num', text: `${row.used || 0}` }),
    el('td', {}, row.drift ? el('span', { class: `badge ${sev}`, text: (row.versions || []).join(' vs ') }) : el('span', { class: 'badge', text: 'stable' })),
  ];
  for (const project of projects) cells.push(renderCell(row, project.name));
  return el('tr', { class: cls, 'data-package': row.package || '', 'data-drift': row.drift ? 'true' : 'false', 'data-severity': sev, 'data-used': String(row.used || 0) }, ...cells);
}

function renderCell(row, projectName) {
  const cell = row.cells && row.cells[projectName];
  if (!cell || !cell.present) return el('td', {}, el('span', { class: 'not-used', text: 'not used' }));
  return el('td', {},
    el('div', { class: 'version', text: cell.version || '(undeclared)' }),
    el('div', { class: 'imports', text: `${cell.imports || 0} import${cell.imports === 1 ? '' : 's'} · ${cell.files || 0} file${cell.files === 1 ? '' : 's'}` }));
}

function emptyText() {
  if (!REPORT.projects.length) return 'No projects to compare.';
  if (!REPORT.rows.length) return 'No package imports found.';
  return 'No packages match the current filters.';
}

function bindControls() {
  $('#search').addEventListener('input', (e) => { filters.search = e.target.value.trim().toLowerCase(); renderMatrix(); });
  $('#drift-only').addEventListener('change', (e) => { filters.drift = e.target.checked; renderMatrix(); });
  $('#shared-only').addEventListener('change', (e) => { filters.shared = e.target.checked; renderMatrix(); });
  $('#unique-only').addEventListener('change', (e) => { filters.unique = e.target.checked; renderMatrix(); });
}

bindControls();
load().catch((err) => {
  $('#subtitle').textContent = `Failed to load comparison: ${err.message}`;
  $('#matrix-body').replaceChildren(el('tr', {}, el('td', { class: 'empty', text: 'Could not load comparison data.' })));
});
