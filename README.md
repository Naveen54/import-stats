# importstats

A dependency-free Go CLI that walks a React application's **import graph from an entry file** and shows the statistics in a local web dashboard: which npm packages are imported, how many times, from which files and lines, which symbols are pulled in, plus orphan files, circular dependencies, installed size, unused exports, architecture-rule violations and `package.json` health.

Handles modern ESM (`import`, `export … from`, dynamic `import()`) and legacy CommonJS (`require`) in `.js`, `.jsx`, `.ts`, `.tsx`, `.mjs` and `.cjs` files.

Zero Go dependencies, zero JavaScript dependencies, no build step, no network access, and no build-config parsing.

## Quick start

```bash
npx importstats ./my-react-app
```

Needs Node 18+; the prebuilt binary for your OS/CPU is fetched automatically
(macOS, Linux, Windows; x64 and arm64). Flags go before the path; add
`-no-open` in headless/CI environments.

## Install

```bash
cd import-stats
go build -o importstats ./cmd/importstats
# optional
mv importstats /usr/local/bin/
```

Prebuilt binaries for Windows (amd64/arm64) and macOS (a universal Intel +
Apple Silicon binary) are attached to each [GitHub release](../../releases),
created by `.github/workflows/publish.yml` when a version tag is pushed.

### Releasing to npm

Pushing a version tag publishes the npm packages (`importstats` and the
`@mnkdev/importstats-*` platform packages) at that version via
`.github/workflows/publish.yml`:

```bash
git tag v0.2.0 && git push origin v0.2.0
```

Pre-release tags (e.g. `v0.2.0-rc.1`) publish under the `next` dist-tag. Publishing
uses npm trusted publishing (OIDC): each package has this repo and `publish.yml`
configured as its trusted publisher on npmjs.com, so no token secret is needed. The workflow only *stages* each package; approve the
7 staged versions (platform packages first, `importstats` last) on npmjs.com under
**Staged Packages**, or with `npm stage list` / `npm stage approve <stage-id>`.

## Usage

```bash
# analyse a specific entry file
importstats ./my-react-app/src/index.js

# point at a project directory — defaults to src/index.js
importstats ./my-react-app

# export only, no dashboard
importstats --no-serve --json report.json --csv packages.csv ./src/index.js

# compare several sibling apps
importstats --compare ../admin-app --compare ../inbox-app ./main-app

# gate a CI build against a saved baseline
importstats --no-serve --baseline base.json --fail-on new-cycles,rules ./src/index.js
```

> **Flags must come before the project path.** Go's flag parser stops at the first positional argument, so `importstats ./app --compare ../other` would silently ignore `--compare`. The CLI detects the common form of this mistake and errors rather than misleading you.

The dashboard opens automatically in your browser. The report JSON is also served at `/api/report`.

### Flags

| Flag | Description |
|---|---|
| `--entry <file>` | Entry file. Also accepted as a positional argument. Defaults to `src/index.js` (then `src/main.*`, `index.*`) in the given/current directory. |
| `--src-root <dir>` | Root used to resolve bare local imports such as `common/analytics`. Defaults to the `src` directory on the entry's path. |
| `--alias key=path` | Extra module alias, repeatable. Use for build-tool aliases (e.g. `--alias config=config`). |
| `--ignore <pattern>` | Skip paths containing this substring or whose basename matches this glob. Repeatable. |
| `--rule "<a> !-> <b>"` | Architecture constraint, repeatable. See [Architecture rules](#architecture-rules). |
| `--no-size` | Skip measuring installed package size from `node_modules`. |
| `--port <n>` | Dashboard port (`0` = pick a free one). |
| `--no-open` | Don't launch a browser (the server still runs). |
| `--no-serve` | Print the terminal summary and exit without serving. |
| `--top <n>` | Packages listed in the terminal summary (default 15). |
| `--workers <n>` | Parser workers (default: 2× CPU count). |

**Exports**

| Flag | Description |
|---|---|
| `--json <file>` | Write the full report as JSON. |
| `--csv <file>` | Write per-package stats as CSV. |
| `--md <file>` | Write a compact Markdown summary, suited to pasting into a pull request. |
| `--html <file>` | Write a **single self-contained HTML file** — CSS, JS and the report data all inlined, so it opens offline with no server. |
| `--sarif <file>` | Write findings as SARIF 2.1.0 for GitHub code scanning. |
| `--annotate` | Emit GitHub Actions workflow annotations for findings. |
| `--history <n>` | Analyse the latest `n` git commits using isolated worktrees. |
| `--watch` | Re-analyse automatically when source files change and push updates to the dashboard. |
| `--dot <file>` | Write the module graph in Graphviz DOT format. |
| `--mermaid <file>` | Write the module graph as a Mermaid flowchart. |
| `--collapse` | Collapse the DOT/Mermaid export to one node per directory. |

**Baselines and CI**

| Flag | Description |
|---|---|
| `--save-baseline <file>` | Write this run's report for a later comparison. |
| `--baseline <file>` | Compare against a previously saved report and print the diff. |
| `--fail-on <gates>` | Comma-separated gates that exit non-zero: `new-cycles`, `new-undeclared`, `new-orphans`, `rules`, `imports-up`, `packages-up`. |

**Multi-project**

| Flag | Description |
|---|---|
| `--compare <path>` | Additional project to compare against. Repeatable; enables comparison mode. |

## How resolution works

No build configuration is read — resolution is deliberately simple and predictable:

1. **Relative** (`./x`, `../x`) — resolved against the importing file.
2. **Bare, src-root relative** (`common/analytics`) — resolved against the src root, mirroring webpack's `modules: [src, node_modules]`.
3. **Otherwise** — treated as an npm package; `@scope/name/sub/path` is attributed to `@scope/name` with `sub/path` recorded as a subpath.

When a specifier has **no extension**, `.js`, `.jsx`, `.ts`, `.tsx` are probed in that order, then `index.js|jsx|ts|tsx` inside a matching directory, then `.mjs`, `.cjs`, `.json`. Explicit extensions are honoured as written; `.scss/.less/.css` count as styles and images/fonts/etc. as assets.

Only files reachable from the entry are analysed — `node_modules` is never traversed. File identity is canonicalised through symlinks, so a symlinked path and its real path count as one file (and symlink loops cannot hang the walk). Node builtins, including subpaths such as `fs/promises`, are classified as builtins rather than npm packages.

If a bare specifier is neither found under src nor declared in `package.json` but a matching file/directory exists in the project root, it is flagged as a likely build alias in the **Warnings** tab with a ready-to-use `--alias` suggestion (e.g. webpack's `config` and `env` aliases in many webpack projects).

## What the dashboard shows

- **Packages** — import count, distinct importing files, share bar, declared version, dependency type (`dependency`, `devDependency`, `peerDependency`, `optionalDependency`, `undeclared`, `alias?`, `builtin`) and import kinds. Click any row to drill down to every importing file, line, specifier and imported symbols.
- **Symbols** — which bindings are imported from each package and how often (e.g. which `lodash` functions are actually used).
- **Internal files** — fan-in (how many modules import it), fan-out, and total import statements; click to see the importers.
- **Styles & assets** — style/asset imports grouped by extension.
- **Orphans** — source files under `src` that the entry never reaches.
- **Cycles** — circular dependency groups (Tarjan SCC).
- **Dependency health** — declared-but-unused and imported-but-undeclared packages. A package is considered declared if it appears in `dependencies`, `devDependencies`, `peerDependencies` or `optionalDependencies`; when it is listed in more than one, that precedence order decides the reported type and version. Only `dependencies` are checked for being unused.
- **Warnings** — unresolved specifiers, non-literal `import()`/`require()`, unreadable files, alias suggestions.
- **Component tree** — the React import hierarchy (entry → pages → components), with a components-only/all-files toggle. See below.

### Why is this here?

Every package and every file carries the **shortest import chain from the entry**, rendered in the drawer as breadcrumbs:

```
Reached via  src/index.js → …/app/App.js → …/app/routes.js → …/common/TheatreList.js
```

This answers the question that import counts alone never do: *why is this in my bundle at all?*

### Insights

Advisory findings, kept separate from **Warnings** (which mean something actually went wrong):

- **Deep imports** — a package imported both bare and via subpaths, or imported bare with named symbols while subpaths exist, suggesting `import x from 'pkg/x'` would pull in less.
- **Barrels** — `index.*` files with high fan-in that re-export a large surface, so importing one name drags in everything behind it.
- **Duplicate-purpose packages** — two or more packages from the same equivalence group (date handling, utilities, HTTP, state, i18n, uuid, immutability, …) both in use, with each one's import count so the winner is obvious.

### Size and cost

When `node_modules` is present, each package is measured for installed size, file count and shallow transitive dependency count, and the Packages table gains a sortable **Size** column. This ranks packages by **cost**, not popularity — on one of the sample apps `@ant-design/icons` is 13 MB for a handful of imports, and `antd` is 47 MB.

Measuring is concurrent and symlink-safe. A missing `node_modules` is a silent no-op, and `--no-size` skips it entirely.

### Unused exports

Each file's exported names are cross-referenced against the symbols other reachable files actually import from it, to surface probable dead code.

This is deliberately **conservative** — a false positive here would destroy trust in the whole tool. The report is suppressed entirely when any importer uses `import * as ns`, when the name is re-exported through a barrel, for the entry file, and for side-effect-only modules. Findings are reported as *possibly unused*, never as errors, and each one states its reasoning: *no file reachable from the entry imports this name; importers that are themselves orphans do not count*.

### Architecture rules

Layering constraints, checked against local file→file edges and matched as globs on src-relative paths:

```bash
importstats --rule "src/components !-> src/pages" \
            --rule "src/util !-> src/views" ./src/index.js
```

- `A !-> B` — forbidden: nothing matching `A` may import anything matching `B`.
- `A --> B` — allowed-only: files matching `A` may import *only* things matching `B`.

Violations carry the file, the line and the rule that was broken, appear in a **Rules** tab, and can fail a build via `--fail-on rules`.

### Baselines and CI gates

```bash
# record the current state
importstats --no-serve --save-baseline base.json ./src/index.js

# later, fail the build if things got worse
importstats --no-serve --baseline base.json \
            --fail-on new-cycles,new-undeclared,rules ./src/index.js
```

The diff reports added and removed packages, import-count deltas, new cycles, newly undeclared dependencies, orphan-count changes and new rule violations.

`--sarif` emits SARIF 2.1.0 for GitHub code scanning. `--annotate` emits escaped
GitHub Actions workflow commands for the same findings. Both formats use
relative paths and deterministic ordering.

### TypeScript paths and workspaces

The analyzer automatically reads a nearby `tsconfig.json` or `jsconfig.json`
for JSONC `baseUrl`/`paths` aliases (explicit `--alias` values take precedence).
It also detects npm, Yarn, pnpm and Lerna workspace roots and resolves local
workspace packages as first-party source instead of third-party dependencies.
The four sample apps are independent repositories, so workspace detection is
normally a fast no-op for them.

### History and watch mode

`--history 10` samples the latest commits using detached temporary worktrees;
the current checkout is never mutated, and failed historical commits are
reported without aborting the trend.

`--watch` starts the normal dashboard plus a stdlib-only polling watcher.
Changes are debounced and delivered to connected browsers over SSE, which
re-fetch the report and repaint existing tabs. No filesystem watcher library
or build tool is required.

### Comparing several projects

Built for the case of several sibling apps in one organisation:

```bash
importstats --compare ../partner-app --compare ../admin-app \
            --compare ../inbox-app ../main-app
```

Each project is analysed in parallel into a package matrix — rows are packages, columns are projects, cells hold the declared version and import count. **Version drift is the headline**: packages whose declared versions differ across apps are highlighted and sorted first, with major drift (`antd@3` vs `antd@4`) distinguished from minor. It also shows the shared core and the packages unique to a single app. A project that fails to analyse reports its error without aborting the others.

### Graph export and visualisation

`--dot` and `--mermaid` write the module graph for Graphviz or Mermaid, and `--collapse` reduces it to one node per directory — essential on the larger apps, where a few hundred individual nodes are unreadable.

The dashboard also renders the graph directly on a `<canvas>` with a hand-written force-directed layout (no libraries): nodes sized by fan-in and coloured by top-level directory, with zoom, pan, node dragging, hover labels and click-through to the detail drawer. It is collapsed to directory level by default, with drill-down into a directory.

### Component tree

For React apps, the **Component tree** tab renders the import structure as a **visual flowchart** — entry at the top, boxes for each file connected by org-chart-style elbow connectors down to pages and components — reading like an "app flow" diagram on a canvas, distinct from the force-directed Graph tab above.

- A **components only / all files** toggle lets you see just the render hierarchy or every reachable file (utils included). If the project has no detected components (e.g. a plain Node app), the toggle auto-switches to "all files" so the tab is never empty.
- Every file appears **once**, nested under its shortest-path parent (the same "why is this here?" chain used elsewhere). A file reached from more than one place shows a `+N other importers` badge that opens the same detail drawer as everywhere else.
- The canvas supports **drag to pan**, **scroll/wheel to zoom**, and a **Reset view** button to re-fit the whole tree; click a box to open its detail drawer, or click a node's expand/collapse handle to fold a branch.
- The tree auto-expands 5 levels deep, with **Expand all** / **Collapse all** controls for larger apps.
- Click the small **flow** affordance on any node to highlight that node's full flow: its ancestor chain back to the tree's current root plus everything in its descendant subtree, while dimming everything else so you can trace how it is reached and what it pulls in without losing the rest of the tree as context. Turn on **Hide unrelated** to replace that dimming with a true filtered view that shows only the highlighted flow. While a flow is highlighted, a **Clear highlight** button restores the normal full-tree view.
- The **search box** above the canvas finds any file or component by name and re-roots the tree at that node (a "Rooted at ..." chip appears; click "show full tree" to reset). This is handy for zooming straight into one part of a large app instead of expanding down from the entry every time.
- When a custom root is selected, click the **▴/▾ handle above the root box** to reveal that node's own ancestor chain — every parent back up to the true entry — as a simple breadcrumb, in one click (click again to hide it). This complements rooting: rooting shows what's *below* the node, this shows what's *above* it, so you can see the full "why is this here, and what does it lead to" picture without leaving the rooted view. Highlighting a flow while ancestors are shown also traces up through the revealed chain.

Whether a file "is a component" is a heuristic, computed from whether the scanner found JSX in it, or it has a directly-declared PascalCase export (`function`, `class`, `const`, `let` or `var` — not a bare re-export list, which is just as often a bag of PascalCase enum constants as a component). Like the other advisory heuristics in this tool, it can both under- and over-detect: it will miss a component built purely with `React.createElement` and no JSX and no PascalCase export, and it will flag a plain helper file that merely embeds a JSX element (e.g. an icon in a config object) as a "component". Treat it as a guide to the app's shape, not a certified inventory.

### Sharing a report

- `--md <file>` writes a compact Markdown summary for pasting into a pull request.
- `--html <file>` writes a **single self-contained file** with the CSS, JS and report data all inlined. It opens offline with no server — useful for archiving a snapshot or attaching to a ticket. Live-only controls (re-analyse, download) are hidden and the page is banner-marked as a static snapshot.


### Re-analysing without restarting

The header shows when the report was generated (absolute time, how long the walk took, and a relative "… ago" that ticks by itself), so you always know how stale the numbers on screen are.

Click **↻ Re-analyse** (or press `R`) to re-run the whole analysis against the current state of the files and repaint every tab — no restart, no page reload. The button is disabled while a run is in flight, and a toast reports the outcome, including what changed since the previous run (e.g. `+1 files, +2 imports, +1 packages`). If the re-run fails, the toast shows the error and the previously loaded report stays on screen.

This is driven by `POST /api/reanalyze`, which returns the refreshed report as JSON and updates the report served at `/api/report`. Runs are serialised server-side, so overlapping requests never analyse concurrently.

Press `/` to focus the filter, `Esc` to close the drawer.

## Project layout

```
cmd/importstats      CLI entry point and flags
internal/analysis    reusable analysis pipeline shared by the CLI and the dashboard
internal/scanner     tolerant JS/JSX/TS lexer, import and export extraction
internal/resolver    specifier → file or package resolution
internal/project     entry/src-root detection, package.json reading
internal/graph       concurrent BFS over the import graph
internal/analyzer    aggregation: packages, symbols, fan-in/out, cycles, chains, insights, unused exports
internal/weight      installed package size from node_modules
internal/rules       architecture layering constraints
internal/diff        baseline comparison and CI gates
internal/compare     multi-project comparison and version drift
internal/viz         Graphviz DOT and Mermaid export
internal/report      JSON/CSV/Markdown/standalone-HTML export and terminal summary
internal/server      embedded dashboard, /api/report and /api/reanalyze
internal/server/web  dashboard HTML/CSS/JS (embedded via embed.FS)
```

## Tests

```bash
go test ./...
go test -race ./...
```

155 tests across all thirteen packages, covering the lexer (JSX, TS generics, template literals and their `${…}` substitutions, comments, CRLF multiline imports, `require` destructuring, `require.resolve`, regex-vs-division, non-literal dynamic imports, local export declarations), the resolver (extension/index probing, aliases, subpaths, builtins and builtin subpaths, symlink identity), the graph walk (cycles, diamonds, orphans, ignore patterns, determinism across worker counts, symlink loops), project detection, package weighing, architecture rules, baseline diffing, multi-project comparison, DOT/Mermaid export, the JSON/CSV/Markdown writers (round-trip, quoting, formula-injection neutralisation, flush errors), the standalone HTML export (asset inlining, script ordering, and `</script>` escape-injection defence), the reusable analysis pipeline, and the dashboard servers.

Reports are deterministic: identical input produces byte-identical JSON apart from the `generatedAt`/`durationMs` timestamps.
