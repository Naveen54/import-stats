'use strict';

(() => {
  const canvas = document.getElementById('graph-canvas');
  if (!canvas) return;

  const ctx = canvas.getContext('2d');
  const wrap = document.getElementById('graph-wrap');
  const tooltip = document.getElementById('graph-tooltip');
  const collapseInput = document.getElementById('graph-collapse');
  const resetButton = document.getElementById('graph-reset');
  const css = getComputedStyle(document.documentElement);
  const paletteVars = ['--accent', '--accent-2', '--accent-3', '--accent-4', '--warn', '--danger'];
  const palette = paletteVars.map((v) => css.getPropertyValue(v).trim()).filter(Boolean);
  const colors = {
    text: css.getPropertyValue('--text').trim() || '#e6edf3',
    muted: css.getPropertyValue('--muted').trim() || '#8b98a9',
    line: css.getPropertyValue('--line').trim() || '#29313d',
    panel: css.getPropertyValue('--panel').trim() || '#151b23',
    accent: css.getPropertyValue('--accent').trim() || '#58a6ff',
  };

  let graph = { nodes: [], edges: [], byId: new Map(), files: new Map() };
  let signature = '';
  let expandedDir = '';
  let transform = { x: 0, y: 0, scale: 1 };
  let raf = 0;
  let running = false;
  let settled = true;
  let tickCount = 0;
  let hover = null;
  let pointer = null;
  let drag = null;
  let lastReport = null;

  const MAX_TICKS = 900;
  const SETTLE_ENERGY = 0.025;

  function reportValue() {
    try {
      return REPORT || null;
    } catch (_) {
      return null;
    }
  }

  function isVisible() {
    const panel = document.getElementById('panel-graph');
    return !!panel && panel.classList.contains('active');
  }

  function hashString(s) {
    let h = 2166136261;
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    return h >>> 0;
  }

  function mulberry32(seed) {
    let a = seed >>> 0;
    return () => {
      a = (a + 0x6D2B79F5) >>> 0;
      let t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  function collapsePath(path) {
    const parts = String(path || '').replace(/^\/+|\/+$/g, '').split('/').filter(Boolean);
    if (!parts.length) return '';
    if (parts.length === 1) return parts[0];
    if (parts.length === 2 && parts[1].includes('.')) return parts[0];
    return `${parts[0]}/${parts[1]}`;
  }

  function topDir(path) {
    const id = collapsePath(path);
    return id || String(path || 'root');
  }

  function shortLabel(path) {
    const parts = String(path || '').split('/');
    if (parts.length <= 3) return path;
    return `…/${parts.slice(-2).join('/')}`;
  }

  function colorFor(name) {
    if (!palette.length) return colors.accent;
    return palette[hashString(String(name)) % palette.length];
  }

  function nodeIdFor(file, collapsed) {
    const dir = collapsePath(file);
    if (!collapsed) return file;
    if (expandedDir && dir === expandedDir) return file;
    return dir || file;
  }

  function buildGraph(report, preservePositions) {
    const files = Array.isArray(report && report.internalFiles) ? report.internalFiles : [];
    const collapsed = !!collapseInput.checked;
    const nextSignature = JSON.stringify({
      collapsed,
      expandedDir,
      entry: report && report.summary && report.summary.entry,
      files: files.map((f) => [f.file, f.fanIn, f.fanOut, (f.sites || []).map((s) => s.file)]),
    });
    if (nextSignature === signature && report === lastReport) return;

    const old = preservePositions ? graph.byId : new Map();
    const nodes = new Map();
    const edges = new Map();
    const fileMap = new Map();
    const collapsedFiles = new Map();
    const entry = report && report.summary ? report.summary.entry : '';

    function ensureNode(id, label, file, isCollapsed, isEntry) {
      if (!id) return null;
      let n = nodes.get(id);
      if (!n) {
        const kept = old.get(id);
        n = {
          id,
          label,
          path: id,
          file: isCollapsed ? '' : file,
          collapsed: isCollapsed,
          entry: false,
          fileCount: 0,
          fanIn: 0,
          fanOut: 0,
          imports: 0,
          radius: 6,
          color: colorFor(topDir(id)),
          x: kept && Number.isFinite(kept.x) ? kept.x : 0,
          y: kept && Number.isFinite(kept.y) ? kept.y : 0,
          vx: 0,
          vy: 0,
          fx: kept && Number.isFinite(kept.fx) ? kept.fx : null,
          fy: kept && Number.isFinite(kept.fy) ? kept.fy : null,
        };
        nodes.set(id, n);
      }
      n.entry = n.entry || !!isEntry;
      return n;
    }

    function addEdge(from, to) {
      if (!from || !to || from === to) return;
      const key = `${from}\u0000${to}`;
      let e = edges.get(key);
      if (!e) {
        e = { from, to, weight: 0 };
        edges.set(key, e);
      }
      e.weight++;
    }

    for (const f of files) {
      if (!f || !f.file) continue;
      fileMap.set(f.file, f);
      const id = nodeIdFor(f.file, collapsed);
      const isCollapsed = collapsed && id !== f.file;
      const n = ensureNode(id, id, f.file, isCollapsed, id === nodeIdFor(entry, collapsed));
      if (n) {
        n.fileCount += isCollapsed ? (collapsedFiles.has(`${id}\u0000${f.file}`) ? 0 : 1) : 1;
        collapsedFiles.set(`${id}\u0000${f.file}`, true);
        n.fanIn += Number(f.fanIn) || 0;
        n.fanOut += Number(f.fanOut) || 0;
        n.imports += Number(f.imports) || 0;
      }
      for (const site of f.sites || []) {
        if (!site || !site.file) continue;
        const from = nodeIdFor(site.file, collapsed);
        const fromCollapsed = collapsed && from !== site.file;
        const fromNode = ensureNode(from, from, site.file, fromCollapsed, from === nodeIdFor(entry, collapsed));
        if (fromNode && fromCollapsed && !collapsedFiles.has(`${from}\u0000${site.file}`)) {
          fromNode.fileCount++;
          collapsedFiles.set(`${from}\u0000${site.file}`, true);
        }
        addEdge(from, id);
      }
    }

    if (entry) {
      const id = nodeIdFor(entry, collapsed);
      ensureNode(id, id, entry, collapsed && id !== entry, true);
    }

    const nodeList = Array.from(nodes.values()).sort((a, b) => a.id.localeCompare(b.id));
    const edgeList = Array.from(edges.values())
      .filter((e) => nodes.has(e.from) && nodes.has(e.to))
      .sort((a, b) => a.from.localeCompare(b.from) || a.to.localeCompare(b.to));
    const maxSize = Math.max(1, ...nodeList.map((n) => n.collapsed ? n.fileCount : n.fanIn));
    const seed = hashString(`${report && report.summary ? report.summary.projectName : ''}|${entry}|${nodeList.map((n) => n.id).join('|')}|${edgeList.map((e) => `${e.from}>${e.to}:${e.weight}`).join('|')}`);
    const rand = mulberry32(seed || 1);
    const angleStep = nodeList.length ? (Math.PI * 2) / nodeList.length : 0;
    const baseRadius = Math.max(80, Math.min(520, 35 * Math.sqrt(Math.max(1, nodeList.length))));

    nodeList.forEach((n, i) => {
      const sizeMetric = n.collapsed ? n.fileCount : n.fanIn;
      n.radius = 6 + 16 * Math.sqrt(Math.max(0, sizeMetric) / maxSize);
      if (!Number.isFinite(n.x) || !Number.isFinite(n.y) || (!preservePositions && !old.has(n.id))) {
        const a = i * angleStep + rand() * 0.35;
        const r = baseRadius * (0.45 + rand() * 0.7);
        n.x = Math.cos(a) * r;
        n.y = Math.sin(a) * r;
      }
    });

    graph = { nodes: nodeList, edges: edgeList, byId: new Map(nodeList.map((n) => [n.id, n])), files: fileMap };
    signature = nextSignature;
    lastReport = report;
    tickCount = 0;
    fitGraph();
    reheat();
  }

  function resizeCanvas() {
    const rect = wrap.getBoundingClientRect();
    const ratio = Math.max(1, window.devicePixelRatio || 1);
    const w = Math.max(1, Math.floor(rect.width * ratio));
    const h = Math.max(1, Math.floor(rect.height * ratio));
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w;
      canvas.height = h;
      canvas.style.width = `${rect.width}px`;
      canvas.style.height = `${rect.height}px`;
      draw();
    }
  }

  function fitGraph() {
    resizeCanvas();
    const rect = wrap.getBoundingClientRect();
    transform.x = rect.width / 2;
    transform.y = rect.height / 2;
    transform.scale = graph.nodes.length > 80 ? 0.72 : 0.9;
  }

  function screenToWorld(x, y) {
    const s = transform.scale || 1;
    return { x: (x - transform.x) / s, y: (y - transform.y) / s };
  }

  function worldToScreen(x, y) {
    return { x: x * transform.scale + transform.x, y: y * transform.scale + transform.y };
  }

  function simulationTick() {
    const nodes = graph.nodes;
    const n = nodes.length;
    if (n <= 1) {
      for (const node of nodes) {
        if (!Number.isFinite(node.x)) node.x = 0;
        if (!Number.isFinite(node.y)) node.y = 0;
      }
      tickCount = MAX_TICKS;
      return 0;
    }

    const charge = 4600;
    const spring = 0.012;
    const center = 0.004;
    const damping = 0.84;
    const edgeDistance = Math.max(80, Math.min(170, 2600 / Math.sqrt(n)));

    for (let i = 0; i < n; i++) {
      const a = nodes[i];
      for (let j = i + 1; j < n; j++) {
        const b = nodes[j];
        let dx = a.x - b.x;
        let dy = a.y - b.y;
        let dist2 = dx * dx + dy * dy;
        if (!Number.isFinite(dist2) || dist2 < 0.01) {
          const jitter = ((i + 1) * 928371 + (j + 1) * 364479) % 1000 / 1000;
          dx = Math.cos(jitter * Math.PI * 2) * 0.1;
          dy = Math.sin(jitter * Math.PI * 2) * 0.1;
          dist2 = dx * dx + dy * dy;
        }
        const dist = Math.sqrt(dist2);
        const force = charge / Math.max(25, dist2);
        const fx = (dx / dist) * force;
        const fy = (dy / dist) * force;
        if (a.fx === null) { a.vx += fx; a.vy += fy; }
        if (b.fx === null) { b.vx -= fx; b.vy -= fy; }
      }
    }

    for (const e of graph.edges) {
      const a = graph.byId.get(e.from);
      const b = graph.byId.get(e.to);
      if (!a || !b) continue;
      let dx = b.x - a.x;
      let dy = b.y - a.y;
      let dist = Math.sqrt(dx * dx + dy * dy);
      if (!Number.isFinite(dist) || dist < 0.001) {
        dx = 0.001;
        dy = 0;
        dist = 0.001;
      }
      const wanted = edgeDistance + a.radius + b.radius;
      const force = (dist - wanted) * spring * Math.min(4, Math.max(1, e.weight));
      const fx = (dx / dist) * force;
      const fy = (dy / dist) * force;
      if (a.fx === null) { a.vx += fx; a.vy += fy; }
      if (b.fx === null) { b.vx -= fx; b.vy -= fy; }
    }

    let energy = 0;
    for (const node of nodes) {
      if (node.fx !== null) {
        node.x = node.fx;
        node.y = node.fy;
        node.vx = 0;
        node.vy = 0;
        continue;
      }
      node.vx = (node.vx - node.x * center) * damping;
      node.vy = (node.vy - node.y * center) * damping;
      if (!Number.isFinite(node.vx)) node.vx = 0;
      if (!Number.isFinite(node.vy)) node.vy = 0;
      node.x += Math.max(-18, Math.min(18, node.vx));
      node.y += Math.max(-18, Math.min(18, node.vy));
      if (!Number.isFinite(node.x)) node.x = 0;
      if (!Number.isFinite(node.y)) node.y = 0;
      energy += node.vx * node.vx + node.vy * node.vy;
    }
    tickCount++;
    return energy / n;
  }

  function frame() {
    raf = 0;
    if (!running || !isVisible()) {
      running = false;
      return;
    }
    let energy = 0;
    for (let i = 0; i < 3; i++) energy = simulationTick();
    draw();
    if (tickCount >= MAX_TICKS || energy < SETTLE_ENERGY) {
      running = false;
      settled = true;
      return;
    }
    raf = requestAnimationFrame(frame);
  }

  function reheat() {
    if (!isVisible()) {
      draw();
      return;
    }
    settled = false;
    running = true;
    if (!raf) raf = requestAnimationFrame(frame);
  }

  function drawArrow(from, to, weight) {
    const dx = to.x - from.x;
    const dy = to.y - from.y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (!Number.isFinite(dist) || dist < 0.001) return;
    const ux = dx / dist;
    const uy = dy / dist;
    const startX = from.x + ux * from.radius;
    const startY = from.y + uy * from.radius;
    const endX = to.x - ux * (to.radius + 4);
    const endY = to.y - uy * (to.radius + 4);
    ctx.lineWidth = Math.min(3.5, 0.5 + Math.log1p(weight));
    ctx.strokeStyle = 'rgba(139,152,169,.34)';
    ctx.beginPath();
    ctx.moveTo(startX, startY);
    ctx.lineTo(endX, endY);
    ctx.stroke();

    const size = 5 + Math.min(4, weight);
    ctx.fillStyle = 'rgba(139,152,169,.62)';
    ctx.beginPath();
    ctx.moveTo(endX, endY);
    ctx.lineTo(endX - ux * size - uy * size * 0.55, endY - uy * size + ux * size * 0.55);
    ctx.lineTo(endX - ux * size + uy * size * 0.55, endY - uy * size - ux * size * 0.55);
    ctx.closePath();
    ctx.fill();
  }

  function draw() {
    resizeCanvas();
    const ratio = Math.max(1, window.devicePixelRatio || 1);
    const width = canvas.width / ratio;
    const height = canvas.height / ratio;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);

    if (!graph.nodes.length) {
      ctx.fillStyle = colors.muted;
      ctx.font = '13px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
      ctx.fillText('No internal module graph to display.', 24, 36);
      return;
    }

    ctx.save();
    ctx.translate(transform.x, transform.y);
    ctx.scale(transform.scale, transform.scale);

    for (const e of graph.edges) {
      const from = graph.byId.get(e.from);
      const to = graph.byId.get(e.to);
      if (from && to) drawArrow(from, to, e.weight);
    }

    for (const n of graph.nodes) {
      ctx.beginPath();
      ctx.arc(n.x, n.y, n.radius, 0, Math.PI * 2);
      ctx.fillStyle = n.color;
      ctx.globalAlpha = n === hover ? 1 : 0.88;
      ctx.fill();
      ctx.globalAlpha = 1;
      ctx.lineWidth = n.entry ? 3 : (n.fx ? 2 : 1);
      ctx.strokeStyle = n.entry ? '#ffffff' : (n.fx ? colors.accent : colors.panel);
      ctx.stroke();
    }

    const labelCutoff = transform.scale < 0.48 ? 18 : 8;
    ctx.font = `${Math.max(10, Math.min(13, 11 / Math.sqrt(transform.scale)))}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    for (const n of graph.nodes) {
      if (n.radius < labelCutoff && n !== hover) continue;
      const p = worldToScreen(n.x, n.y);
      if (p.x < -80 || p.y < -30 || p.x > width + 80 || p.y > height + 30) continue;
      ctx.fillStyle = n === hover ? colors.text : 'rgba(230,237,243,.78)';
      ctx.fillText(shortLabel(n.label), n.x, n.y + n.radius + 4);
    }
    ctx.restore();
  }

  function nodeAt(clientX, clientY) {
    const rect = canvas.getBoundingClientRect();
    const p = screenToWorld(clientX - rect.left, clientY - rect.top);
    for (let i = graph.nodes.length - 1; i >= 0; i--) {
      const n = graph.nodes[i];
      const dx = p.x - n.x;
      const dy = p.y - n.y;
      if (dx * dx + dy * dy <= Math.pow(n.radius + 5, 2)) return n;
    }
    return null;
  }

  function updateTooltip(node, clientX, clientY) {
    if (!node) {
      tooltip.style.display = 'none';
      return;
    }
    tooltip.innerHTML = `<div class="graph-tip-title"></div><div class="graph-tip-meta"></div>`;
    tooltip.querySelector('.graph-tip-title').textContent = node.label;
    tooltip.querySelector('.graph-tip-meta').textContent =
      `${node.collapsed ? `${node.fileCount} files · ` : ''}fan-in ${node.fanIn} · fan-out ${node.fanOut}` +
      (node.collapsed ? ' · click to drill in' : ' · click for details');
    const rect = wrap.getBoundingClientRect();
    tooltip.style.left = `${Math.min(rect.width - 20, clientX - rect.left + 14)}px`;
    tooltip.style.top = `${Math.max(10, clientY - rect.top + 14)}px`;
    tooltip.style.display = 'block';
  }

  canvas.addEventListener('pointerdown', (e) => {
    if (!isVisible()) return;
    canvas.setPointerCapture(e.pointerId);
    const rect = canvas.getBoundingClientRect();
    const node = nodeAt(e.clientX, e.clientY);
    pointer = { x: e.clientX, y: e.clientY, sx: e.clientX - rect.left, sy: e.clientY - rect.top, moved: false };
    if (node) {
      const world = screenToWorld(e.clientX - rect.left, e.clientY - rect.top);
      drag = { type: 'node', node, dx: node.x - world.x, dy: node.y - world.y };
      node.fx = node.x;
      node.fy = node.y;
    } else {
      drag = { type: 'pan', x: transform.x, y: transform.y };
    }
    canvas.classList.add('dragging');
  });

  canvas.addEventListener('pointermove', (e) => {
    if (!isVisible()) return;
    const nextHover = nodeAt(e.clientX, e.clientY);
    if (nextHover !== hover) {
      hover = nextHover;
      draw();
    }
    updateTooltip(hover, e.clientX, e.clientY);
    if (!drag || !pointer) return;
    const dx = e.clientX - pointer.x;
    const dy = e.clientY - pointer.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) pointer.moved = true;
    if (drag.type === 'node') {
      const rect = canvas.getBoundingClientRect();
      const world = screenToWorld(e.clientX - rect.left, e.clientY - rect.top);
      drag.node.fx = world.x + drag.dx;
      drag.node.fy = world.y + drag.dy;
      drag.node.x = drag.node.fx;
      drag.node.y = drag.node.fy;
      reheat();
    } else {
      transform.x = drag.x + dx;
      transform.y = drag.y + dy;
      draw();
    }
  });

  canvas.addEventListener('pointerup', (e) => {
    canvas.classList.remove('dragging');
    const clicked = drag && drag.type === 'node' && pointer && !pointer.moved ? drag.node : null;
    drag = null;
    pointer = null;
    if (clicked) {
      if (clicked.collapsed) {
        expandedDir = clicked.id;
        buildGraph(reportValue(), true);
      } else if (typeof openFile === 'function') {
        const f = graph.files.get(clicked.file || clicked.id);
        if (f) openFile(f);
      }
    }
  });

  canvas.addEventListener('pointercancel', () => {
    canvas.classList.remove('dragging');
    drag = null;
    pointer = null;
  });

  canvas.addEventListener('mouseleave', () => {
    hover = null;
    updateTooltip(null);
    draw();
  });

  canvas.addEventListener('wheel', (e) => {
    if (!isVisible()) return;
    e.preventDefault();
    const rect = canvas.getBoundingClientRect();
    const before = screenToWorld(e.clientX - rect.left, e.clientY - rect.top);
    const factor = Math.exp(-e.deltaY * 0.001);
    transform.scale = Math.max(0.18, Math.min(3.2, transform.scale * factor));
    transform.x = e.clientX - rect.left - before.x * transform.scale;
    transform.y = e.clientY - rect.top - before.y * transform.scale;
    draw();
  }, { passive: false });

  collapseInput.addEventListener('change', () => {
    expandedDir = '';
    buildGraph(reportValue(), false);
  });

  resetButton.addEventListener('click', () => {
    for (const n of graph.nodes) {
      n.fx = null;
      n.fy = null;
    }
    signature = '';
    buildGraph(reportValue(), false);
  });

  document.querySelectorAll('.tab').forEach((tab) => {
    tab.addEventListener('click', () => {
      if (tab.dataset.tab === 'graph') {
        buildGraph(reportValue(), true);
        resizeCanvas();
        reheat();
      } else {
        running = false;
      }
    });
  });

  window.addEventListener('resize', () => {
    if (isVisible()) {
      resizeCanvas();
      draw();
    }
  });

  if (window.ResizeObserver) {
    new ResizeObserver(() => {
      if (isVisible()) {
        resizeCanvas();
        draw();
      }
    }).observe(wrap);
  }

  setInterval(() => {
    const report = reportValue();
    if (report && report !== lastReport && isVisible()) buildGraph(report, true);
  }, 800);

  window.__IMPORTSTATS_GRAPH__ = {
    state: () => ({
      nodes: graph.nodes.map((n) => ({ id: n.id, x: n.x, y: n.y, fx: n.fx, fy: n.fy, collapsed: n.collapsed })),
      edges: graph.edges.length,
      running,
      settled,
      ticks: tickCount,
      expandedDir,
      transform: { ...transform },
    }),
    rebuild: () => buildGraph(reportValue(), false),
    settle: () => new Promise((resolve) => {
      const wait = () => {
        if (!running || settled || tickCount >= MAX_TICKS) resolve(window.__IMPORTSTATS_GRAPH__.state());
        else requestAnimationFrame(wait);
      };
      wait();
    }),
  };
})();
