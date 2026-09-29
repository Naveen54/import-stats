'use strict';

// tree.js renders the "Component tree" tab as a real top-down flowchart —
// boxes connected by elbow connectors, laid out level by level from the
// entry point — rather than an indented file list. It intentionally mirrors
// the interaction model of the existing force-directed Graph tab (canvas,
// pan/zoom, hover tooltip, click-through to the shared detail drawer) so the
// two tabs feel like siblings, while the layout itself is a strict hierarchy
// instead of a physics simulation.
//
// Nothing here adds a server round-trip: the hierarchy is derived entirely
// from data the report already ships (`entryChain` gives each file's
// shortest-path parent, `fanIn` says how many other files also import it).
// The one new signal is `isComponent`, a best-effort heuristic computed
// server-side (JSX presence or a directly-declared PascalCase export) —
// see README for its documented limitations.
(() => {
  const canvas = document.getElementById('tree-canvas');
  if (!canvas) return;

  const ctx = canvas.getContext('2d');
  const wrap = document.getElementById('tree-wrap');
  const tooltip = document.getElementById('tree-tooltip');
  const onlyToggle = document.getElementById('tree-components-only');
  const expandAllBtn = document.getElementById('tree-expand-all');
  const collapseAllBtn = document.getElementById('tree-collapse-all');
  const resetBtn = document.getElementById('tree-reset');
  const hideUnrelatedToggle = document.getElementById('tree-hide-unrelated');
  const clearHighlightBtn = document.getElementById('tree-clear-highlight');
  const note = document.getElementById('tree-note');
  const searchInput = document.getElementById('tree-search');
  const searchResults = document.getElementById('tree-search-results');
  const rootChip = document.getElementById('tree-root-chip');
  const rootChipFile = document.getElementById('tree-root-chip-file');
  const rootClearBtn = document.getElementById('tree-root-clear');

  const css = getComputedStyle(document.documentElement);
  const colors = {
    text: css.getPropertyValue('--text').trim() || '#e6edf3',
    muted: css.getPropertyValue('--muted').trim() || '#8b98a9',
    line: css.getPropertyValue('--line').trim() || '#29313d',
    panel: css.getPropertyValue('--panel').trim() || '#151b23',
    panel2: css.getPropertyValue('--panel-2').trim() || '#1c232d',
    accent: css.getPropertyValue('--accent').trim() || '#58a6ff',
    accent3: css.getPropertyValue('--accent-3').trim() || '#bc8cff',
    accent4: css.getPropertyValue('--accent-4').trim() || '#39c5cf',
    warn: css.getPropertyValue('--warn').trim() || '#d29922',
  };

  const NODE_W = 190;
  const NODE_H = 46;
  const H_GAP = 26;
  const V_GAP = 64;
  const DEFAULT_EXPAND_DEPTH = 5;

  let nodes = [];        // flattened, visible nodes only (post-layout)
  let byFile = new Map(); // file -> node, visible nodes only
  let collapsedState = new Map(); // file -> user-set collapsed override
  let componentsOnly = true;
  let rootOverride = null; // file path chosen via search, or null for the entry
  let showAncestors = false; // reveal rootOverride's own ancestor chain up to the entry
  let rootHasAncestors = false; // whether the current root has a non-empty ancestor chain
  let highlightedFile = null;
  let highlightedFlow = new Set();
  let trueEntryFile = null;
  let currentRootFile = null;
  let lastReport = null;
  let lastSignature = '';
  let transform = { x: 0, y: 0, scale: 1 };
  let hover = null;
  let pointer = null;
  let drag = null;

  function reportValue() {
    try {
      return REPORT || null;
    } catch (_) {
      return null;
    }
  }

  function isVisible() {
    const panel = document.getElementById('panel-tree');
    return !!panel && panel.classList.contains('active');
  }

  function findEntry(files) {
    return files.find((f) => f.entryChain && f.entryChain.length === 1 && f.entryChain[0] === f.file) || null;
  }

  // primaryParent is the second-to-last hop of a file's shortest chain from
  // the entry — the "why-here" parent already computed by the analyzer.
  function primaryParent(f) {
    const chain = f.entryChain;
    if (!chain || chain.length < 2) return null;
    return chain[chain.length - 2];
  }

  // componentParent walks a file's chain upward past any non-component
  // ancestors, so filtering to "components only" re-attaches a component's
  // descendants to its nearest component (or the entry) instead of
  // fragmenting the tree when an intermediate util/page wrapper is hidden.
  function componentParent(f, byPath, entryFile) {
    const chain = f.entryChain;
    if (!chain || chain.length < 2) return entryFile ? entryFile.file : null;
    for (let i = chain.length - 2; i >= 1; i--) {
      const p = byPath.get(chain[i]);
      if (p && p.isComponent) return chain[i];
    }
    return chain[0];
  }

  function buildModel(report) {
    const files = (report && report.internalFiles) || [];
    const byPath = new Map(files.map((f) => [f.file, f]));
    const entryFile = findEntry(files);
    const componentCount = files.filter((f) => f.isComponent).length;

    const childrenAll = new Map();
    const childrenComponents = new Map();
    for (const f of files) {
      if (entryFile && f.file === entryFile.file) continue;
      const p = primaryParent(f);
      if (p) {
        if (!childrenAll.has(p)) childrenAll.set(p, []);
        childrenAll.get(p).push(f);
      }
      if (f.isComponent) {
        const cp = componentParent(f, byPath, entryFile);
        if (cp) {
          if (!childrenComponents.has(cp)) childrenComponents.set(cp, []);
          childrenComponents.get(cp).push(f);
        }
      }
    }
    const byName = (a, b) => a.file.localeCompare(b.file);
    for (const list of childrenAll.values()) list.sort(byName);
    for (const list of childrenComponents.values()) list.sort(byName);

    return { byPath, entryFile, componentCount, childrenAll, childrenComponents };
  }

  function baseName(path) {
    const parts = String(path).split('/');
    return parts[parts.length - 1];
  }

  // buildTree walks the model into a plain node tree (not yet laid out),
  // respecting each node's collapsed state. Guards against revisiting a
  // file within one root-to-leaf path so a data inconsistency can never
  // hang the browser. `rootFile` defaults to the entry, but any file can be
  // used as the root (via the search box) to view the tree from that point.
  function buildTree(model, mode, rootFile) {
    const childrenMap = mode === 'components' ? model.childrenComponents : model.childrenAll;

    function make(file, depth, parent, ancestors) {
      const fs = model.byPath.get(file) || null;
      const hasKidsData = (childrenMap.get(file) || []).length > 0;
      let collapsed = collapsedState.has(file) ? collapsedState.get(file) : depth >= DEFAULT_EXPAND_DEPTH;
      const node = { file, fs, depth, parent, collapsed, hasChildren: hasKidsData, children: [] };
      if (hasKidsData && !collapsed && !ancestors.has(file)) {
        const nextAncestors = new Set(ancestors);
        nextAncestors.add(file);
        for (const child of childrenMap.get(file)) {
          node.children.push(make(child.file, depth + 1, file, nextAncestors));
        }
      }
      return node;
    }

    const start = rootFile && model.byPath.has(rootFile) ? rootFile : (model.entryFile ? model.entryFile.file : null);
    if (!start) return null;
    return make(start, 0, null, new Set());
  }

  // ancestorChainOf walks upward from `file` to the true entry using the
  // same parent relationship the current mode already builds children
  // from, returning an ordered array of file paths from the entry down to
  // (but excluding) `file` itself. Used so a custom search-selected root
  // can also reveal the path *above* it, not just its own descendants.
  // A `seen` set is a defensive guard against a cyclic/inconsistent chain
  // ever hanging the browser (componentParent always returns a fallback
  // rather than null, so it cannot terminate on its own).
  function ancestorChainOf(model, mode, file) {
    const chain = [];
    const seen = new Set([file]);
    let current = file;
    if (mode === 'components') {
      const entryPath = model.entryFile ? model.entryFile.file : null;
      while (current !== entryPath) {
        const fs = model.byPath.get(current);
        if (!fs) break;
        const p = componentParent(fs, model.byPath, model.entryFile);
        if (!p || seen.has(p)) break;
        chain.push(p);
        seen.add(p);
        current = p;
      }
    } else {
      while (true) {
        const fs = model.byPath.get(current);
        if (!fs) break;
        const p = primaryParent(fs);
        if (!p || seen.has(p)) break;
        chain.push(p);
        seen.add(p);
        current = p;
      }
    }
    chain.reverse();
    return chain;
  }

  // layout assigns each node a center-x/top-y in world units via a simple
  // post-order tidy-tree pass: leaves/collapsed nodes claim one slot along
  // the cursor, internal nodes center over their own children.
  function layout(root) {
    let cursor = 0;
    const flat = [];

    function place(node) {
      node.y = node.depth * (NODE_H + V_GAP);
      if (!node.children.length) {
        node.x = cursor + NODE_W / 2;
        cursor += NODE_W + H_GAP;
      } else {
        for (const child of node.children) place(child);
        const first = node.children[0];
        const last = node.children[node.children.length - 1];
        node.x = (first.x + last.x) / 2;
      }
      flat.push(node);
    }

    place(root);
    return flat;
  }

  function signatureOf(report, mode) {
    return `${report && report.summary && report.summary.generatedAt}::${mode}::${rootOverride || ''}::${showAncestors}::${(report && report.internalFiles || []).length}`;
  }

  function rebuild(force) {
    const report = reportValue();
    if (!report) return false;
    const wantComponentsOnly = !!(onlyToggle && onlyToggle.checked);
    const model = buildModel(report);

    // A stale root (e.g. the file no longer exists after a re-analyse)
    // silently falls back to the entry rather than breaking the tab.
    if (rootOverride && !model.byPath.has(rootOverride)) {
      rootOverride = null;
      updateRootChip();
    }

    trueEntryFile = model.entryFile ? model.entryFile.file : null;

    let mode = wantComponentsOnly ? 'components' : 'all';
    if (wantComponentsOnly && model.componentCount === 0) {
      mode = 'all';
      if (onlyToggle) onlyToggle.checked = false;
      if (note) note.textContent = 'No React components were detected in this project (heuristic: JSX or a PascalCase export) — showing all reachable files instead.';
    }
    // Computed after `mode` is finalized above, since components-mode and
    // all-mode walk different parent relationships.
    const ancestorChain = rootOverride ? ancestorChainOf(model, mode, rootOverride) : [];
    rootHasAncestors = ancestorChain.length > 0;
    if (!(wantComponentsOnly && model.componentCount === 0) && note) {
      const rootedNote = rootOverride ? ` Rooted at ${rootOverride} — click "show full tree" above to reset.` : '';
      const rootedNoChildren = rootOverride && mode === 'components' && !model.childrenComponents.has(rootOverride) && !(model.byPath.get(rootOverride) || {}).isComponent
        ? ' The selected file has no detected component descendants — toggle "components only" off to see everything below it.'
        : '';
      const ancestorHint = rootHasAncestors
        ? ` Click the ▴/▾ handle above the root to ${showAncestors ? 'hide' : 'reveal'} its path back to the entry.`
        : '';
      note.textContent = (mode === 'components'
        ? 'Showing components only. A component reached through more than one path appears once, under its shortest-path parent, with a badge for the rest. Drag to pan, wheel to zoom, click a node to open it, click the ▾/▸ handle to expand or collapse.'
        : 'Showing every reachable file. A file reached through more than one path appears once, under its shortest-path parent, with a badge for the rest. Drag to pan, wheel to zoom, click a node to open it, click the ▾/▸ handle to expand or collapse.') + rootedNote + rootedNoChildren + ancestorHint;
    }

    const sig = signatureOf(report, mode);
    if (!force && sig === lastSignature) return false;

    if (mode !== componentsOnlyMode()) collapsedState.clear();
    componentsOnly = mode === 'components';
    lastReport = report;
    lastSignature = sig;

    const root = buildTree(model, mode, rootOverride);
    currentRootFile = root ? root.file : null;
    if (!root) {
      nodes = [];
      byFile = new Map();
      recomputeHighlightedFlow();
      updateHighlightControls();
      return true;
    }
    nodes = layout(root);

    // Prepend the revealed ancestor chain, if any, as a straight vertical
    // breadcrumb above the root — plain data nodes (no siblings, no
    // collapse toggle of their own) rather than full recursive subtrees,
    // since they only ever have one child each on the way back down.
    if (showAncestors && ancestorChain.length) {
      const n = ancestorChain.length;
      const ancestorNodes = [];
      let childNode = root;
      for (let i = n - 1; i >= 0; i--) {
        const file = ancestorChain[i];
        const fs = model.byPath.get(file) || null;
        const anode = { file, fs, depth: -(n - i), parent: null, collapsed: false, hasChildren: false, children: [childNode] };
        ancestorNodes.unshift(anode);
        childNode = anode;
      }
      for (let i = 0; i < ancestorNodes.length; i++) {
        ancestorNodes[i].parent = i > 0 ? ancestorNodes[i - 1].file : null;
      }
      root.parent = ancestorNodes[ancestorNodes.length - 1].file;
      for (const an of ancestorNodes) {
        an.x = root.x;
        an.y = an.depth * (NODE_H + V_GAP);
      }
      nodes = ancestorNodes.concat(nodes);
    }

    byFile = new Map(nodes.map((n) => [n.file, n]));
    recomputeHighlightedFlow();
    updateHighlightControls();
    return true;
  }

  function componentsOnlyMode() {
    return componentsOnly ? 'components' : 'all';
  }

  /* ---------- canvas transform helpers (same convention as graph.js) ---------- */

  function resizeCanvas() {
    const ratio = Math.max(1, window.devicePixelRatio || 1);
    const rect = wrap.getBoundingClientRect();
    const w = Math.max(1, Math.round(rect.width));
    const h = Math.max(1, Math.round(rect.height));
    if (canvas.width !== Math.round(w * ratio) || canvas.height !== Math.round(h * ratio)) {
      canvas.width = Math.round(w * ratio);
      canvas.height = Math.round(h * ratio);
    }
    canvas.style.width = `${w}px`;
    canvas.style.height = `${h}px`;
  }

  function screenToWorld(sx, sy) {
    return { x: (sx - transform.x) / transform.scale, y: (sy - transform.y) / transform.scale };
  }

  function worldToScreen(x, y) {
    return { x: x * transform.scale + transform.x, y: y * transform.scale + transform.y };
  }

  function fitToView() {
    if (!nodes.length) return;
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    for (const n of nodes) {
      minX = Math.min(minX, n.x - NODE_W / 2);
      maxX = Math.max(maxX, n.x + NODE_W / 2);
      minY = Math.min(minY, n.y);
      maxY = Math.max(maxY, n.y + NODE_H);
    }
    const rect = wrap.getBoundingClientRect();
    const w = Math.max(1, rect.width);
    const h = Math.max(1, rect.height);
    const treeW = Math.max(1, maxX - minX);
    const treeH = Math.max(1, maxY - minY);
    const scale = Math.min(1.15, Math.max(0.18, Math.min((w - 60) / treeW, (h - 60) / treeH)));
    transform.scale = scale;
    transform.x = (w - treeW * scale) / 2 - minX * scale;
    transform.y = scale * (30 - minY);
  }

  function hasHighlight() {
    return !!highlightedFile && highlightedFlow.size > 0;
  }

  function recomputeHighlightedFlow() {
    if (!highlightedFile || !byFile.has(highlightedFile)) {
      highlightedFlow = new Set();
      if (highlightedFile && !byFile.has(highlightedFile)) highlightedFile = null;
      return;
    }
    const set = new Set();
    let cursor = byFile.get(highlightedFile) || null;
    while (cursor) {
      set.add(cursor.file);
      cursor = cursor.parent ? byFile.get(cursor.parent) || null : null;
    }
    const start = byFile.get(highlightedFile);
    if (start) {
      // The visible tree already omits descendants behind collapsed branches,
      // so the highlighted flow intentionally tracks the currently rendered
      // subtree instead of auto-expanding hidden paths.
      const stack = [start];
      while (stack.length) {
        const node = stack.pop();
        if (!set.has(node.file)) set.add(node.file);
        for (const child of node.children) stack.push(child);
      }
    }
    highlightedFlow = set;
  }

  function nodeInFlow(node) {
    return highlightedFlow.has(node.file);
  }

  function shouldHideUnrelated() {
    return !!(hideUnrelatedToggle && hideUnrelatedToggle.checked && hasHighlight());
  }

  function nodeIsRendered(node) {
    return !shouldHideUnrelated() || nodeInFlow(node);
  }

  function setHighlight(file) {
    if (!file || !byFile.has(file)) return false;
    highlightedFile = file;
    recomputeHighlightedFlow();
    updateHighlightControls();
    draw();
    return true;
  }

  function clearHighlight() {
    highlightedFile = null;
    highlightedFlow = new Set();
    updateHighlightControls();
    draw();
  }

  function updateHighlightControls() {
    if (clearHighlightBtn) clearHighlightBtn.hidden = !hasHighlight();
  }

  /* ---------- drawing ---------- */

  function truncateLabel(text, maxWidth) {
    if (ctx.measureText(text).width <= maxWidth) return text;
    let lo = 0, hi = text.length;
    while (lo < hi) {
      const mid = Math.ceil((lo + hi) / 2);
      const candidate = `${text.slice(0, mid)}…`;
      if (ctx.measureText(candidate).width <= maxWidth) lo = mid;
      else hi = mid - 1;
    }
    return lo <= 0 ? '…' : `${text.slice(0, lo)}…`;
  }

  function nodeRect(n) {
    return { left: n.x - NODE_W / 2, top: n.y, right: n.x + NODE_W / 2, bottom: n.y + NODE_H };
  }

  function toggleRect(n) {
    // Small handle centered on the bottom edge of the box, used to
    // expand/collapse without triggering the drawer.
    return { left: n.x - 11, top: n.y + NODE_H - 8, right: n.x + 11, bottom: n.y + NODE_H + 8 };
  }

  function ancestorToggleRect(n) {
    // Handle centered on the top edge of the currently selected root node
    // (depth 0), mirroring toggleRect's bottom-edge placement. Reveals or
    // hides the node's full ancestor chain back to the true entry in one
    // click, rather than expanding one level at a time.
    return { left: n.x - 11, top: n.y - 8, right: n.x + 11, bottom: n.y + 8 };
  }

  function chipRect(n) {
    const r = nodeRect(n);
    return { left: r.right - 34, top: r.top - 9, right: r.right + 12, bottom: r.top + 9 };
  }

  function flowRect(n) {
    const r = nodeRect(n);
    return { left: r.left - 8, top: r.top - 8, right: r.left + 10, bottom: r.top + 10 };
  }

  function drawEdge(parent, child) {
    const midY = parent.y + NODE_H + (child.y - (parent.y + NODE_H)) / 2;
    ctx.beginPath();
    ctx.moveTo(parent.x, parent.y + NODE_H);
    ctx.lineTo(parent.x, midY);
    ctx.lineTo(child.x, midY);
    ctx.lineTo(child.x, child.y);
    ctx.stroke();
  }

  function draw() {
    resizeCanvas();
    const ratio = Math.max(1, window.devicePixelRatio || 1);
    const rect = wrap.getBoundingClientRect();
    const width = rect.width;
    const height = rect.height;
    const highlightActive = hasHighlight();
    const flowColor = colors.accent4;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);

    if (!nodes.length) {
      ctx.fillStyle = colors.muted;
      ctx.font = '13px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
      ctx.fillText('No entry file found in this report.', 24, 36);
      return;
    }

    ctx.save();
    ctx.translate(transform.x, transform.y);
    ctx.scale(transform.scale, transform.scale);

    // Edges first, beneath the boxes.
    for (const n of nodes) {
      if (!nodeIsRendered(n)) continue;
      for (const child of n.children) {
        if (!nodeIsRendered(child)) continue;
        const edgeInFlow = highlightActive && nodeInFlow(n) && nodeInFlow(child);
        ctx.save();
        if (highlightActive && !edgeInFlow) ctx.globalAlpha = 0.25;
        ctx.lineWidth = edgeInFlow ? 2.4 : 1.4;
        ctx.strokeStyle = edgeInFlow ? flowColor : 'rgba(139,152,169,.4)';
        drawEdge(n, child);
        ctx.restore();
      }
    }

    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    for (const n of nodes) {
      if (!nodeIsRendered(n)) continue;
      const r = nodeRect(n);
      const isTrueEntry = n.file === trueEntryFile;
      const isRoot = n.depth === 0 && !isTrueEntry;
      const isComponent = n.fs && n.fs.isComponent;
      const nodeHighlighted = highlightActive && nodeInFlow(n);
      const isSelected = highlightedFile === n.file;
      const border = nodeHighlighted ? flowColor : isTrueEntry ? colors.accent : isRoot ? colors.warn : isComponent ? colors.accent3 : colors.line;

      ctx.save();
      if (highlightActive && !nodeHighlighted) ctx.globalAlpha = 0.25;

      ctx.beginPath();
      roundRect(r.left, r.top, NODE_W, NODE_H, 8);
      ctx.fillStyle = isSelected ? 'rgba(57,197,207,.14)' : (n === hover ? colors.panel : colors.panel2);
      ctx.fill();
      ctx.lineWidth = nodeHighlighted || isTrueEntry || isRoot ? 2.4 : 1.4;
      ctx.strokeStyle = border;
      ctx.stroke();

      const f = flowRect(n);
      ctx.beginPath();
      ctx.arc((f.left + f.right) / 2, (f.top + f.bottom) / 2, 9, 0, Math.PI * 2);
      ctx.fillStyle = isSelected ? flowColor : colors.panel;
      ctx.fill();
      ctx.lineWidth = 1;
      ctx.strokeStyle = isSelected ? flowColor : colors.line;
      ctx.stroke();
      ctx.beginPath();
      ctx.arc((f.left + f.right) / 2 - 2.5, (f.top + f.bottom) / 2 - 1.5, 2.2, 0, Math.PI * 2);
      ctx.fillStyle = isSelected ? colors.panel : flowColor;
      ctx.fill();
      ctx.beginPath();
      ctx.moveTo((f.left + f.right) / 2 - 1, (f.top + f.bottom) / 2 + 1);
      ctx.lineTo((f.left + f.right) / 2 + 4, (f.top + f.bottom) / 2 + 1);
      ctx.lineTo((f.left + f.right) / 2 + 4, (f.top + f.bottom) / 2 - 4);
      ctx.strokeStyle = isSelected ? colors.panel : flowColor;
      ctx.lineWidth = 1.5;
      ctx.stroke();

      // Kind tag (top strip text) + file name.
      ctx.font = '9px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
      ctx.fillStyle = nodeHighlighted ? flowColor : colors.muted;
      const kindLabel = isTrueEntry ? 'ENTRY' : isRoot ? 'ROOT' : isComponent ? 'COMPONENT' : 'FILE';
      ctx.fillText(kindLabel, r.left + 22, r.top + 12);

      ctx.font = '12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';
      ctx.fillStyle = colors.text;
      const label = n.fs ? baseName(n.fs.file) : baseName(n.file);
      ctx.fillText(truncateLabel(label, NODE_W - 32), r.left + 10, r.top + 30);

      // "+N other importers" badge.
      if (n.fs && n.fs.fanIn > 1) {
        const c = chipRect(n);
        ctx.beginPath();
        roundRect(c.left, c.top, c.right - c.left, c.bottom - c.top, 8);
        ctx.fillStyle = 'rgba(210,153,34,.14)';
        ctx.fill();
        ctx.lineWidth = 1;
        ctx.strokeStyle = 'rgba(210,153,34,.55)';
        ctx.stroke();
        ctx.font = '10px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
        ctx.fillStyle = colors.warn;
        ctx.textAlign = 'center';
        ctx.fillText(`+${n.fs.fanIn - 1}`, (c.left + c.right) / 2, (c.top + c.bottom) / 2 + 1);
        ctx.textAlign = 'left';
      }

      // Expand/collapse handle, only when the node actually has children
      // in the underlying data (even while collapsed).
      if (n.hasChildren) {
        const t = toggleRect(n);
        ctx.beginPath();
        ctx.arc((t.left + t.right) / 2, (t.top + t.bottom) / 2, 9, 0, Math.PI * 2);
        ctx.fillStyle = colors.panel;
        ctx.fill();
        ctx.strokeStyle = colors.line;
        ctx.lineWidth = 1;
        ctx.stroke();
        ctx.font = '10px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
        ctx.fillStyle = colors.muted;
        ctx.textAlign = 'center';
        ctx.fillText(n.children.length ? '▾' : '▸', (t.left + t.right) / 2, (t.top + t.bottom) / 2 + 1);
        ctx.textAlign = 'left';
      }

      // Ancestor-reveal handle on the current root's top edge, only
      // when it actually has an ancestor chain to show.
      if (n.depth === 0 && rootHasAncestors) {
        const at = ancestorToggleRect(n);
        ctx.beginPath();
        ctx.arc((at.left + at.right) / 2, (at.top + at.bottom) / 2, 9, 0, Math.PI * 2);
        ctx.fillStyle = colors.panel;
        ctx.fill();
        ctx.strokeStyle = colors.line;
        ctx.lineWidth = 1;
        ctx.stroke();
        ctx.font = '10px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
        ctx.fillStyle = colors.muted;
        ctx.textAlign = 'center';
        ctx.fillText(showAncestors ? '▾' : '▴', (at.left + at.right) / 2, (at.top + at.bottom) / 2 + 1);
        ctx.textAlign = 'left';
      }
      ctx.restore();
    }
    ctx.restore();
  }

  function roundRect(x, y, w, h, r) {
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  /* ---------- hit testing ---------- */

  function nodeAtScreen(clientX, clientY) {
    const rect = canvas.getBoundingClientRect();
    const p = screenToWorld(clientX - rect.left, clientY - rect.top);
    for (let i = nodes.length - 1; i >= 0; i--) {
      const n = nodes[i];
      if (!nodeIsRendered(n)) continue;
      const r = nodeRect(n);
      const t = n.hasChildren ? toggleRect(n) : null;
      const at = (n.depth === 0 && rootHasAncestors) ? ancestorToggleRect(n) : null;
      const f = flowRect(n);
      const withinToggle = t && p.x >= t.left && p.x <= t.right && p.y >= t.top && p.y <= t.bottom;
      const withinAncestorToggle = at && p.x >= at.left && p.x <= at.right && p.y >= at.top && p.y <= at.bottom;
      const withinFlow = p.x >= f.left && p.x <= f.right && p.y >= f.top && p.y <= f.bottom;
      if (withinToggle || withinAncestorToggle || withinFlow || (p.x >= r.left && p.x <= r.right && p.y >= r.top - 10 && p.y <= r.bottom)) {
        return n;
      }
    }
    return null;
  }

  function hitRegion(n, clientX, clientY) {
    const rect = canvas.getBoundingClientRect();
    const p = screenToWorld(clientX - rect.left, clientY - rect.top);
    const f = flowRect(n);
    if (p.x >= f.left && p.x <= f.right && p.y >= f.top && p.y <= f.bottom) return 'flow';
    if (n.depth === 0 && rootHasAncestors) {
      const at = ancestorToggleRect(n);
      if (p.x >= at.left && p.x <= at.right && p.y >= at.top && p.y <= at.bottom) return 'ancestorToggle';
    }
    if (n.hasChildren) {
      const t = toggleRect(n);
      if (p.x >= t.left && p.x <= t.right && p.y >= t.top && p.y <= t.bottom) return 'toggle';
    }
    if (n.fs && n.fs.fanIn > 1) {
      const c = chipRect(n);
      if (p.x >= c.left && p.x <= c.right && p.y >= c.top && p.y <= c.bottom) return 'chip';
    }
    return 'body';
  }

  function updateTooltip(node, clientX, clientY) {
    if (!node) {
      tooltip.style.display = 'none';
      return;
    }
    const fs = node.fs;
    tooltip.innerHTML = '<div class="tree-tip-title"></div><div class="tree-tip-meta"></div>';
    tooltip.querySelector('.tree-tip-title').textContent = node.file;
    const meta = fs
      ? `${fs.isComponent ? 'component' : 'file'} · imported ${fs.imports} times by ${fs.fanIn} file${fs.fanIn === 1 ? '' : 's'} · imports ${fs.fanOut} modules` +
        (hasHighlight() && nodeInFlow(node) ? ' · in highlighted flow' : '') +
        (node.hasChildren ? (node.children.length ? ' · click ▾ to collapse' : ' · click ▸ to expand') : '')
      : node.file;
    tooltip.querySelector('.tree-tip-meta').textContent = meta;
    const rect = wrap.getBoundingClientRect();
    tooltip.style.left = `${Math.min(rect.width - 20, clientX - rect.left + 14)}px`;
    tooltip.style.top = `${Math.max(10, clientY - rect.top + 14)}px`;
    tooltip.style.display = 'block';
  }

  /* ---------- rendering entry points ---------- */

  let firstRenderDone = false;

  function render(force) {
    if (!isVisible() && !force) return;
    const changed = rebuild(force);
    if (!changed && firstRenderDone) return;
    if (!firstRenderDone && nodes.length) {
      fitToView();
      firstRenderDone = true;
    } else if (!nodes.length) {
      firstRenderDone = false;
    }
    draw();
  }

  function setAllCollapsed(collapsed) {
    for (const n of nodes) {
      if (n.hasChildren) collapsedState.set(n.file, collapsed);
    }
    // Also record state for any not-yet-materialized descendants so
    // re-expanding step-by-step later starts from a consistent baseline.
    const report = reportValue();
    if (report) {
      const model = buildModel(report);
      const map = componentsOnly ? model.childrenComponents : model.childrenAll;
      for (const list of map.values()) {
        for (const f of list) {
          if ((map.get(f.file) || []).length) collapsedState.set(f.file, collapsed);
        }
      }
    }
    render(true);
    fitVisibleIfNeeded();
  }

  /* ---------- interaction ---------- */

  canvas.addEventListener('pointerdown', (e) => {
    if (!isVisible()) return;
    canvas.setPointerCapture(e.pointerId);
    const rect = canvas.getBoundingClientRect();
    pointer = { x: e.clientX, y: e.clientY, sx: e.clientX - rect.left, sy: e.clientY - rect.top, moved: false };
    drag = { type: 'pan', ox: transform.x, oy: transform.y };
    canvas.classList.add('dragging');
  });

  canvas.addEventListener('pointermove', (e) => {
    if (!isVisible()) return;
    const rect = canvas.getBoundingClientRect();
    if (drag && pointer) {
      const dx = e.clientX - pointer.x;
      const dy = e.clientY - pointer.y;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) pointer.moved = true;
      if (drag.type === 'pan') {
        transform.x = drag.ox + dx;
        transform.y = drag.oy + dy;
        draw();
      }
      return;
    }
    const node = nodeAtScreen(e.clientX, e.clientY);
    if (node !== hover) {
      hover = node;
      draw();
    }
    updateTooltip(node, e.clientX, e.clientY);
    canvas.style.cursor = node ? 'pointer' : 'grab';
  });

  function endDrag(e) {
    if (!drag) return;
    const wasClick = pointer && !pointer.moved;
    drag = null;
    canvas.classList.remove('dragging');
    if (wasClick) {
      const node = nodeAtScreen(e.clientX, e.clientY);
      if (node) {
        const region = hitRegion(node, e.clientX, e.clientY);
        if (region === 'flow') {
          if (highlightedFile === node.file) clearHighlight();
          else setHighlight(node.file);
        } else if (region === 'toggle') {
          collapsedState.set(node.file, node.children.length > 0);
          render(true);
          fitVisibleIfNeeded();
        } else if (region === 'ancestorToggle') {
          showAncestors = !showAncestors;
          render(true);
          fitToView();
          draw();
        } else if (region === 'chip' && node.fs && typeof openFile === 'function') {
          openFile(node.fs);
        } else if (region === 'body' && node.fs && typeof openFile === 'function') {
          openFile(node.fs);
        }
      }
    }
    pointer = null;
  }

  // After a toggle, avoid the tree drifting so far it needs manual
  // recentring on every click — refit only if the current view no longer
  // contains any node at all (e.g. collapsing the whole visible subtree).
  function fitVisibleIfNeeded() {
    if (!nodes.length) return;
    const rect = wrap.getBoundingClientRect();
    let anyOnscreen = false;
    for (const n of nodes) {
      const p = worldToScreen(n.x, n.y);
      if (p.x > -NODE_W && p.x < rect.width + NODE_W && p.y > -NODE_H && p.y < rect.height + NODE_H) {
        anyOnscreen = true;
        break;
      }
    }
    if (!anyOnscreen) {
      fitToView();
      draw();
    }
  }

  canvas.addEventListener('pointerup', endDrag);
  canvas.addEventListener('pointercancel', endDrag);
  canvas.addEventListener('pointerleave', () => {
    if (!drag) {
      hover = null;
      tooltip.style.display = 'none';
      draw();
    }
  });

  canvas.addEventListener('wheel', (e) => {
    if (!isVisible()) return;
    e.preventDefault();
    const rect = canvas.getBoundingClientRect();
    const sx = e.clientX - rect.left;
    const sy = e.clientY - rect.top;
    const before = screenToWorld(sx, sy);
    const factor = Math.exp(-e.deltaY * 0.001);
    transform.scale = Math.min(3, Math.max(0.08, transform.scale * factor));
    const after = screenToWorld(sx, sy);
    transform.x += (after.x - before.x) * transform.scale;
    transform.y += (after.y - before.y) * transform.scale;
    draw();
  }, { passive: false });

  if (onlyToggle) onlyToggle.addEventListener('change', () => {
    collapsedState.clear();
    firstRenderDone = false;
    render(true);
  });
  if (expandAllBtn) expandAllBtn.addEventListener('click', () => setAllCollapsed(false));
  if (collapseAllBtn) collapseAllBtn.addEventListener('click', () => setAllCollapsed(true));
  if (resetBtn) resetBtn.addEventListener('click', () => {
    fitToView();
    draw();
  });
  if (hideUnrelatedToggle) hideUnrelatedToggle.addEventListener('change', () => draw());
  if (clearHighlightBtn) clearHighlightBtn.addEventListener('click', clearHighlight);

  /* ---------- search: pick any file/component as the tree's root ---------- */

  function updateRootChip() {
    if (!rootChip) return;
    if (rootOverride) {
      rootChip.hidden = false;
      if (rootChipFile) rootChipFile.textContent = rootOverride;
    } else {
      rootChip.hidden = true;
    }
  }

  // searchMatches ranks a query against every internal file's path: an exact
  // basename match first, then substring hits, capped so the dropdown stays
  // short. Case-insensitive, matched against the full path so a query like
  // "pages/list" also works.
  function searchMatches(query) {
    const report = reportValue();
    const files = (report && report.internalFiles) || [];
    const q = query.trim().toLowerCase();
    if (!q) return [];
    const scored = [];
    for (const f of files) {
      const path = f.file.toLowerCase();
      const base = baseName(f.file).toLowerCase();
      let score = -1;
      if (base === q) score = 0;
      else if (base.startsWith(q)) score = 1;
      else if (base.includes(q)) score = 2;
      else if (path.includes(q)) score = 3;
      if (score >= 0) scored.push({ f, score });
    }
    scored.sort((a, b) => (a.score - b.score) || a.f.file.localeCompare(b.f.file));
    return scored.slice(0, 12).map((s) => s.f);
  }

  function closeSearchResults() {
    if (searchResults) {
      searchResults.hidden = true;
      searchResults.replaceChildren();
    }
  }

  function renderSearchResults(query) {
    if (!searchResults) return;
    const matches = searchMatches(query);
    if (!matches.length) {
      searchResults.replaceChildren();
      const empty = document.createElement('div');
      empty.className = 'tree-search-empty';
      empty.textContent = query.trim() ? 'No matching files.' : 'Type to search files or components.';
      searchResults.appendChild(empty);
      searchResults.hidden = false;
      return;
    }
    const items = matches.map((f) => {
      const row = document.createElement('div');
      row.className = 'tree-search-result';
      row.setAttribute('role', 'button');
      row.dataset.file = f.file;
      const path = document.createElement('span');
      path.className = 'tree-search-path';
      path.textContent = f.file;
      row.appendChild(path);
      if (f.isComponent) {
        const tag = document.createElement('span');
        tag.className = 'tree-search-tag';
        tag.textContent = 'component';
        row.appendChild(tag);
      }
      row.addEventListener('click', () => selectRoot(f.file));
      return row;
    });
    searchResults.replaceChildren(...items);
    searchResults.hidden = false;
  }

  function selectRoot(file) {
    const report = reportValue();
    const exists = report && (report.internalFiles || []).some((f) => f.file === file);
    if (!exists) return false;
    rootOverride = file;
    showAncestors = false;
    updateRootChip();
    closeSearchResults();
    if (searchInput) searchInput.value = '';
    firstRenderDone = false;
    render(true);
    return true;
  }

  function clearRoot() {
    rootOverride = null;
    showAncestors = false;
    updateRootChip();
    firstRenderDone = false;
    render(true);
  }

  if (searchInput) {
    searchInput.addEventListener('input', (e) => renderSearchResults(e.target.value));
    searchInput.addEventListener('focus', (e) => {
      if (e.target.value.trim()) renderSearchResults(e.target.value);
    });
    searchInput.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        closeSearchResults();
        searchInput.blur();
      } else if (e.key === 'Enter') {
        const matches = searchMatches(searchInput.value);
        if (matches.length) selectRoot(matches[0].file);
      }
    });
  }
  document.addEventListener('click', (e) => {
    if (!searchResults || searchResults.hidden) return;
    if (e.target === searchInput || searchResults.contains(e.target)) return;
    closeSearchResults();
  });
  if (rootClearBtn) rootClearBtn.addEventListener('click', clearRoot);
  updateHighlightControls();

  document.querySelectorAll('.tab').forEach((tab) => {
    tab.addEventListener('click', () => {
      if (tab.dataset.tab === 'tree') {
        firstRenderDone = firstRenderDone && nodes.length > 0;
        render(true);
      }
    });
  });

  window.addEventListener('resize', () => {
    if (isVisible()) draw();
  });

  if (window.ResizeObserver) {
    new ResizeObserver(() => {
      if (isVisible()) draw();
    }).observe(wrap);
  }

  setInterval(() => {
    const report = reportValue();
    if (report && report !== lastReport && isVisible()) render(true);
  }, 800);

  window.__IMPORTSTATS_TREE__ = {
    state: () => ({
      nodeCount: nodes.length,
      renderedNodeCount: nodes.filter((n) => nodeIsRendered(n)).length,
      transform: { ...transform },
      componentsOnly,
      highlightedFile,
      highlightSize: highlightedFlow.size,
      currentRootFile,
      trueEntryFile,
      hideUnrelated: !!(hideUnrelatedToggle && hideUnrelatedToggle.checked),
      rootHasAncestors,
      showAncestors,
    }),
    rebuild: () => render(true),
    fit: () => { fitToView(); draw(); },
    hitTest: (clientX, clientY) => {
      const node = nodeAtScreen(clientX, clientY);
      return node ? { file: node.file, region: hitRegion(node, clientX, clientY) } : null;
    },
    // toggleAncestors reveals/hides the current root's ancestor chain up
    // to the true entry, mirroring what clicking the ▴/▾ handle above the
    // root does. Returns false when the root has no ancestors to show.
    toggleAncestors: () => {
      if (!rootHasAncestors) return false;
      showAncestors = !showAncestors;
      render(true);
      fitToView();
      draw();
      return true;
    },
    hasAncestors: () => rootHasAncestors,
    ancestorsShown: () => showAncestors,
    // screenPositionOf exposes a node's current on-screen center, useful for
    // deterministic interaction tests (headless click/tap coordinates).
    screenPositionOf: (file) => {
      const n = byFile.get(file);
      if (!n || !nodeIsRendered(n)) return null;
      const p = worldToScreen(n.x, n.y + NODE_H / 2);
      const rect = wrap.getBoundingClientRect();
      return { x: rect.left + p.x, y: rect.top + p.y };
    },
    // screenToggleOf exposes the expand/collapse handle's current on-screen
    // center for the same reason.
    screenToggleOf: (file) => {
      const n = byFile.get(file);
      if (!n || !n.hasChildren || !nodeIsRendered(n)) return null;
      const t = toggleRect(n);
      const p = worldToScreen((t.left + t.right) / 2, (t.top + t.bottom) / 2);
      const rect = wrap.getBoundingClientRect();
      return { x: rect.left + p.x, y: rect.top + p.y };
    },
    // screenAncestorToggleOf exposes the ancestor-reveal handle's current
    // on-screen center, or null when the current root has no ancestors.
    screenAncestorToggleOf: (file) => {
      const n = byFile.get(file);
      if (!n || n.depth !== 0 || !rootHasAncestors || !nodeIsRendered(n)) return null;
      const at = ancestorToggleRect(n);
      const p = worldToScreen((at.left + at.right) / 2, (at.top + at.bottom) / 2);
      const rect = wrap.getBoundingClientRect();
      return { x: rect.left + p.x, y: rect.top + p.y };
    },
    // screenChipOf exposes the "+N other importers" badge's current
    // on-screen center, or null if the node has no such badge.
    screenChipOf: (file) => {
      const n = byFile.get(file);
      if (!n || !n.fs || n.fs.fanIn <= 1 || !nodeIsRendered(n)) return null;
      const c = chipRect(n);
      const p = worldToScreen((c.left + c.right) / 2, (c.top + c.bottom) / 2);
      const rect = wrap.getBoundingClientRect();
      return { x: rect.left + p.x, y: rect.top + p.y };
    },
    screenFlowOf: (file) => {
      const n = byFile.get(file);
      if (!n || !nodeIsRendered(n)) return null;
      const f = flowRect(n);
      const p = worldToScreen((f.left + f.right) / 2, (f.top + f.bottom) / 2);
      const rect = wrap.getBoundingClientRect();
      return { x: rect.left + p.x, y: rect.top + p.y };
    },
    panToNode: (file) => {
      const n = byFile.get(file);
      if (!n || !nodeIsRendered(n)) return false;
      const rect = wrap.getBoundingClientRect();
      transform.x = rect.width / 2 - n.x * transform.scale;
      transform.y = rect.height / 2 - n.y * transform.scale;
      draw();
      return true;
    },
    highlightNode: (file) => setHighlight(file),
    clearHighlight,
    currentHighlight: () => highlightedFile,
    kindOf: (file) => {
      const n = byFile.get(file);
      if (!n) return null;
      if (n.file === trueEntryFile) return 'ENTRY';
      if (n.depth === 0) return 'ROOT';
      return n.fs && n.fs.isComponent ? 'COMPONENT' : 'FILE';
    },
    entryFile: () => (lastReport && findEntry(lastReport.internalFiles || [])) || null,
    search: (query) => searchMatches(query).map((f) => f.file),
    selectRoot,
    clearRoot,
    currentRoot: () => rootOverride,
  };
})();
