// The report UI. The generator ships this script compressed together with the data (see pack in
// main.go); the loader in report.html.tmpl decompresses both, sets window.pcsData and runs this script.
//
// Everything is scoped to this function: hosts that re-write the page into the same document with
// document.open/write (e.g. Slack's HTML preview) keep the global scope, and top-level const
// declarations would then throw "already declared" and leave the page blank.
(() => {
"use strict";

// unpack rebuilds the data model from the compact form written by pack in main.go.
function unpack(p) {
  const meta = new Map(p.features.map((f) => [f.id, f]));
  const s = p.surface;
  return {
    roles: p.roles.map((r) => ({
      name: r.name, impls: r.impls, reference: r.reference,
      features: r.ids.map((id, i) => ({ ...meta.get(id), cells: r.cells[i], kinds: r.kinds?.[i] || [] })),
    })),
    surface: s.entry.map((entry, i) => ({
      entry, kind: s.kind[i], source: s.source[i], status: s.status[i], reason: s.reason[i] || undefined,
      scope: s.scope[i] || undefined, coveredBy: s.coveredBy[i] || undefined, gaps: s.gaps[i] || undefined,
    })),
    kindAPIs: p.kindAPIs || {},
  };
}

const DATA = unpack(window.pcsData);

const FEATURE_STATUSES = ["supported", "partial", "fail", "fail-closed", "rejected", "ignored", "unsupported", "unknown", "missing"];
// Statuses that mean a proxy does not (or has not been shown to) implement a feature.
const NOT_IMPLEMENTED = new Set(["unsupported", "ignored", "rejected", "fail-closed", "unknown", "missing"]);
const SURFACE_STATUSES = ["covered", "todo", "exempt"];
// API filter: which APIs a feature's recorded kinds (coverage.KindAPI) must, or must not, include.
const API_FILTERS = {
  all: { label: "All APIs", test: () => true },
  "no-istio": {
    label: "Gateway API / Kubernetes only",
    help: "Features that apply no Istio API (Istio CRDs, MeshConfig, istiod flags), i.e. that a Gateway API implementation can run. Ingress and waypoint features also use the role's shared Gateway API Gateway.",
    test: (apis) => !apis.has("istio"),
  },
  "gateway-api": {
    label: "Uses Gateway API",
    help: "Features that apply a Gateway API kind (gateway.networking.k8s.io, inference.networking.k8s.io).",
    test: (apis) => apis.has("gateway-api"),
  },
  istio: {
    label: "Uses Istio APIs",
    help: "Features that apply an Istio CRD, patch MeshConfig or set istiod flags.",
    test: (apis) => apis.has("istio"),
  },
  "no-config": {
    label: "Applies no config",
    help: "Features that test default behavior: they apply nothing beyond the role's shared setup.",
    test: (apis, kinds) => kinds.length === 0,
  },
};
const API_LABELS = { "gateway-api": "Gateway API", istio: "Istio", kubernetes: "Kubernetes" };
const SURFACE_HIDDEN = new Set(["todo", "exempt"]);
const STATUS_HELP = {
  supported: "Behaves as specified",
  partial: "Some subcases pass",
  fail: "Accepted but behaves incorrectly",
  "fail-closed": "Not supported; traffic is denied (safe)",
  rejected: "Config is rejected (e.g. status condition)",
  ignored: "Config is silently ignored",
  unsupported: "Declared unsupported",
  unknown: "Not measured yet (skipped)",
  missing: "No declaration in the manifest",
  covered: "Covered by at least one feature",
  todo: "Not covered by any feature yet (backlog)",
  exempt: "Deliberately not covered",
  gap: "In scope for the role but not covered",
  oos: "Out of scope for the role",
};

// Maps an API kind of the surface to a feature area, so the surface tab groups like the role tabs.
const KIND_AREAS = {
  AuthorizationPolicy: "Authorization",
  PeerAuthentication: "Authentication (mTLS & JWT)",
  RequestAuthentication: "Authentication (mTLS & JWT)",
  VirtualService: "Routing & traffic shifting",
  DestinationRule: "Load balancing & resilience",
  ServiceEntry: "Service discovery & sidecar scope",
  WorkloadEntry: "Service discovery & sidecar scope",
  WorkloadGroup: "Service discovery & sidecar scope",
  Sidecar: "Service discovery & sidecar scope",
  Gateway: "Gateways & listeners",
  "gateway-api-ext": "Gateway API",
  Telemetry: "Observability",
  EnvoyFilter: "Extensibility (implementation specific)",
  WasmPlugin: "Extensibility (implementation specific)",
  TrafficExtension: "Extensibility (implementation specific)",
  MeshConfig: "MeshConfig",
  ProxyConfig: "ProxyConfig",
  annotation: "Annotations & labels",
  label: "Annotations & labels",
  flag: "Feature flags",
  interaction: "Interactions",
};

// The page may be embedded in a sandboxed iframe (e.g. a chat preview) where reading localStorage
// throws, so settings fall back to in-memory state there.
const store = {
  get(k) { try { return window.localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { window.localStorage.setItem(k, v); } catch { /* not persisted */ } },
};

function hashTab() {
  try { return decodeURIComponent(location.hash.slice(1)) || "overview"; } catch { return "overview"; }
}

const state = {
  tab: hashTab(),
  q: "",
  hideUnimpl: store.get("pcs.hideUnimpl") === "1",
  api: API_FILTERS[store.get("pcs.api")] ? store.get("pcs.api") : "all",
  off: { feature: new Set(), surface: new Set() },
  collapsed: new Set(parseList(store.get("pcs.collapsed"))),
  open: new Set(),
};

function parseList(s) {
  try { const v = JSON.parse(s || "[]"); return Array.isArray(v) ? v : []; } catch { return []; }
}

const $ = (s) => document.querySelector(s);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const color = (s) => getComputedStyle(document.documentElement).getPropertyValue("--" + s.replace("-", "")).trim();

function linkIssue(issue) {
  if (/^https?:\/\//.test(issue)) return `<a href="${esc(issue)}" target="_blank">${esc(issue)}</a>`;
  const m = /^([\w.-]+\/[\w.-]+)#(\d+)$/.exec(issue);
  if (m) return `<a href="https://github.com/${m[1]}/issues/${m[2]}" target="_blank">${esc(issue)}</a>`;
  return esc(issue);
}

function persist() {
  store.set("pcs.hideUnimpl", state.hideUnimpl ? "1" : "0");
  store.set("pcs.api", state.api);
  store.set("pcs.collapsed", JSON.stringify([...state.collapsed]));
}

// featureVisible reports whether a feature row of a role passes the filters. With ignoreChips, the
// status chips are not applied (used to count the rows behind each chip).
function featureVisible(role, f, ignoreChips) {
  const sts = role.impls.map((i) => f.cells[i].status);
  if (state.hideUnimpl && sts.every((s) => NOT_IMPLEMENTED.has(s))) return false;
  if (!ignoreChips && !sts.some((s) => !state.off.feature.has(s))) return false;
  if (!API_FILTERS[state.api].test(featureAPIs(f), f.kinds)) return false;
  return matchesQuery(f.id, f.description, f.covers, f.cases, f.kinds, role.impls.map((i) => f.cells[i].reason));
}

function featureAPIs(f) {
  return new Set(f.kinds.map((k) => DATA.kindAPIs[k]));
}

function surfaceVisible(e, ignoreChips) {
  if (state.hideUnimpl && SURFACE_HIDDEN.has(e.status)) return false;
  if (!ignoreChips && state.off.surface.has(e.status)) return false;
  return matchesQuery(e.entry, e.reason, e.source, Object.values(e.coveredBy || {}).flat());
}

function renderTabs() {
  const tabs = [["overview", "Overview", null, null]]
    .concat(DATA.roles.map((r) => [r.name, r.name, r.features.filter((f) => featureVisible(r, f)).length, r.features.length]))
    .concat([["surface", "API surface", DATA.surface.filter((e) => surfaceVisible(e)).length, DATA.surface.length]]);
  if (!tabs.some((t) => t[0] === state.tab)) state.tab = "overview";
  $("#tabs").innerHTML = tabs.map(([id, label, n, total]) => {
    const count = n == null ? "" : `<span class="count" title="${n} of ${total} shown">${n === total ? n : `${n}/${total}`}</span>`;
    return `<button data-tab="${esc(id)}" class="${id === state.tab ? "active" : ""}">${esc(label[0].toUpperCase() + label.slice(1))}${count}</button>`;
  }).join("");
}

// renderChips renders the status filter chips with, for each status, the number of rows that have it
// among the rows passing the other filters.
function renderChips(kind, statuses, counts) {
  const off = state.off[kind];
  $("#chips").innerHTML = statuses.map((s) =>
    `<span class="chip ${off.has(s) ? "off" : ""}" data-chip="${kind}:${s}" title="${esc(STATUS_HELP[s])}"><span class="dot" style="background:${color(s)}"></span>${esc(s)} <b>${counts[s] || 0}</b></span>`).join("");
}

function setShown(shown, total) {
  $("#shown").textContent = `Showing ${shown} of ${total}`;
}

function matchesQuery(...fields) {
  if (!state.q) return true;
  const q = state.q.toLowerCase();
  return fields.some((f) => (Array.isArray(f) ? f.join(" ") : String(f ?? "")).toLowerCase().includes(q));
}

function groupBy(items, key) {
  const m = new Map();
  for (const it of items) {
    const k = key(it);
    if (!m.has(k)) m.set(k, []);
    m.get(k).push(it);
  }
  return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]));
}

function areaRow(groupKey, label, n, total, cells) {
  const collapsed = state.collapsed.has(groupKey);
  return `<tr class="area ${collapsed ? "collapsed" : ""}" data-group="${esc(groupKey)}"><th colspan="2"><span class="caret">▾</span>${esc(label)} <span class="stats">(${n}${n !== total ? ` of ${total}` : ""})</span></th>${cells}</tr>`;
}

// Role tab: rows are features grouped by area, columns are the role's proxies.
function renderRole(role) {
  const counts = {};
  for (const f of role.features) {
    if (!featureVisible(role, f, true)) continue;
    for (const s of new Set(role.impls.map((i) => f.cells[i].status))) counts[s] = (counts[s] || 0) + 1;
  }
  renderChips("feature", FEATURE_STATUSES, counts);
  const visible = (f) => featureVisible(role, f);
  const groups = groupBy(role.features, (f) => f.area);
  let rows = "";
  let shown = 0;
  for (const [area, all] of groups) {
    const fs = all.filter(visible);
    if (!fs.length) continue;
    shown += fs.length;
    const gk = role.name + "/" + area;
    const stats = role.impls.map((i) => {
      const ok = all.filter((f) => f.cells[i].status === "supported").length;
      return `<th class="impl" title="${ok} of ${all.length} features supported">${ok}/${all.length}</th>`;
    }).join("");
    rows += areaRow(gk, area, fs.length, all.length, stats);
    if (state.collapsed.has(gk)) continue;
    for (const f of fs) {
      const key = role.name + "/" + f.id;
      const badges = (f.security ? `<span class="badge sec" title="Silent failure is a vulnerability (fail-open rule)">security</span>` : "") +
        (f.profile && f.profile !== "core" ? `<span class="badge">${esc(f.profile)}</span>` : "") +
        (f.requiresL7 ? `<span class="badge" title="Requires L7 processing">L7</span>` : "");
      const cells = role.impls.map((i) => {
        const c = f.cells[i];
        const tip = [STATUS_HELP[c.status], c.reason, c.issue && "issue: " + c.issue].filter(Boolean).join("\n");
        return `<td class="cell"><span class="st ${esc(c.status)} ${c.reason ? "has-reason" : ""}" title="${esc(tip)}">${esc(c.status)}</span></td>`;
      }).join("");
      rows += `<tr class="feature" data-open="${esc(key)}"><td class="id"><code>${esc(f.id)}</code>${badges}</td><td class="desc">${esc(f.description)}</td>${cells}</tr>`;
      if (state.open.has(key)) rows += featureDetails(role, f);
    }
  }
  const head = `<thead><tr><th>Feature</th><th>Description</th>${role.impls.map((i) =>
    `<th class="impl">${esc(i)}${i === role.reference ? `<span class="ref">reference</span>` : ""}</th>`).join("")}</tr></thead>`;
  setShown(shown, role.features.length);
  $("#main").innerHTML = shown ? `<table>${head}<tbody>${rows}</tbody></table>` : `<div class="empty">No features match the filters.</div>`;
}

function featureDetails(role, f) {
  let dl = `<dt>Description</dt><dd>${esc(f.description)}</dd><dt>Profile</dt><dd>${esc(f.profile || "core")}</dd>`;
  if (f.cases?.length) dl += `<dt>Cases</dt><dd>${f.cases.map((c) => `<code>${esc(c)}</code>`).join(" ")}</dd>`;
  if (f.covers?.length) dl += `<dt>Covers</dt><dd>${f.covers.map((c) => `<code>${esc(c)}</code>`).join(" ")}</dd>`;
  dl += `<dt>Kinds applied</dt><dd>${f.kinds.length ? groupBy(f.kinds, (k) => DATA.kindAPIs[k]).map(([api, ks]) =>
    `${esc(API_LABELS[api] || api)}: ${ks.map((k) => `<code>${esc(k)}</code>`).join(" ")}`).join("<br>") : "none (default behavior)"}</dd>`;
  for (const i of role.impls) {
    const c = f.cells[i];
    let v = `<span class="st ${esc(c.status)}">${esc(c.status)}</span>`;
    if (c.cases?.length) v += ` passing: ${c.cases.map((x) => `<code>${esc(x)}</code>`).join(" ")}`;
    if (c.issue) v += ` · ${linkIssue(c.issue)}`;
    if (c.reason) v += `\n${esc(c.reason)}`;
    dl += `<dt>${esc(i)}</dt><dd>${v}</dd>`;
  }
  return `<tr class="details"><td colspan="${2 + role.impls.length}"><dl>${dl}</dl></td></tr>`;
}

// Surface tab: rows are API surface entries grouped by area and kind, columns are roles.
function renderSurface() {
  const counts = {};
  for (const e of DATA.surface) if (surfaceVisible(e, true)) counts[e.status] = (counts[e.status] || 0) + 1;
  renderChips("surface", SURFACE_STATUSES, counts);
  const roles = DATA.roles.map((r) => r.name);
  const visible = (e) => surfaceVisible(e);
  const groups = groupBy(DATA.surface, (e) => (KIND_AREAS[e.kind] || "Other") + " › " + e.kind);
  let rows = "";
  let shown = 0;
  for (const [g, all] of groups) {
    const es = all.filter(visible);
    if (!es.length) continue;
    shown += es.length;
    const gk = "surface/" + g;
    const stats = roles.map((r) => {
      const inScope = all.filter((e) => e.scope?.includes(r)).length;
      const cov = all.filter((e) => e.coveredBy?.[r]).length;
      return `<th class="impl" title="${cov} of ${inScope} in-scope entries covered">${inScope ? `${cov}/${inScope}` : "–"}</th>`;
    }).join("");
    rows += areaRow(gk, g, es.length, all.length, stats);
    if (state.collapsed.has(gk)) continue;
    for (const e of es) {
      const key = "surface/" + e.entry;
      const cells = roles.map((r) => {
        let s, tip;
        if (e.status === "exempt") { s = "exempt"; tip = e.reason; }
        else if (e.coveredBy?.[r]) { s = "covered"; tip = e.coveredBy[r].join("\n"); }
        else if (e.scope?.includes(r)) { s = "gap"; tip = STATUS_HELP.gap; }
        else { s = "oos"; tip = STATUS_HELP.oos; }
        return `<td class="cell"><span class="st ${s}" title="${esc(tip)}">${s === "oos" ? "–" : s}</span></td>`;
      }).join("");
      rows += `<tr class="feature" data-open="${esc(key)}"><td class="id wrap"><code>${esc(e.entry)}</code></td><td class="desc" style="white-space:nowrap"><span class="st ${esc(e.status)} ${e.reason ? "has-reason" : ""}" title="${esc(e.reason || STATUS_HELP[e.status])}">${esc(e.status)}</span> <span class="badge">${esc(e.source)}</span></td>${cells}</tr>`;
      if (state.open.has(key)) rows += surfaceDetails(e, roles);
    }
  }
  const head = `<thead><tr><th>API surface entry</th><th>Status</th>${roles.map((r) => `<th class="impl">${esc(r)}</th>`).join("")}</tr></thead>`;
  setShown(shown, DATA.surface.length);
  $("#main").innerHTML = shown ? `<table>${head}<tbody>${rows}</tbody></table>` : `<div class="empty">No entries match the filters.</div>`;
}

function surfaceDetails(e, roles) {
  let dl = `<dt>Source</dt><dd>${esc(e.source)}</dd><dt>Status</dt><dd>${esc(e.status)}</dd>`;
  if (e.reason) dl += `<dt>Exempt because</dt><dd>${esc(e.reason)}</dd>`;
  if (e.scope?.length) dl += `<dt>Scope</dt><dd>${e.scope.map(esc).join(", ")}</dd>`;
  if (e.gaps?.length) dl += `<dt>Gaps</dt><dd>${e.gaps.map(esc).join(", ")}</dd>`;
  for (const r of roles) {
    if (e.coveredBy?.[r]) dl += `<dt>${esc(r)}</dt><dd>${e.coveredBy[r].map((f) => `<code>${esc(f)}</code>`).join(" ")}</dd>`;
  }
  return `<tr class="details"><td colspan="${2 + roles.length}"><dl>${dl}</dl></td></tr>`;
}

function bar(counts, order, total) {
  return `<div class="bar">${order.filter((s) => counts[s]).map((s) =>
    `<span style="width:${(100 * counts[s] / total).toFixed(2)}%;background:${color(s)}" title="${s}: ${counts[s]}"></span>`).join("")}</div>`;
}

function renderOverview() {
  $("#chips").innerHTML = "";
  const legend = `<div class="legend">${FEATURE_STATUSES.map((s) => `<span><span class="dot" style="background:${color(s)}"></span> ${s}</span>`).join("")}</div>`;
  const cards = DATA.roles.map((r) => {
    const impls = r.impls.map((i) => {
      const counts = {};
      for (const f of r.features) counts[f.cells[i].status] = (counts[f.cells[i].status] || 0) + 1;
      const n = r.features.length;
      return `<div class="impl"><b>${esc(i)}${i === r.reference ? " (reference)" : ""}</b><span>${counts.supported || 0} / ${n} supported</span></div>${bar(counts, FEATURE_STATUSES, n)}`;
    }).join("");
    return `<div class="card"><h3><a href="#${esc(r.name)}" data-tab="${esc(r.name)}">${esc(r.name[0].toUpperCase() + r.name.slice(1))}</a> · ${r.features.length} features</h3>${impls}</div>`;
  }).join("");
  const sc = {};
  for (const e of DATA.surface) sc[e.status] = (sc[e.status] || 0) + 1;
  const surface = `<div class="card"><h3><a href="#surface" data-tab="surface">API surface</a> · ${DATA.surface.length} entries</h3>
    <div class="impl"><span>${SURFACE_STATUSES.map((s) => `${s}: ${sc[s] || 0}`).join(" · ")}</span></div>${bar(sc, SURFACE_STATUSES, DATA.surface.length)}</div>`;
  $("#main").innerHTML = legend + `<div class="overview">${cards}${surface}</div>`;
}

function render() {
  renderTabs();
  const role = DATA.roles.find((r) => r.name === state.tab);
  $("#toolbar").classList.toggle("hidden", state.tab === "overview");
  $("#api").classList.toggle("hidden", !role);
  if (role) renderRole(role);
  else if (state.tab === "surface") renderSurface();
  else renderOverview();
  stickyOffsets();
}

// Keeps the table header below the sticky tabs and toolbar, whose height depends on wrapping.
function stickyOffsets() {
  const nav = $("#tabs"), tb = $("#toolbar");
  tb.style.top = nav.offsetHeight + "px";
  document.documentElement.style.setProperty("--thead-top", nav.offsetHeight + tb.offsetHeight + "px");
}
window.addEventListener("resize", stickyOffsets);

function currentGroups() {
  return [...document.querySelectorAll("tr.area")].map((r) => r.dataset.group);
}

function switchTab(tab) {
  state.tab = tab;
  render();
  try { history.replaceState(null, "", "#" + encodeURIComponent(tab)); } catch { /* sandboxed */ }
}

$("#tabs").addEventListener("click", (ev) => {
  const b = ev.target.closest("button[data-tab]");
  if (b) switchTab(b.dataset.tab);
});
window.addEventListener("hashchange", () => { state.tab = hashTab(); render(); });
$("#q").addEventListener("input", (ev) => { state.q = ev.target.value.trim(); render(); });
$("#hideUnimpl").checked = state.hideUnimpl;
$("#hideUnimpl").addEventListener("change", (ev) => { state.hideUnimpl = ev.target.checked; persist(); render(); });
$("#api").innerHTML = Object.entries(API_FILTERS).map(([k, v]) =>
  `<option value="${esc(k)}" title="${esc(v.help || "")}"${k === state.api ? " selected" : ""}>${esc(v.label)}</option>`).join("");
$("#api").addEventListener("change", (ev) => { state.api = ev.target.value; persist(); render(); });
$("#chips").addEventListener("click", (ev) => {
  const c = ev.target.closest("[data-chip]");
  if (!c) return;
  const [kind, s] = c.dataset.chip.split(":");
  const off = state.off[kind];
  off.has(s) ? off.delete(s) : off.add(s);
  render();
});
$("#expandAll").addEventListener("click", () => { currentGroups().forEach((g) => state.collapsed.delete(g)); persist(); render(); });
$("#collapseAll").addEventListener("click", () => { currentGroups().forEach((g) => state.collapsed.add(g)); persist(); render(); });
$("#main").addEventListener("click", (ev) => {
  const link = ev.target.closest("a");
  if (link) {
    if (link.dataset.tab) {
      ev.preventDefault();
      switchTab(link.dataset.tab);
    }
    return;
  }
  const area = ev.target.closest("tr.area");
  if (area) {
    const g = area.dataset.group;
    state.collapsed.has(g) ? state.collapsed.delete(g) : state.collapsed.add(g);
    persist();
    render();
    return;
  }
  const row = ev.target.closest("tr[data-open]");
  if (row) {
    const k = row.dataset.open;
    state.open.has(k) ? state.open.delete(k) : state.open.add(k);
    render();
  }
});
$("#gen").textContent = `${DATA.roles.reduce((n, r) => n + r.features.length, 0)} feature × role pairs · ${DATA.surface.length} API surface entries`;
render();
window.pcsReady = true;
})();
