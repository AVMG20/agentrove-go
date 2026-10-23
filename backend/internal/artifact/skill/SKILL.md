---
name: artifacts
description: Publish HTML pages as AgentRove artifacts, shareable links the user can open and make public. Use when the user mentions an artifact, or wants a page, report, dashboard, chart, diagram, prototype or visual summary (for example of code changes) to view or share.
---

# Artifacts

An artifact is a single self-contained HTML page published with the `create_artifact` tool of the `agentrove` MCP server. It gets its own link (`url` in the result) that the user can open, share, and switch between private and public.

- Use an artifact instead of writing an `.html` file whenever the page is meant to be viewed or shared.
- Always reply with the link as a markdown link: `[Title](url)`.
- To change one, call `update_artifact` with its id and the complete new HTML; the link stays the same. Use `get_artifact` to read the current HTML first and `list_artifacts` to find one.
- Artifacts start private: only the user, signed in, can open them. The user makes one public (anyone with the link) with the toggle on the artifact page; you can't change visibility.
- The page runs sandboxed with an opaque origin: no cookies, localStorage or sessionStorage, so keep state in memory. Scripts and styles from public CDNs and `fetch()` to public CORS APIs work.

## Kit

Start every page with the kit unless the user asks for a custom look. It styles plain HTML in light and dark mode and adds layout, data and chart helpers:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Weekly report</title>
  <link rel="stylesheet" href="{{KIT}}/kit.css">
  <script src="{{KIT}}/kit.js"></script>
</head>
<body>
<main>
  <h1>Weekly report</h1>
  <p class="lead">One sentence on what this page shows.</p>

  <div class="grid">
    <div class="card stat">
      <span class="stat-label">Revenue</span>
      <span class="stat-value">$48.2k</span>
      <span class="stat-delta up">12% vs last week</span>
    </div>
    <div class="card stat">
      <span class="stat-label">Errors</span>
      <span class="stat-value">31</span>
      <span class="stat-delta down">4 more</span>
    </div>
  </div>

  <div class="card"><h3>Trend</h3><div id="trend"></div></div>
  <div class="card"><h3>Top pages</h3><div id="pages"></div></div>
</main>
<script>
  Kit.chart('#trend', {
    type: 'area',
    labels: ['Mon', 'Tue', 'Wed', 'Thu', 'Fri'],
    series: [{ name: 'Revenue', data: [8.1, 9.4, 7.7, 11.2, 11.8] }],
    unit: '$',
  });
  Kit.table('#pages', [
    { page: '/pricing', views: 1840, conversion: 4.2 },
    { page: '/docs', views: 1312, conversion: 1.1 },
  ], { columns: [{ key: 'page', label: 'Page' }, { key: 'views', label: 'Views' }, { key: 'conversion', label: 'Conversion', unit: '%' }] });
</script>
</body>
</html>
```

CSS (kit.css):
- Put content in `<main>`. Headings, `p.lead`, lists, tables, code, `pre`, buttons (`.primary`), inputs and `blockquote` are styled without classes.
- Layout: `.grid` (responsive cards), `.grid-2`, `.grid-3`, `.span-2`, `.row` (`.row.between`), `.stack`.
- `.card` is a bordered panel.
- `.stat` with `.stat-label`, `.stat-value` and `.stat-delta.up` / `.down` is a KPI tile.
- `.badge` and `.callout` take `.good`, `.bad`, `.warn`, `.info` (badge also `.accent`).
- `.progress > span[style="width:40%"]` is a bar, and `.progress` also takes `.good`, `.bad`, `.warn`.
- `.muted` and `.num` (right-aligned tabular numbers).
- Colors are CSS variables: `--kit-accent`, `--kit-good`, `--kit-bad`, `--kit-warn`, `--kit-info`, `--kit-muted`, `--kit-border`, `--kit-surface`, and series colors `--kit-c1` to `--kit-c8`.
- `<html data-theme="light">` or `"dark"` forces a theme.

JS (kit.js, call it from a script at the end of `<body>`):
- `Kit.chart(target, options)` draws a themed Chart.js chart. Options:
  - `type`: `'line'`, `'area'`, `'bar'`, `'pie'`, `'doughnut'` or `'scatter'`.
  - `labels`: an array of x labels.
  - `series`: `[{ name, data, color? }]`; for scatter, `data` is `[{ x, y }]`.
  - Optional: `stacked`, `horizontal` (bars), `unit` (`'%'`, `'$'`, `'€'`, `'ms'`, `'GB'`, …), `digits`, `height` (px, default 280), `points` (show dots), and `options` (extra Chart.js options).
- `Kit.table(target, rows, { columns: [{ key, label, unit, digits, align }] })` renders an array of objects as a sortable table. Numbers are formatted and right-aligned.
- `<pre class="mermaid">…</pre>` renders a mermaid diagram (flowchart, sequence, class, state, gantt, timeline).
- `<pre class="diff">…</pre>` colours a unified diff (`+`, `-` and `@@` lines). It is good for showing code changes.
- `Kit.format(value, unit, digits)` formats a number the same way, and `Kit.colors()` returns the series colors.

React or any other library can still be loaded from a CDN when a page needs it, but plain HTML with the kit covers most pages.
