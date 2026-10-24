/* AgentRove artifact kit: themed charts (Chart.js), sortable tables, mermaid
   diagrams and coloured diffs. Load kit.css alongside it. */
(function () {
  'use strict';

  var CHART_JS = 'https://cdn.jsdelivr.net/npm/chart.js@4.4.7/dist/chart.umd.min.js';
  var MERMAID_JS = 'https://cdn.jsdelivr.net/npm/mermaid@11.4.1/dist/mermaid.min.js';

  var scripts = {};
  function load(src) {
    if (!scripts[src]) {
      scripts[src] = new Promise(function (resolve, reject) {
        var s = document.createElement('script');
        s.src = src;
        s.onload = resolve;
        s.onerror = function () { reject(new Error('Could not load ' + src)); };
        document.head.appendChild(s);
      });
    }
    return scripts[src];
  }

  function $(target) {
    var el = typeof target === 'string' ? document.querySelector(target) : target;
    if (!el) throw new Error('Kit: no element for ' + target);
    return el;
  }

  function token(name) {
    return getComputedStyle(document.documentElement).getPropertyValue('--kit-' + name).trim();
  }

  function palette() {
    var out = [];
    for (var i = 1; i <= 8; i++) out.push(token('c' + i));
    return out;
  }

  function alpha(color, a) {
    var m = /^#([0-9a-f]{6})$/i.exec(color);
    if (!m) return color;
    var n = parseInt(m[1], 16);
    return 'rgba(' + (n >> 16) + ',' + ((n >> 8) & 255) + ',' + (n & 255) + ',' + a + ')';
  }

  // Shown in the page and logged, so the viewer reports it to the agent.
  function fail(el, err) {
    console.error('Kit:', err);
    el.innerHTML = '';
    var p = document.createElement('p');
    p.className = 'kit-error';
    p.textContent = String(err && err.message ? err.message : err);
    el.appendChild(p);
  }

  // format(1234.5, '$') -> "$1,234.5"; units: '%', '$', '€', '£' or any suffix ('ms', 'GB').
  function format(v, unit, digits) {
    if (typeof v !== 'number' || !isFinite(v)) return v == null ? '' : String(v);
    var abs = Math.abs(v);
    var s = abs >= 10000
      ? new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(v)
      : v.toLocaleString(undefined, { maximumFractionDigits: digits == null ? (abs < 10 ? 2 : 1) : digits });
    if (!unit) return s;
    if (unit === '%') return s + '%';
    if (unit === '$' || unit === '€' || unit === '£') return (v < 0 ? '-' + unit + s.slice(1) : unit + s);
    return s + ' ' + unit;
  }

  /* Kit.chart('#el', {
       type: 'line' | 'area' | 'bar' | 'pie' | 'doughnut' | 'scatter',
       labels: ['Jan', 'Feb'],
       series: [{ name: 'Revenue', data: [1, 2], color: optional }],  // scatter: data [{x, y}]
       stacked, horizontal, unit, digits, height (px, default 280), points (show dots),
       options: extra Chart.js options, shallow-merged
     }) -> Promise<Chart> */
  function chart(target, o) {
    var host = $(target);
    o = o || {};
    host.innerHTML = '';
    var box = document.createElement('div');
    box.className = 'kit-chart';
    box.style.height = (o.height || 280) + 'px';
    var canvas = document.createElement('canvas');
    box.appendChild(canvas);
    host.appendChild(box);

    return load(CHART_JS).then(function () {
      var Chart = window.Chart;
      var colors = palette();
      var type = o.type || 'line';
      var round = type === 'pie' || type === 'doughnut';
      var area = type === 'area';
      var border = token('border');
      var fmt = function (v) { return format(v, o.unit, o.digits); };

      Chart.defaults.color = token('muted');
      Chart.defaults.borderColor = border;
      Chart.defaults.font.family = token('font');
      Chart.defaults.font.size = 12;

      var datasets = (o.series || []).map(function (s, i) {
        var c = s.color || colors[i % colors.length];
        if (round) {
          return {
            label: s.name,
            data: s.data,
            backgroundColor: s.data.map(function (_, j) { return colors[j % colors.length]; }),
            borderColor: token('surface'),
            borderWidth: 2,
          };
        }
        return {
          label: s.name,
          data: s.data,
          borderColor: c,
          backgroundColor: area ? alpha(c, 0.18) : c,
          fill: area ? 'origin' : false,
          pointBackgroundColor: c,
        };
      });

      var options = {
        responsive: true,
        maintainAspectRatio: false,
        // Index mode searches along x unless told otherwise, which picks the
        // wrong row on horizontal bars.
        interaction: round || type === 'scatter'
          ? { mode: 'nearest', intersect: false }
          : { mode: 'index', axis: o.horizontal ? 'y' : 'x', intersect: false },
        elements: {
          line: { tension: 0.3, borderWidth: 2 },
          point: { radius: o.points || type === 'scatter' ? 3 : 0, hoverRadius: 5 },
          bar: { borderRadius: 4, maxBarThickness: 48 },
        },
        plugins: {
          legend: {
            display: round || datasets.length > 1,
            position: 'bottom',
            labels: { usePointStyle: true, boxWidth: 8, boxHeight: 8, padding: 16 },
          },
          tooltip: {
            callbacks: {
              label: function (ctx) {
                var p = ctx.parsed;
                var v = typeof p === 'object' ? (o.horizontal ? p.x : p.y) : p;
                var name = round ? ctx.label : ctx.dataset.label;
                return (name ? name + ': ' : '') + fmt(v);
              },
            },
          },
        },
      };
      if (type === 'doughnut') options.cutout = '62%';
      if (!round) {
        var value = {
          beginAtZero: true,
          stacked: !!o.stacked,
          grid: { color: border },
          border: { display: false },
          ticks: { callback: function (v) { return fmt(v); } },
        };
        var category = type === 'scatter'
          ? { type: 'linear', grid: { color: border }, border: { display: false } }
          : { stacked: !!o.stacked, grid: { display: false } };
        options.scales = o.horizontal ? { x: value, y: category } : { x: category, y: value };
        if (o.horizontal) options.indexAxis = 'y';
      }
      if (o.options) {
        for (var k in o.options) options[k] = o.options[k];
      }
      return new Chart(canvas, {
        type: area ? 'line' : type,
        data: { labels: o.labels, datasets: datasets },
        options: options,
      });
    }).catch(function (err) { fail(host, err); });
  }

  /* Kit.table('#el', rows, { columns: [{ key, label, unit, digits, align }] })
     renders an array of objects as a sortable table; numbers are right-aligned
     and formatted. Columns default to the first row's keys. */
  function table(target, rows, o) {
    var host = $(target);
    o = o || {};
    rows = rows || [];
    var columns = (o.columns || Object.keys(rows[0] || {})).map(function (c) {
      return typeof c === 'string' ? { key: c, label: c } : c;
    });
    var numeric = columns.map(function (c) {
      return c.align ? c.align === 'right' : rows.every(function (r) { return r[c.key] == null || typeof r[c.key] === 'number'; });
    });
    var wrap = document.createElement('div');
    wrap.className = 'table-wrap';
    var t = document.createElement('table');
    var thead = t.createTHead().insertRow();
    var tbody = t.createTBody();
    var order = rows.slice();

    function render() {
      tbody.innerHTML = '';
      order.forEach(function (r) {
        var tr = tbody.insertRow();
        columns.forEach(function (c, i) {
          var td = tr.insertCell();
          var v = r[c.key];
          td.textContent = typeof v === 'number' ? format(v, c.unit, c.digits) : v == null ? '' : String(v);
          if (numeric[i]) td.className = 'num';
        });
      });
    }

    columns.forEach(function (c, i) {
      var th = document.createElement('th');
      th.textContent = c.label == null ? c.key : c.label;
      th.setAttribute('data-sort', '');
      if (numeric[i]) th.className = 'num';
      th.addEventListener('click', function () {
        var dir = th.getAttribute('data-sort') === 'asc' ? 'desc' : 'asc';
        Array.prototype.forEach.call(thead.children, function (h) { h.setAttribute('data-sort', ''); });
        th.setAttribute('data-sort', dir);
        order.sort(function (a, b) {
          var x = a[c.key], y = b[c.key];
          var cmp = typeof x === 'number' && typeof y === 'number' ? x - y : String(x == null ? '' : x).localeCompare(String(y == null ? '' : y), undefined, { numeric: true });
          return dir === 'asc' ? cmp : -cmp;
        });
        render();
      });
      thead.appendChild(th);
    });
    render();
    wrap.appendChild(t);
    host.innerHTML = '';
    host.appendChild(wrap);
    return t;
  }

  // <pre class="mermaid"> blocks become diagrams.
  function diagrams() {
    var blocks = document.querySelectorAll('pre.mermaid');
    if (!blocks.length) return Promise.resolve();
    return load(MERMAID_JS).then(function () {
      var dark = getComputedStyle(document.documentElement).colorScheme.indexOf('dark') !== -1;
      window.mermaid.initialize({
        startOnLoad: false,
        theme: 'base',
        fontFamily: token('font'),
        themeVariables: {
          darkMode: dark,
          fontFamily: token('font'),
          background: token('bg'),
          primaryColor: token('surface-2'),
          primaryTextColor: token('text'),
          primaryBorderColor: token('accent'),
          secondaryColor: token('surface'),
          tertiaryColor: token('surface'),
          lineColor: token('muted'),
          textColor: token('text'),
          noteBkgColor: token('surface-2'),
          noteTextColor: token('text'),
        },
      });
      return window.mermaid.run({ nodes: blocks });
    }).catch(function (err) {
      console.error('Kit: mermaid:', err);
      Array.prototype.forEach.call(blocks, function (b) {
        var p = document.createElement('p');
        p.className = 'kit-error';
        p.textContent = String(err && err.message ? err.message : err);
        b.after(p);
      });
    });
  }

  // <pre class="diff"> gets added/removed/hunk lines coloured.
  function diffs() {
    Array.prototype.forEach.call(document.querySelectorAll('pre.diff:not([data-kit])'), function (pre) {
      pre.setAttribute('data-kit', '');
      var lines = pre.textContent.replace(/\n$/, '').split('\n');
      pre.textContent = '';
      lines.forEach(function (line) {
        var span = document.createElement('span');
        span.textContent = line;
        if (/^(\+\+\+|---|diff |index )/.test(line)) span.className = 'meta';
        else if (line[0] === '+') span.className = 'add';
        else if (line[0] === '-') span.className = 'del';
        else if (line.indexOf('@@') === 0) span.className = 'hunk';
        pre.appendChild(span);
      });
    });
  }

  /* Kit.store: JSON state that survives reloads. Saved for the artifact's
     owner (the agent reads it with get_artifact_state); other viewers and
     pages opened outside AgentRove keep it in memory only.
       await Kit.store.ready;  Kit.store.get(key, fallback);  Kit.store.set(key, value);
       Kit.store.remove(key);  Kit.store.all();  Kit.store.persistent */
  var framed = window.parent !== window;
  var cache = {};
  var early = {};
  var persistent = false;
  var has = function (o, k) { return Object.prototype.hasOwnProperty.call(o, k); };
  var clone = function (v) { return v === undefined ? undefined : JSON.parse(JSON.stringify(v)); };
  var ready = new Promise(function (resolve) {
    if (!framed) return resolve();
    var done = false;
    function finish() {
      if (done) return;
      done = true;
      window.removeEventListener('message', onInit);
      // Writes made before the saved state arrived win over it.
      for (var k in early) {
        if (early[k] === null) delete cache[k];
        else cache[k] = early[k];
      }
      resolve();
    }
    function onInit(e) {
      var d = e.data;
      if (e.source !== window.parent || !d || d.agentrove !== 'state:init') return;
      cache = d.state && typeof d.state === 'object' ? d.state : {};
      persistent = !!d.persistent;
      finish();
    }
    window.addEventListener('message', onInit);
    window.parent.postMessage({ agentrove: 'state:load' }, '*');
    setTimeout(finish, 3000);
  });
  var store = {
    get: function (key, fallback) { return has(cache, key) ? clone(cache[key]) : fallback; },
    set: function (key, value) {
      key = String(key);
      value = value === undefined ? null : clone(value);
      if (value === null) delete cache[key];
      else cache[key] = value;
      early[key] = value;
      if (framed) window.parent.postMessage({ agentrove: 'state:set', key: key, value: value }, '*');
    },
    remove: function (key) { store.set(key, null); },
    all: function () { return clone(cache); },
  };
  Object.defineProperty(store, 'persistent', { get: function () { return persistent; } });
  store.ready = ready.then(function () { early = {}; return store; });

  // Hand-written tables get the same scroll wrapper as Kit.table(), so a wide
  // table scrolls on its own instead of pushing the whole page sideways.
  function wrapTables() {
    var tables = document.querySelectorAll('table');
    for (var i = 0; i < tables.length; i++) {
      var t = tables[i];
      if (t.parentElement && t.parentElement.classList.contains('table-wrap')) continue;
      var wrap = document.createElement('div');
      wrap.className = 'table-wrap';
      t.parentNode.insertBefore(wrap, t);
      wrap.appendChild(t);
    }
  }

  function init() {
    wrapTables();
    diffs();
    diagrams();
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();

  window.Kit = { chart: chart, table: table, format: format, diagrams: diagrams, diffs: diffs, colors: palette, store: store };
})();
