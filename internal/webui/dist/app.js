const views = [
  { id: "overview", label: "Overview" },
  { id: "library", label: "Library" },
  { id: "updates", label: "Updates" },
  { id: "projects", label: "Projects" },
  { id: "matrix", label: "Matrix" },
  { id: "cross-machine", label: "Cross-machine" },
  { id: "settings", label: "Settings" },
  { id: "discover", label: "Discover" },
];

let currentView = "overview";
let cacheBust = () => Date.now();
let sessionToken = null;
let libraryState = { search: "", category: "", tag: "", source: "", compatibility: "", requirements: "", sort: "name", page: 1, pageSize: 25 };
let scanAutoIngestState = null;
let assessmentActionState = {};

async function ensureSession() {
  if (sessionToken) return;
  const s = await api("/api/v1/session");
  sessionToken = s.token;
}

async function api(path, opts) {
  const sep = path.includes("?") ? "&" : "?";
  const res = await fetch(path + sep + "_=" + cacheBust(), {
    cache: "no-store",
    ...opts,
    headers: { ...(opts && opts.headers), "Cache-Control": "no-store" },
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const err = await res.json();
      if (err.error) msg = err.error;
    } catch (_) {}
    throw new Error(msg);
  }
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("application/json")) return res.json();
  return res.text();
}

async function runCLI(args) {
  await ensureSession();
  return api("/api/v1/run", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Skills-Manager-Token": sessionToken,
    },
    body: JSON.stringify({ args }),
  });
}

async function updateAction(skill, action, body) {
  await ensureSession();
  return api("/api/v1/updates/" + encodeURIComponent(skill) + "/" + action, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Skills-Manager-Token": sessionToken,
    },
    body: JSON.stringify(body || {}),
  });
}

async function deleteNotification(file) {
  await ensureSession();
  return api("/api/v1/notifications/" + encodeURIComponent(file), {
    method: "DELETE",
    headers: { "X-Skills-Manager-Token": sessionToken },
  });
}

async function scanAutoIngest() {
  await ensureSession();
  return api("/api/v1/scan/auto-ingest", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Skills-Manager-Token": sessionToken,
    },
    body: "{}",
  });
}

async function dashboardAction(endpoint, body) {
  await ensureSession();
  return api("/api/v1/actions/" + endpoint, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Skills-Manager-Token": sessionToken,
    },
    body: JSON.stringify(body || {}),
  });
}

function el(tag, attrs, children) {
  const n = document.createElement(tag);
  if (attrs) {
    Object.entries(attrs).forEach(([k, v]) => {
      if (k === "className") n.className = v;
      else if (k === "text") n.textContent = v;
      else if (k.startsWith("on")) n.addEventListener(k.slice(2).toLowerCase(), v);
      else n.setAttribute(k, v);
    });
  }
  (children || []).forEach((c) => {
    if (typeof c === "string") n.appendChild(document.createTextNode(c));
    else if (c) n.appendChild(c);
  });
  return n;
}

function setHomePill(home) {
  const pill = document.getElementById("home-pill");
  pill.replaceChildren();
  pill.appendChild(el("strong", { text: "Home" }));
  pill.appendChild(document.createElement("br"));
  pill.appendChild(document.createTextNode(home || ""));
}

function renderNav() {
  const nav = document.getElementById("nav");
  nav.replaceChildren();
  views.forEach((v) => {
    nav.appendChild(el("div", {
      className: "nav-item" + (navActive(v.id) ? " active" : ""),
      onClick: () => { currentView = v.id; render(); },
    }, [v.label]));
  });
}

function navActive(id) {
  return currentView === id || (id === "library" && currentView.startsWith("skill:")) || (id === "projects" && currentView.startsWith("project:"));
}

function statCard(label, value) {
  return el("div", { className: "stat" }, [
    el("div", { className: "label", text: label }),
    el("div", { className: "value", text: String(value) }),
  ]);
}

function badge(text, tone) {
  return el("span", { className: "badge" + (tone ? " " + tone : ""), text });
}

function selectControl(label, value, options, onChange) {
  const select = el("select", { "aria-label": label });
  select.appendChild(el("option", { value: "", text: label }));
  options.forEach((opt) => select.appendChild(el("option", { value: opt, text: opt })));
  select.value = value || "";
  select.addEventListener("change", () => onChange(select.value));
  return select;
}

function parseCSV(value) {
  return (value || "").split(",").map((v) => v.trim()).filter(Boolean);
}

function field(label, node) {
  return el("label", { className: "field" }, [
    el("span", { text: label }),
    node,
  ]);
}

function detailList(values) {
  const items = values || [];
  if (!items.length) return [el("span", { className: "muted", text: "None" })];
  return items.map((v) => badge(v));
}

function jsonBlock(value) {
  return el("pre", { className: "diff compact", text: JSON.stringify(value || {}, null, 2) });
}

function watcherTone(type) {
  // drift/user-edit mean a tracked skill changed unexpectedly — flag amber.
  return (type === "drift" || type === "user-edit") ? "warn" : "";
}

// renderWatcherPanel builds the Overview panel for filesystem-watcher detections
// (~/.skills-manager/notifications/). Returns null when there are none so the
// Overview stays uncluttered. Ingest runs `add <path> --yes`; Dismiss deletes
// the notification file via the gated endpoint. Both re-render on success.
function renderWatcherPanel(notifications) {
  if (!notifications || notifications.length === 0) return null;
  const panel = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Watcher alerts (" + notifications.length + ")" }),
  ]);
  const list = el("div", { className: "activity-list" });
  notifications.forEach((n) => {
    const actions = el("div", { className: "update-actions" });
    if (n.type === "ingest-candidate") {
      const ingestBtn = el("button", { className: "btn confirm", text: "Ingest" });
      ingestBtn.onclick = async () => {
        if (!confirm("Ingest skill from " + n.path + " into the library?")) return;
        ingestBtn.disabled = true;
        try {
          const r = await runCLI(["add", n.path, "--yes"]);
          if (r.exit_code !== 0) { alert(r.stderr || r.stdout || "Ingest failed"); ingestBtn.disabled = false; return; }
          render();
        } catch (e) { alert(e.message); ingestBtn.disabled = false; }
      };
      actions.appendChild(ingestBtn);
    }
    const dismissBtn = el("button", { className: "btn danger", text: "Dismiss" });
    dismissBtn.onclick = async () => {
      if (!confirm("Dismiss this watcher alert? This deletes the notification file.")) return;
      dismissBtn.disabled = true;
      try {
        await deleteNotification(n.file);
        render();
      } catch (e) { alert(e.message); dismissBtn.disabled = false; }
    };
    actions.appendChild(dismissBtn);

    list.appendChild(el("div", { className: "activity-row", style: "grid-template-columns: auto 1fr auto;" }, [
      badge(n.type || "watch", watcherTone(n.type)),
      el("div", null, [
        el("div", { className: "row-title", text: n.skill || "(unknown skill)" }),
        el("div", { className: "muted", text: [n.path, n.note].filter(Boolean).join(" · ") }),
      ]),
      actions,
    ]));
  });
  panel.appendChild(list);
  return panel;
}

function outcomeTone(outcome) {
  if (outcome === "ingested") return "ok";
  if (outcome === "blocked" || outcome === "failed") return "danger";
  if (outcome === "refused" || outcome === "skipped") return "warn";
  return "";
}

function missingSummary(missing) {
  const parts = [];
  if (missing && missing.tools && missing.tools.length) parts.push("tools=" + missing.tools.join(","));
  if (missing && missing.mcp_servers && missing.mcp_servers.length) parts.push("mcp_servers=" + missing.mcp_servers.join(","));
  if (missing && missing.model && missing.model.length) parts.push("model=" + missing.model.join(","));
  if (missing && missing.credentials && missing.credentials.length) parts.push("credentials=" + missing.credentials.join(","));
  if (missing && missing.runtimes && missing.runtimes.length) parts.push("runtimes=" + missing.runtimes.join(","));
  return parts.join(", ");
}

function renderScanAutoIngestPanel(result) {
  if (!result) return null;
  const panel = el("section", { className: "panel scan-panel" }, [
    el("div", { className: "panel-title", text: "Scan auto-ingest result" }),
    el("div", { className: "scan-summary" }, [
      statCard("Discovered", result.discovered_count || 0),
      statCard("Eligible", result.eligible_auto_ingest_count || 0),
      statCard("Blocked", result.blocked_count || 0),
      statCard("Ignored", result.ignored_count || 0),
    ]),
  ]);

  const groups = result.missing_dependency_groups || [];
  if (groups.length) {
    const groupList = el("div", { className: "dependency-groups" });
    groups.forEach((g) => {
      groupList.appendChild(el("div", { className: "dependency-group" }, [
        badge(g.kind + "=" + g.name, "danger"),
        el("span", { className: "muted", text: (g.candidates || []).join(", ") }),
      ]));
    });
    panel.appendChild(groupList);
  }

  const outcomes = result.outcomes || [];
  if (!outcomes.length) {
    panel.appendChild(el("p", { className: "empty", text: "No candidates found." }));
    return panel;
  }
  const list = el("div", { className: "activity-list" });
  outcomes.forEach((o) => {
    const details = [o.path, o.reason, missingSummary(o.missing)].filter(Boolean).join(" · ");
    list.appendChild(el("div", { className: "activity-row" }, [
      badge(o.outcome || o.status || "scan", outcomeTone(o.outcome)),
      el("div", null, [
        el("div", { className: "row-title", text: o.name || "(unknown skill)" }),
        el("div", { className: "muted", text: details }),
      ]),
    ]));
  });
  panel.appendChild(list);
  return panel;
}

async function renderOverview(content) {
  const [o, notifications] = await Promise.all([
    api("/api/v1/overview"),
    api("/api/v1/notifications").catch(() => []),
  ]);
  document.getElementById("page-subtitle").textContent =
    "Live state from disk · " + (o.generated_at || "");
  setHomePill(o.home || "");
  content.appendChild(el("div", { className: "stats" }, [
    statCard("Library skills", o.library_skills),
    statCard("Projects", o.projects),
    statCard("Pending updates", o.pending_updates),
    statCard("Unregistered", o.unregistered),
  ]));
  const watcherPanel = renderWatcherPanel(notifications);
  if (watcherPanel) content.appendChild(watcherPanel);
  const scanPanel = renderScanAutoIngestPanel(scanAutoIngestState);
  if (scanPanel) content.appendChild(scanPanel);
  const grid = el("div", { className: "overview-grid" });
  const activity = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Recent activity" }),
  ]);
  if ((o.activity || []).length === 0) {
    activity.appendChild(el("p", { className: "empty", text: "No recent activity." }));
  } else {
    const list = el("div", { className: "activity-list" });
    (o.activity || []).forEach((item) => {
      list.appendChild(el("div", { className: "activity-row" }, [
        badge(item.kind || "event"),
        el("div", null, [
          el("div", { className: "row-title", text: item.skill_name || item.detail || "unknown" }),
          el("div", { className: "muted", text: [item.detail, item.at].filter(Boolean).join(" · ") }),
        ]),
      ]));
    });
    activity.appendChild(list);
  }
  const usage = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Most used" }),
  ]);
  if ((o.most_used || []).length === 0) {
    usage.appendChild(el("p", { className: "empty", text: "No invocation data yet." }));
  } else {
    const max = Math.max(...o.most_used.map((u) => u.count || 0), 1);
    (o.most_used || []).forEach((u) => {
      usage.appendChild(el("div", { className: "usage-row" }, [
        el("div", { className: "usage-label", text: u.skill_name }),
        el("div", { className: "usage-bar" }, [
          el("span", { style: "width:" + Math.max(4, Math.round(((u.count || 0) / max) * 100)) + "%" }),
        ]),
        el("div", { className: "usage-count", text: String(u.count || 0) }),
      ]));
    });
  }
  grid.appendChild(activity);
  grid.appendChild(usage);
  content.appendChild(grid);
  content.appendChild(el("p", { className: "muted", text: "Scheduled checks: " + (o.scheduled_check || "unknown") }));
  const actions = document.getElementById("page-actions");
  actions.replaceChildren();
  const checkBtn = el("button", { className: "btn", text: "Run check" });
  checkBtn.onclick = async () => {
    checkBtn.disabled = true;
    try {
      const r = await runCLI(["check"]);
      alert(r.exit_code === 0 ? "Check finished" : "Check exit " + r.exit_code + "\n" + r.stderr);
      cacheBust = () => Date.now();
      render();
    } finally {
      checkBtn.disabled = false;
    }
  };
  actions.appendChild(checkBtn);
  const scanBtn = el("button", { className: "btn confirm", text: "Scan auto-ingest" });
  scanBtn.onclick = async () => {
    if (!confirm("Run scan --auto-ingest now? Eligible candidates may be added to the library; dependency-blocked candidates stay skipped.")) return;
    scanBtn.disabled = true;
    try {
      scanAutoIngestState = await scanAutoIngest();
      render();
    } catch (e) {
      alert(e.message);
    } finally {
      scanBtn.disabled = false;
    }
  };
  actions.appendChild(scanBtn);
}

async function renderLibrary(content) {
  const params = new URLSearchParams();
  Object.entries(libraryState).forEach(([key, value]) => {
    if (value !== "" && value !== null && value !== undefined) params.set(key === "pageSize" ? "page_size" : key, value);
  });
  const list = await api("/api/v1/skills?" + params.toString());
  document.getElementById("page-subtitle").textContent = list.total + " skills in catalog";
  const actions = document.getElementById("page-actions");
  actions.replaceChildren();
  const search = el("input", { className: "search", placeholder: "Search skills", value: libraryState.search, type: "search" });
  let searchTimer = null;
  search.addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      libraryState.search = search.value;
      libraryState.page = 1;
      render();
    }, 180);
  });
  actions.appendChild(search);

  const filters = el("div", { className: "filters" }, [
    selectControl("Category", libraryState.category, list.categories || [], (v) => { libraryState.category = v; libraryState.page = 1; render(); }),
    selectControl("Tag", libraryState.tag, list.tags || [], (v) => { libraryState.tag = v; libraryState.page = 1; render(); }),
    selectControl("Source", libraryState.source, list.sources || [], (v) => { libraryState.source = v; libraryState.page = 1; render(); }),
    selectControl("Compatibility", libraryState.compatibility, ["compatible", "exclusive", "portable", "unknown"], (v) => { libraryState.compatibility = v; libraryState.page = 1; render(); }),
    selectControl("Requirements", libraryState.requirements, ["declared", "inferred", "none"], (v) => { libraryState.requirements = v; libraryState.page = 1; render(); }),
    selectControl("Sort", libraryState.sort, ["name", "usage", "recent", "updates"], (v) => { libraryState.sort = v || "name"; libraryState.page = 1; render(); }),
  ]);
  content.appendChild(filters);

  if ((list.skills || []).length === 0) {
    content.appendChild(el("div", { className: "empty-state" }, [
      el("div", { className: "panel-title", text: "No skills match" }),
      el("p", { className: "muted", text: "Try clearing filters or run scan/check from the CLI." }),
    ]));
    return;
  }

  const table = el("table", { className: "library-table" });
  const thead = el("thead");
  thead.appendChild(el("tr", null, [
    el("th", { text: "Name" }),
    el("th", { text: "Summary" }),
    el("th", { text: "State" }),
    el("th", { text: "Usage" }),
    el("th", { text: "Tags" }),
  ]));
  table.appendChild(thead);
  const tbody = el("tbody");
  (list.skills || []).forEach((s) => {
    tbody.appendChild(el("tr", { className: "click-row", onClick: () => { currentView = "skill:" + s.name; render(); } }, [
      el("td", null, [
        el("div", { className: "row-title", text: s.name }),
        el("div", { className: "muted", text: [(s.categories || []).join(", ") || "Uncategorized", s.source].filter(Boolean).join(" · ") }),
      ]),
      el("td", { text: (s.summary || "").slice(0, 140) }),
      el("td", null, [
        badge(s.compatibility_label || "unknown"),
        badge(s.requirements_status || "none"),
        ...((s.update_badges || []).length ? s.update_badges.map((b) => badge(b, "warn")) : (s.pending_update ? [badge("update", "warn")] : [])),
      ]),
      el("td", null, [
        el("div", { text: (s.usage_30d || 0) + " in 30d" }),
        el("div", { className: "muted", text: [s.installed_projects + " projects", s.last_activity_at].filter(Boolean).join(" · ") }),
      ]),
      el("td", null, (s.tags || []).slice(0, 4).map((t) => badge(t))),
    ]));
  });
  table.appendChild(tbody);
  content.appendChild(table);

  const maxPage = Math.max(1, Math.ceil((list.total || 0) / list.page_size));
  const pager = el("div", { className: "pager" });
  const prev = el("button", { className: "btn", text: "Prev" });
  prev.disabled = list.page <= 1;
  prev.onclick = () => { libraryState.page = Math.max(1, libraryState.page - 1); render(); };
  const next = el("button", { className: "btn", text: "Next" });
  next.disabled = list.page >= maxPage;
  next.onclick = () => { libraryState.page += 1; render(); };
  pager.appendChild(prev);
  pager.appendChild(el("span", { className: "muted", text: "Page " + list.page + " of " + maxPage }));
  pager.appendChild(next);
  content.appendChild(pager);
}

async function renderSkillDetail(content, name) {
  const detail = await api("/api/v1/skills/" + encodeURIComponent(name));
  document.getElementById("page-title").textContent = detail.name;
  document.getElementById("page-subtitle").textContent = detail.summary || "Skill detail";
  const actions = document.getElementById("page-actions");
  actions.replaceChildren();
  actions.appendChild(el("button", { className: "btn", text: "Back to library", onClick: () => { currentView = "library"; render(); } }));

  const grid = el("div", { className: "detail-grid" });
  grid.appendChild(el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Origin" }),
    el("p", { className: "muted", text: [
      detail.origin && (detail.origin.source || detail.origin.type),
      detail.origin && (detail.origin.url || detail.origin.path),
      detail.origin && (detail.origin.commit || detail.origin.version),
    ].filter(Boolean).join(" · ") || "No origin metadata." }),
    el("div", { className: "kv", text: "Fingerprint " + ((detail.fingerprint && detail.fingerprint.sha256) || "unknown") }),
  ]));
  grid.appendChild(el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "State" }),
    badge(detail.compatibility_label || "unknown"),
    badge(detail.requirements_status || "none"),
    el("p", { className: "muted", text: (detail.usage_30d || 0) + " invocations in 30d · " + ((detail.installed_projects || []).length) + " projects" }),
    el("div", null, detailList(detail.installed_projects)),
  ]));
  content.appendChild(grid);

  const cats = el("input", { value: (detail.categories || []).join(", "), placeholder: "Engineering, Product" });
  const tags = el("input", { value: (detail.tags || []).join(", "), placeholder: "go, cli" });
  const reqs = el("textarea", { rows: "8" });
  reqs.value = JSON.stringify(detail.requirements || {}, null, 2);
  const save = el("button", { className: "btn", text: "Save metadata" });
  save.onclick = async () => {
    save.disabled = true;
    try {
      await api("/api/v1/skills/" + encodeURIComponent(name), {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "X-Skills-Manager-Token": sessionToken },
        body: JSON.stringify({ categories: parseCSV(cats.value), tags: parseCSV(tags.value), requirements: JSON.parse(reqs.value || "{}") }),
      });
      render();
    } catch (e) {
      alert(e.message);
    } finally {
      save.disabled = false;
    }
  };
  const metaPanel = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Metadata" }),
    field("Categories", cats),
    field("Tags", tags),
    field("Requirements JSON", reqs),
    save,
  ]);
  content.appendChild(metaPanel);

  const mode = selectControl("Compatibility", detail.compatibility && detail.compatibility.mode, ["portable", "compatible", "exclusive"], () => {});
  mode.value = (detail.compatibility && detail.compatibility.mode) || detail.compatibility_label || "portable";
  const harness = el("input", { value: (detail.compatibility && detail.compatibility.harness) || "", placeholder: "codex" });
  const harnesses = el("input", { value: ((detail.compatibility && detail.compatibility.harnesses) || []).join(", "), placeholder: "codex, claude" });
  const reason = el("input", { value: "", placeholder: "Reason" });
  const compatSave = el("button", { className: "btn", text: "Apply compatibility" });
  compatSave.onclick = async () => {
    compatSave.disabled = true;
    try {
      await api("/api/v1/skills/" + encodeURIComponent(name) + "/compatibility", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Skills-Manager-Token": sessionToken },
        body: JSON.stringify({ mode: mode.value, harness: harness.value.trim(), harnesses: parseCSV(harnesses.value), reason: reason.value.trim() }),
      });
      render();
    } catch (e) {
      alert(e.message);
    } finally {
      compatSave.disabled = false;
    }
  };
  content.appendChild(el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Compatibility" }),
    field("Mode", mode),
    field("Exclusive harness", harness),
    field("Compatible harnesses", harnesses),
    field("Reason", reason),
    el("p", { className: "muted", text: "Preview: updates SKILL.md compatibility frontmatter, .skill-meta.yaml, and catalog.yaml." }),
    compatSave,
  ]));

  content.appendChild(el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Raw compatibility" }),
    jsonBlock(detail.compatibility || {}),
  ]));
}

async function renderUpdates(content) {
  const updates = await api("/api/v1/updates");
  document.getElementById("page-subtitle").textContent = updates.length + " pending";
  if (!updates.length) {
    content.appendChild(el("p", { className: "muted", text: "No pending updates." }));
    return;
  }
  updates.forEach((u) => content.appendChild(renderUpdateCard(u)));
}

function renderUpdateCard(u) {
  const card = el("section", { className: "panel update-card" });
  card.appendChild(el("div", { className: "panel-title", text: u.skill_name }));
  const from = (u.from_version || "").slice(0, 7);
  const to = (u.to_version || "").slice(0, 7);
  card.appendChild(el("p", { className: "muted", text: from + " → " + to + " · " + (u.source || "") }));

  // Hostile / prompt-injection prominence: deterministic, never cleared by AI.
  if (u.hostile) {
    card.appendChild(el("div", { className: "safety-banner", text: "⚠ Hostile review instructions / prompt-injection detected — review the raw diff. An AI summary cannot clear this." }));
  }

  // Deterministic safety flags (authoritative).
  const flags = u.safety_flags || [];
  const flagRow = el("div", null);
  if (flags.length) {
    flags.forEach((f) => flagRow.appendChild(badge((f.blocking ? "⛔ " : "") + f.name, f.blocking ? "danger" : "warn")));
  } else {
    flagRow.appendChild(badge("no deterministic safety flags", "ok"));
  }
  card.appendChild(flagRow);

  // Advisory AI summary panel — clearly marked advisory, cannot clear flags.
  const adv = el("div", null);
  if ((u.summary_badges || []).length || u.summary_status) {
    if (u.summary_status) adv.appendChild(badge("summary: " + u.summary_status, u.summary_status === "tainted" ? "danger" : "advisory"));
    (u.summary_badges || []).forEach((b) => adv.appendChild(badge(b, "advisory")));
    adv.appendChild(el("div", { className: "advisory-note", text: "AI summary is advisory only and does not override deterministic safety flags." }));
  }
  card.appendChild(adv);

  // Affected projects preview.
  const affected = u.affected_projects || [];
  card.appendChild(el("p", { className: "muted", text: affected.length ? "Affects " + affected.length + " project(s): " + affected.join(", ") : "Not installed in any project." }));

  // Actions.
  const actions = el("div", { className: "update-actions" });
  const diffBtn = el("button", { className: "btn", text: "View raw diff" });
  diffBtn.onclick = async () => {
    try {
      const diff = await api("/api/v1/updates/" + encodeURIComponent(u.skill_name) + "/diff");
      card.querySelectorAll("pre.diff").forEach((n) => n.remove());
      card.appendChild(el("pre", { className: "diff", text: diff || "(no changes)" }));
    } catch (e) { alert(e.message); }
  };
  actions.appendChild(diffBtn);

  const summaryBtn = el("button", { className: "btn", text: "Generate summary" });
  summaryBtn.onclick = async () => {
    summaryBtn.disabled = true;
    try {
      const res = await updateAction(u.skill_name, "summary", { mode: "auto" });
      if (res.exit_code === 0) { alert("Advisory summary generated. Reload to view badges."); render(); return; }
      // Provider unavailable → handoff fallback.
      if (confirm("No configured summary provider succeeded. Write a handoff prompt instead?")) {
        const ho = await updateAction(u.skill_name, "summary", { mode: "handoff" });
        alert(ho.exit_code === 0 ? "Handoff prompt written. Import the agent output with: skills-manager summarize " + u.skill_name + " --from <file>" : (ho.stderr || "handoff failed"));
      }
    } catch (e) { alert(e.message); }
    finally { summaryBtn.disabled = false; }
  };
  actions.appendChild(summaryBtn);

  const affectedNote = affected.length ? "\nThis affects " + affected.length + " installed project(s): " + affected.join(", ") + "." : "";
  const acceptBtn = el("button", { className: "btn confirm", text: "Accept" });
  acceptBtn.onclick = () => confirmUpdateAction(u.skill_name, "accept", "Accept the update for " + u.skill_name + "?" + (u.blocking ? "\n\nThis update has BLOCKING safety flags. Accepting is a manual override." : "") + affectedNote);
  actions.appendChild(acceptBtn);

  const rejectBtn = el("button", { className: "btn danger", text: "Reject" });
  rejectBtn.onclick = () => confirmUpdateAction(u.skill_name, "reject", "Reject and discard the pending update for " + u.skill_name + "?" + affectedNote);
  actions.appendChild(rejectBtn);

  const pinBtn = el("button", { className: "btn", text: "Pin" });
  pinBtn.onclick = () => confirmUpdateAction(u.skill_name, "pin", "Pin " + u.skill_name + " at its current version and reject this update?" + affectedNote);
  actions.appendChild(pinBtn);

  card.appendChild(actions);
  return card;
}

async function confirmUpdateAction(skill, action, message) {
  if (!confirm(message)) return;
  try {
    const res = await updateAction(skill, action, {});
    if (res.exit_code !== 0) { alert(res.stderr || res.stdout || (action + " failed")); return; }
    render();
  } catch (e) { alert(e.message); }
}

async function renderProjects(content) {
  const projects = await api("/api/v1/projects");
  document.getElementById("page-subtitle").textContent = projects.length + " registered projects";
  const table = el("table");
  const thead = el("thead");
  thead.appendChild(el("tr", null, [
    el("th", { text: "Slug" }),
    el("th", { text: "Path" }),
    el("th", { text: "Skills" }),
  ]));
  table.appendChild(thead);
  const tbody = el("tbody");
  projects.forEach((p) => {
    tbody.appendChild(el("tr", { className: "click-row", onClick: () => { currentView = "project:" + p.slug; render(); } }, [
      el("td", { text: p.slug }),
      el("td", { text: p.project_path }),
      el("td", { text: String(p.skill_count || 0) }),
    ]));
  });
  table.appendChild(tbody);
  content.appendChild(table);
}

async function renderProjectDetail(content, slug) {
  const p = await api("/api/v1/projects/" + encodeURIComponent(slug));
  document.getElementById("page-title").textContent = p.slug;
  document.getElementById("page-subtitle").textContent = p.project_path || "";
  const actions = document.getElementById("page-actions");
  actions.replaceChildren();
  actions.appendChild(el("button", { className: "btn", text: "Back to projects", onClick: () => { currentView = "projects"; render(); } }));

  content.appendChild(el("div", { className: "stats" }, [
    statCard("Installed skills", p.skill_count || 0),
    statCard("Managed paths", p.managed_paths || 0),
    statCard("Suggested", (p.suggested_skills || []).length),
    statCard("Warnings", (p.warnings || []).length),
  ]));

  content.appendChild(el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Project profile" }),
    el("div", null, detailList((p.config && p.config.Categories) || (p.config && p.config.categories))),
    el("div", null, detailList((p.config && p.config.Tags) || (p.config && p.config.tags))),
    el("p", { className: "muted", text: "Detected stack: " + ((p.detected_stack || []).join(", ") || "unknown") }),
  ]));

  const warnings = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Dependency warnings" })]);
  if (!(p.warnings || []).length) warnings.appendChild(el("p", { className: "muted", text: "No missing dependency warnings for installed skills." }));
  (p.warnings || []).forEach((w) => warnings.appendChild(el("div", { className: "activity-row" }, [
    badge(w.kind || "missing", "warn"),
    el("div", null, [
      el("div", { className: "row-title", text: w.skill }),
      el("div", { className: "muted", text: (w.names || []).join(", ") }),
    ]),
  ])));
  content.appendChild(warnings);

  renderCandidatePanel(content, "Preview skills", p.preview_skills || []);
  renderCandidatePanel(content, "Suggested skills", p.suggested_skills || []);
  renderCandidatePanel(content, "Match explanation", p.match_explain || []);
}

function renderCandidatePanel(content, title, candidates) {
  const panel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: title })]);
  if (!candidates.length) {
    panel.appendChild(el("p", { className: "muted", text: "No entries." }));
  } else {
    const table = el("table");
    const thead = el("thead");
    thead.appendChild(el("tr", null, [el("th", { text: "Skill" }), el("th", { text: "Score" }), el("th", { text: "Reasons" }), el("th", { text: "Warnings" })]));
    table.appendChild(thead);
    const tbody = el("tbody");
    candidates.slice(0, 50).forEach((c) => tbody.appendChild(el("tr", { className: "click-row", onClick: () => { currentView = "skill:" + c.name; render(); } }, [
      el("td", { text: c.name }),
      el("td", { text: String(c.score || 0) }),
      el("td", { text: (c.reasons || []).join(", ") }),
      el("td", { text: (c.warnings || []).join("; ") }),
    ])));
    table.appendChild(tbody);
    panel.appendChild(table);
  }
  content.appendChild(panel);
}

let matrixState = { colorBy: "install", category: "", tag: "", harness: "", filter: "" };

function heatClass(count) {
  if (count <= 0) return "";
  if (count >= 20) return "heat-4";
  if (count >= 8) return "heat-3";
  if (count >= 3) return "heat-2";
  return "heat-1";
}

function daysSince(ts) {
  if (!ts) return Infinity;
  const t = Date.parse(ts);
  if (isNaN(t)) return Infinity;
  return (Date.now() - t) / 86400000;
}

function recencyHeat(ts) {
  const d = daysSince(ts);
  if (d === Infinity) return "";
  if (d <= 1) return "heat-4";
  if (d <= 7) return "heat-3";
  if (d <= 30) return "heat-2";
  return "heat-1";
}

async function renderMatrix(content) {
  const t0 = performance.now();
  const m = await api("/api/v1/matrix");
  const ms = Math.round(performance.now() - t0);
  const info = m.skill_info || {};
  const usage = m.usage || {};
  const installed = m.cells || {};

  // Collect filter option values.
  const cats = new Set(), tags = new Set(), harnesses = new Set();
  m.skills.forEach((s) => {
    (info[s] && info[s].categories || []).forEach((c) => cats.add(c));
    (info[s] && info[s].tags || []).forEach((t) => tags.add(t));
    (info[s] && info[s].harnesses || []).forEach((h) => harnesses.add(h));
  });

  function skillPasses(skill) {
    const i = info[skill] || {};
    if (matrixState.category && !(i.categories || []).includes(matrixState.category)) return false;
    if (matrixState.tag && !(i.tags || []).includes(matrixState.tag)) return false;
    if (matrixState.harness && !(i.harnesses || []).includes(matrixState.harness)) return false;
    if (matrixState.filter === "missing-deps" && !i.missing_deps) return false;
    if (matrixState.filter === "safety-flag" && !i.safety_flag) return false;
    return true;
  }

  const toolbar = el("div", { className: "toolbar" });
  toolbar.appendChild(field("Color by", selectControlPreset("Color by", matrixState.colorBy,
    [["install", "Install state"], ["usage", "Usage count"], ["recency", "Recency"], ["compatibility", "Compatibility"], ["requirements", "Requirements"]],
    (v) => { matrixState.colorBy = v || "install"; render(); })));
  toolbar.appendChild(field("Category", selectControl("All categories", matrixState.category, [...cats].sort(), (v) => { matrixState.category = v; render(); })));
  toolbar.appendChild(field("Tag", selectControl("All tags", matrixState.tag, [...tags].sort(), (v) => { matrixState.tag = v; render(); })));
  toolbar.appendChild(field("Harness", selectControl("All harnesses", matrixState.harness, [...harnesses].sort(), (v) => { matrixState.harness = v; render(); })));
  toolbar.appendChild(field("Flag", selectControlPreset("No flag filter", matrixState.filter,
    [["", "No filter"], ["missing-deps", "Missing dependency"], ["safety-flag", "Pending safety flag"]],
    (v) => { matrixState.filter = v; render(); })));
  content.appendChild(toolbar);

  const visibleSkills = m.skills.filter(skillPasses);
  document.getElementById("page-subtitle").textContent =
    visibleSkills.length + "/" + m.skills.length + " skills × " + m.projects.length + " projects · colored by " + matrixState.colorBy + " · loaded in " + ms + "ms";

  const wrap = el("div", { className: "matrix-wrap" });
  const table = el("table");
  const headRow = [el("th", { text: "Skill" })];
  m.projects.forEach((p) => headRow.push(el("th", { text: p })));
  const thead = el("thead");
  thead.appendChild(el("tr", null, headRow));
  table.appendChild(thead);
  const tbody = el("tbody");

  visibleSkills.forEach((skill) => {
    const i = info[skill] || {};
    const skillCell = el("td", null, [
      el("span", { text: skill }),
    ]);
    if (i.hostile) skillCell.appendChild(badge("hostile", "danger"));
    else if (i.safety_flag) skillCell.appendChild(badge("flag", "warn"));
    if (i.missing_deps) skillCell.appendChild(badge("deps", "warn"));
    const row = [skillCell];
    m.projects.forEach((proj) => {
      const hits = (installed[proj] || []).includes(skill);
      const count = (usage[proj] || {})[skill] || 0;
      let cls = "", text = "";
      switch (matrixState.colorBy) {
        case "usage":
          cls = heatClass(count); text = count ? String(count) : "";
          break;
        case "recency":
          cls = hits ? recencyHeat(i.last_activity) : ""; text = hits ? "●" : "";
          break;
        case "compatibility":
          cls = hits ? "installed" : ""; text = hits ? (i.compatibility || "").slice(0, 3) : "";
          break;
        case "requirements":
          cls = hits ? "installed" : ""; text = hits ? (i.requirements || "").slice(0, 3) : "";
          break;
        default:
          cls = hits ? "installed" : ""; text = hits ? "●" : "";
      }
      row.push(el("td", { className: cls, text, title: hits ? skill + " in " + proj + " · usage " + count : "" }));
    });
    tbody.appendChild(el("tr", null, row));
  });
  table.appendChild(tbody);
  wrap.appendChild(table);
  content.appendChild(wrap);
}

// selectControlPreset is like selectControl but takes [value,label] pairs and
// keeps the current selection rather than prepending an empty option.
function selectControlPreset(label, value, options, onChange) {
  const select = el("select", { "aria-label": label });
  options.forEach(([val, lbl]) => select.appendChild(el("option", { value: val, text: lbl })));
  select.value = value || "";
  select.addEventListener("change", () => onChange(select.value));
  return select;
}

async function render() {
  try {
    await ensureSession();
  } catch (e) {
    document.getElementById("content").replaceChildren(el("div", { className: "error", text: e.message }));
    return;
  }
  renderNav();
  const view = views.find((v) => v.id === currentView);
  document.getElementById("page-title").textContent = view ? view.label : "";
  document.getElementById("page-subtitle").textContent = "";
  document.getElementById("page-actions").replaceChildren();
  const content = document.getElementById("content");
  content.replaceChildren();
  try {
    if (currentView === "overview") await renderOverview(content);
    else if (currentView === "library") await renderLibrary(content);
    else if (currentView === "updates") await renderUpdates(content);
    else if (currentView === "projects") await renderProjects(content);
    else if (currentView === "matrix") await renderMatrix(content);
    else if (currentView === "cross-machine") await renderCrossMachine(content);
    else if (currentView === "settings") await renderSettings(content);
    else if (currentView === "discover") await renderDiscover(content);
    else if (currentView.startsWith("skill:")) await renderSkillDetail(content, currentView.slice("skill:".length));
    else if (currentView.startsWith("project:")) await renderProjectDetail(content, currentView.slice("project:".length));
  } catch (e) {
    content.appendChild(el("div", { className: "error", text: e.message }));
  }
}

async function renderCrossMachine(content) {
  const cm = await api("/api/v1/machines");
  document.getElementById("page-subtitle").textContent =
    "this machine: " + cm.current_machine + (cm.has_remote ? " · remote configured" : " · local-only");

  if (!cm.is_git_repo) {
    content.appendChild(el("p", { className: "muted", text: "Library is not a git repository. Run `skills-manager init-library` or `join` to enable cross-machine sync." }));
    return;
  }

  const syncPanel = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Sync — backed by `skills-manager sync-library`" }),
    el("p", { className: "muted", text: "HEAD: " + (cm.head_commit || "unknown") }),
    el("pre", { className: "diff compact", text: cm.git_status || "(clean)" }),
  ]);
  if (cm.has_remote) {
    // "Refresh status" goes through the status action so the library is
    // fetched before reporting, surfacing remote divergence.
    const actions = el("div", { className: "update-actions" });
    const pull = el("button", { className: "btn confirm", text: "Pull" });
    pull.onclick = () => runSync("pull");
    const push = el("button", { className: "btn", text: "Push" });
    push.onclick = () => runSync("push");
    const status = el("button", { className: "btn", text: "Refresh status" });
    status.onclick = () => runSync("status");
    actions.appendChild(pull);
    actions.appendChild(push);
    actions.appendChild(status);
    syncPanel.appendChild(actions);
  } else {
    syncPanel.appendChild(el("p", { className: "advisory-note", text: "Local-only library (no git remote). Configure a remote with `skills-manager init-library` to enable pull/push." }));
  }
  content.appendChild(syncPanel);

  const machinePanel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Machines" })]);
  if (!(cm.machines || []).length) {
    machinePanel.appendChild(el("p", { className: "muted", text: "No machines registered yet." }));
  } else {
    const table = el("table");
    table.appendChild(el("thead", null, [el("tr", null, [
      el("th", { text: "Machine" }), el("th", { text: "Last scan" }), el("th", { text: "Tools" }),
      el("th", { text: "Skills" }), el("th", { text: "Commit" }), el("th", { text: "Git drift" }), el("th", { text: "Inventory" }),
    ])]));
    const tbody = el("tbody");
    cm.machines.forEach((mm) => {
      const driftTone = mm.drift === "in-sync" ? "ok" : (mm.drift === "diverged" ? "warn" : "");
      const invTone = mm.inventory_drift === "in-sync" ? "ok" : (mm.inventory_drift === "drifted" ? "warn" : "");
      tbody.appendChild(el("tr", null, [
        el("td", null, [el("span", { text: mm.name + (mm.current ? " (this)" : "") })]),
        el("td", { text: mm.last_scan || mm.last_synced || "—" }),
        el("td", { text: String(mm.tools_found || 0) }),
        el("td", { text: String((mm.global_skills || 0) + (mm.project_local_skills || 0)) }),
        el("td", { text: mm.last_commit || "—" }),
        el("td", null, [badge(mm.drift, driftTone)]),
        el("td", null, [badge(mm.inventory_drift || "unknown", invTone)]),
      ]));
    });
    table.appendChild(tbody);
    machinePanel.appendChild(table);
  }
  content.appendChild(machinePanel);

  const inventoryFindings = cm.inventory_findings || [];
  const invPanel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Inventory drift by machine" })]);
  if (!inventoryFindings.length) {
    invPanel.appendChild(el("p", { className: "muted", text: "No cross-machine inventory snapshots to compare yet." }));
  } else {
    inventoryFindings.slice(0, 80).forEach((f) => invPanel.appendChild(el("div", { className: "activity-row" }, [
      badge(f.status, f.status === "same" ? "ok" : "warn"),
      el("div", null, [
        el("div", { className: "row-title", text: f.skill_name }),
        el("div", { className: "muted", text: (f.machines || []).join(", ") + " · " + (f.detail || "") }),
      ]),
    ])));
  }
  content.appendChild(invPanel);

  const missing = cm.missing_locked_skills || [];
  const mlPanel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Missing locked skills" })]);
  if (!missing.length) {
    mlPanel.appendChild(el("p", { className: "muted", text: "All locked skills are present in the library." }));
  } else {
    mlPanel.appendChild(el("div", { className: "safety-banner", text: "Some projects lock skills not present in this library. Pull the library or reinstall to remediate." }));
    missing.forEach((m) => mlPanel.appendChild(el("div", { className: "activity-row" }, [
      badge("missing", "warn"),
      el("div", null, [
        el("div", { className: "row-title", text: m.project }),
        el("div", { className: "muted", text: (m.skills || []).join(", ") }),
      ]),
    ])));
  }
  content.appendChild(mlPanel);

  const overlap = cm.project_overlap || [];
  const ovPanel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Project overlap (this machine)" })]);
  if (!overlap.length) {
    ovPanel.appendChild(el("p", { className: "muted", text: "No projects registered on this machine." }));
  } else {
    const table = el("table");
    table.appendChild(el("thead", null, [el("tr", null, [el("th", { text: "Project" }), el("th", { text: "Skills" })])]));
    const tbody = el("tbody");
    overlap.forEach((p) => tbody.appendChild(el("tr", null, [
      el("td", { text: p.slug }),
      el("td", { text: (p.skills || []).join(", ") || "—" }),
    ])));
    table.appendChild(tbody);
    ovPanel.appendChild(table);
  }
  content.appendChild(ovPanel);
}

async function runSync(action) {
  await ensureSession();
  try {
    const res = await api("/api/v1/sync", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Skills-Manager-Token": sessionToken },
      body: JSON.stringify({ action }),
    });
    if (res.exit_code !== 0) { alert(res.stderr || res.stdout || (action + " failed")); return; }
    render();
  } catch (e) { alert(e.message); }
}

async function renderSettings(content) {
  const s = await api("/api/v1/settings");
  document.getElementById("page-subtitle").textContent = "local manager configuration";
  const draft = {
    mode: s.mode || "copy",
    llm_provider: s.llm_provider || "",
    llm_model: s.llm_model || "",
    llm_api_key_env: s.llm_api_key_env || "",
    update_frequency_hours: s.update_frequency_hours || 24,
  };

  const form = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Settings" })]);

  const modeSel = el("select", null, []);
  ["copy", "symlink"].forEach((m) => modeSel.appendChild(el("option", { value: m, text: m })));
  modeSel.value = draft.mode;
  modeSel.addEventListener("change", () => { draft.mode = modeSel.value; });
  form.appendChild(field("Install mode", modeSel));

  const provSel = el("select", null, []);
  ["", "anthropic", "openai", "codex-cli", "cursor-cli"].forEach((p) => provSel.appendChild(el("option", { value: p, text: p || "(none)" })));
  provSel.value = draft.llm_provider;
  provSel.addEventListener("change", () => { draft.llm_provider = provSel.value; });
  form.appendChild(field("LLM provider", provSel));

  const modelInput = el("input", { type: "text", value: draft.llm_model });
  modelInput.addEventListener("input", () => { draft.llm_model = modelInput.value; });
  form.appendChild(field("LLM model", modelInput));

  const keyInput = el("input", { type: "text", value: draft.llm_api_key_env });
  keyInput.addEventListener("input", () => { draft.llm_api_key_env = keyInput.value; });
  const keyField = field("LLM API key env var", keyInput);
  keyField.appendChild(el("div", { className: "advisory-note", text: "Name of the environment variable holding the key — never the key itself." }));
  form.appendChild(keyField);

  const freqInput = el("input", { type: "number", min: "1", value: String(draft.update_frequency_hours) });
  freqInput.addEventListener("input", () => { draft.update_frequency_hours = parseInt(freqInput.value, 10) || 24; });
  form.appendChild(field("Update frequency (hours)", freqInput));

  const saveBtn = el("button", { className: "btn confirm", text: "Save settings" });
  saveBtn.onclick = async () => {
    saveBtn.disabled = true;
    try {
      await ensureSession();
      // Always send LLM fields (including empties) so clearing a provider /
      // model / key env var actually persists rather than being dropped.
      const body = {
        mode: draft.mode,
        update_frequency_hours: draft.update_frequency_hours,
        llm_provider: draft.llm_provider,
        llm_model: draft.llm_model,
        llm_api_key_env: draft.llm_api_key_env,
      };
      const res = await fetch("/api/v1/settings", {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "X-Skills-Manager-Token": sessionToken },
        body: JSON.stringify(body),
      });
      if (!res.ok) { const e = await res.json().catch(() => ({})); alert(e.error || "save failed"); return; }
      alert("Settings saved.");
      render();
    } catch (e) { alert(e.message); }
    finally { saveBtn.disabled = false; }
  };
  form.appendChild(el("div", { className: "update-actions" }, [saveBtn]));
  content.appendChild(form);

  content.appendChild(el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Library sync" }),
    el("p", { className: "muted", text: s.library_has_remote ? "A git remote is configured. Use the Cross-machine view to pull/push." : "Local-only library (no git remote)." }),
  ]));

  content.appendChild(el("p", { className: "advisory-note", text: "Cloud scheduling (Claude routines / Codex automation) is intentionally not configurable here." }));
}

async function renderDiscover(content) {
  const a = await api("/api/v1/assessment");
  document.getElementById("page-subtitle").textContent =
    "Latest persisted discovery · " + (a.generated_at || "");
  const s = a.summary || {};
  content.appendChild(el("div", { className: "stats assessment-stats" }, [
    statCard("Global skills", s.global_skills || 0),
    statCard("Project-local", s.project_local_skills || 0),
    statCard("Detected tools", s.tools_found || 0),
    statCard("Projects", s.projects_found || 0),
    statCard("Drift groups", s.drift_groups || 0),
    statCard("Duplicates", s.duplicate_content || 0),
    statCard("Missing coverage", s.missing_tool_coverage || 0),
  ]));

  const tabs = el("div", { className: "assessment-tabs" });
  const sections = [
    ["inventory", "Inventory"],
    ["drift", "Drift"],
    ["scope", "Global vs Project"],
    ["coverage", "Tool Coverage"],
    ["recommendations", "Recommendations"],
    ["actions", "Actions"],
  ];
  const body = el("div", { className: "assessment-body" });
  const renderSection = (id) => {
    body.replaceChildren();
    tabs.querySelectorAll("button").forEach((b) => b.classList.toggle("active", b.dataset.section === id));
    if (id === "inventory") renderAssessmentInventory(body, a);
    else if (id === "drift") renderAssessmentDrift(body, a);
    else if (id === "scope") renderAssessmentScope(body, a);
    else if (id === "coverage") renderAssessmentCoverage(body, a);
    else if (id === "recommendations") renderAssessmentRecommendations(body, a);
    else renderAssessmentActions(body, a);
  };
  sections.forEach(([id, label]) => {
    tabs.appendChild(el("button", { className: "btn", "data-section": id, text: label, onClick: () => renderSection(id) }));
  });
  content.appendChild(tabs);
  content.appendChild(body);
  renderSection("inventory");
}

function renderAssessmentInventory(content, a) {
  const installs = a.installations || [];
  const panel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Inventory" })]);
  if (!installs.length) {
    panel.appendChild(el("p", { className: "empty", text: "No discovery inventory yet. Run `skills-manager discover --global` or scan project roots." }));
    content.appendChild(panel);
    return;
  }
  const table = el("table", { className: "assessment-table" });
  table.appendChild(el("thead", null, [el("tr", null, [
    el("th", { text: "Skill" }), el("th", { text: "Tool" }), el("th", { text: "Scope" }),
    el("th", { text: "Project" }), el("th", { text: "Path" }), el("th", { text: "Hash" }), el("th", { text: "Ownership" }),
  ])]));
  const tbody = el("tbody");
  installs.forEach((i) => {
    const skillCell = el("td", null, [el("button", { className: "link-button", text: i.skill_name, onClick: () => { currentView = "skill:" + i.skill_name; render(); } })]);
    tbody.appendChild(el("tr", null, [
      skillCell,
      el("td", { text: i.tool_id || "" }),
      el("td", null, [badge(i.scope || "unknown")]),
      el("td", { text: i.project_id || "global" }),
      el("td", { className: "path-cell", text: i.source_path || "" }),
      el("td", { className: "hash-cell", text: (i.content_sha256 || "").slice(0, 12) }),
      el("td", null, [badge(i.managed ? "managed" : "unmanaged", i.managed ? "ok" : "warn")]),
    ]));
  });
  table.appendChild(tbody);
  panel.appendChild(table);
  content.appendChild(panel);
}

function renderAssessmentDrift(content, a) {
  const groups = a.drift_groups || [];
  const panel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Drift" })]);
  if (!groups.length) {
    panel.appendChild(el("p", { className: "empty", text: "No drift groups in the latest inventory." }));
  } else {
    const list = el("div", { className: "activity-list" });
    groups.forEach((g) => {
      list.appendChild(el("div", { className: "activity-row assessment-row" }, [
        badge(g.group_type || "drift", g.review_status === "ignored" ? "warn" : ""),
        el("div", null, [
          el("div", { className: "row-title", text: g.skill_name || g.content_sha256 || g.group_id }),
          el("div", { className: "muted", text: [g.classification, g.review_status, g.review_reason].filter(Boolean).join(" · ") }),
          el("div", { className: "muted", text: (g.installation_ids || []).join(", ") }),
        ]),
      ]));
    });
    panel.appendChild(list);
  }
  content.appendChild(panel);
}

function renderAssessmentScope(content, a) {
  const bySkill = {};
  (a.installations || []).forEach((i) => {
    bySkill[i.skill_name] = bySkill[i.skill_name] || { global: 0, project: 0, tools: new Set() };
    bySkill[i.skill_name][i.scope] = (bySkill[i.skill_name][i.scope] || 0) + 1;
    if (i.tool_id) bySkill[i.skill_name].tools.add(i.tool_id);
  });
  const rows = Object.entries(bySkill).sort((a, b) => a[0].localeCompare(b[0]));
  const panel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Global vs Project" })]);
  const table = el("table");
  table.appendChild(el("thead", null, [el("tr", null, [el("th", { text: "Skill" }), el("th", { text: "Global" }), el("th", { text: "Project" }), el("th", { text: "Tools" })])]));
  const tbody = el("tbody");
  rows.forEach(([skill, r]) => tbody.appendChild(el("tr", null, [
    el("td", { text: skill }), el("td", { text: String(r.global || 0) }), el("td", { text: String(r.project || 0) }),
    el("td", { text: Array.from(r.tools).sort().join(", ") }),
  ])));
  table.appendChild(tbody);
  panel.appendChild(table);
  content.appendChild(panel);
}

function renderAssessmentCoverage(content, a) {
  const panel = el("section", { className: "panel" }, [el("div", { className: "panel-title", text: "Tool Coverage" })]);
  const tools = a.tools || [];
  if (!tools.length) panel.appendChild(el("p", { className: "empty", text: "No tool scan data has been persisted yet." }));
  else tools.forEach((t) => panel.appendChild(el("div", { className: "activity-row" }, [
    badge(t.detected ? "detected" : "missing", t.detected ? "ok" : "danger"),
    el("div", null, [
      el("div", { className: "row-title", text: t.display_name || t.tool_id }),
      el("div", { className: "muted", text: (t.global_roots || []).concat(t.project_patterns || []).join(" · ") }),
    ]),
  ])));
  content.appendChild(panel);
}

function renderAssessmentRecommendations(content, a) {
  const panel = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Recommendations" }),
    el("p", { className: "advisory-note", text: "Deterministic facts come from inventory. AI advisory output is not mixed into these recommendations." }),
  ]);
  const facts = a.review_facts || [];
  if (facts.length) {
    panel.appendChild(el("div", { className: "subhead", text: "Deterministic facts" }));
    facts.slice(0, 12).forEach((f) => panel.appendChild(el("div", { className: "activity-row" }, [badge(f.kind || "fact"), el("div", null, [el("div", { className: "row-title", text: f.title || f.skill_name || "fact" }), el("div", { className: "muted", text: f.detail || "" })])])));
  }
  panel.appendChild(el("div", { className: "subhead", text: "AI advisory output" }));
  panel.appendChild(el("p", { className: "muted", text: "No AI advisory output is generated for this local assessment." }));
  const recs = a.recommendations || [];
  panel.appendChild(el("div", { className: "subhead", text: "Recommended actions" }));
  if (!recs.length) panel.appendChild(el("p", { className: "empty", text: "No recommendations in the latest inventory." }));
  recs.slice(0, 20).forEach((r) => panel.appendChild(el("div", { className: "activity-row assessment-row" }, [
    badge(r.kind || "action", r.kind === "needs_port" ? "warn" : ""),
    el("div", null, [
      el("div", { className: "row-title", text: r.title || r.skill_name || r.recommendation_id }),
      el("div", { className: "muted", text: r.reason || "" }),
      el("div", { className: "advisory-note", text: "Dry-run plan required before write: skills-manager plan --inventory <discover.json> --recommendation " + r.recommendation_id }),
    ]),
  ])));
  content.appendChild(panel);
}

function renderAssessmentActions(content, a) {
  content.replaceChildren();
  const panel = el("section", { className: "panel" }, [
    el("div", { className: "panel-title", text: "Actions" }),
    el("p", { className: "muted", text: "Filesystem changes require a precomputed dry-run plan, explicit confirmation, and an audit entry." }),
  ]);
  const reviews = {};
  (a.action_reviews || []).forEach((r) => { reviews[r.recommendation_id] = r; });
  const installsByPath = {};
  (a.installations || []).forEach((i) => { installsByPath[i.source_path] = i; });
  const recs = (a.recommendations || []).slice(0, 20);
  if (!recs.length) {
    panel.appendChild(el("p", { className: "empty", text: "No actionable recommendations in the latest inventory." }));
  }
  recs.forEach((r) => {
    const id = r.recommendation_id;
    const local = assessmentActionState[id] || {};
    const review = local.review || reviews[id] || { status: "new" };
    const row = el("div", { className: "action-card" });
    row.appendChild(el("div", { className: "action-card-head" }, [
      badge(review.status || "new", actionStatusTone(review.status)),
      el("div", null, [
        el("div", { className: "row-title", text: r.title || r.skill_name || id }),
        el("div", { className: "muted", text: r.reason || "" }),
      ]),
    ]));
    const controls = el("div", { className: "action-controls" }, [
      el("button", { className: "btn", text: "Preview plan", onClick: async () => {
        local.message = "Generating dry-run plan...";
        assessmentActionState[id] = local;
        renderAssessmentActions(content, a);
        try {
          assessmentActionState[id] = await dashboardAction("plan", { recommendation_id: id });
        } catch (e) {
          assessmentActionState[id] = { error: e.message };
        }
        renderAssessmentActions(content, a);
      } }),
      el("button", { className: "btn", text: "Accept", onClick: async () => {
        assessmentActionState[id] = { review: await dashboardAction("review", { recommendation_id: id, status: "accepted", reason: "accepted in dashboard" }) };
        renderAssessmentActions(content, a);
      } }),
      el("button", { className: "btn", text: "Ignore", onClick: async () => {
        assessmentActionState[id] = { review: await dashboardAction("review", { recommendation_id: id, status: "ignored", reason: "ignored in dashboard" }) };
        renderAssessmentActions(content, a);
      } }),
    ]);
    if (local.plan && local.plan.status === "ready" && ["install_global", "install_project", "remove"].includes(local.plan.kind)) {
      controls.appendChild(el("button", { className: "btn danger", text: "Confirm apply", onClick: async () => {
        if (!window.confirm("Apply this precomputed dry-run plan and record an audit entry?")) return;
        assessmentActionState[id] = await dashboardAction("apply", { recommendation_id: id, plan: local.plan, confirm: true, reason: "confirmed in dashboard" });
        renderAssessmentActions(content, a);
      } }));
    }
    row.appendChild(controls);
    if (local.message) row.appendChild(el("p", { className: "muted", text: local.message }));
    if (local.error) row.appendChild(el("p", { className: "error", text: local.error }));
    if (local.plan) row.appendChild(renderActionPlanPreview(local.plan, installsByPath));
    if (local.stdout) row.appendChild(el("pre", { className: "diff compact", text: local.stdout }));
    panel.appendChild(row);
  });
  panel.appendChild(el("pre", { className: "diff compact", text: "CLI equivalent:\nskills-manager discover --global --projects <roots> --json > discover.json\nskills-manager plan --inventory discover.json --recommendation <id>\nskills-manager plan --inventory discover.json --recommendation <id> --apply --confirm" }));
  content.appendChild(panel);
}

function actionStatusTone(status) {
  if (status === "applied" || status === "accepted") return "ok";
  if (status === "failed") return "danger";
  if (status === "ignored") return "warn";
  return "";
}

function renderActionPlanPreview(plan, installsByPath) {
  const wrap = el("div", { className: "action-preview" }, [
    el("div", { className: "subhead", text: "Dry-run plan preview" }),
    el("div", { className: "muted", text: [plan.kind, plan.status, plan.target_tool_id, plan.target_project_id].filter(Boolean).join(" · ") }),
  ]);
  if ((plan.blockers || []).length) wrap.appendChild(el("p", { className: "error", text: "Blocked: " + plan.blockers.join("; ") }));
  const rows = [];
  ["create", "update", "remove", "preserve", "skip"].forEach((kind) => {
    ((plan.files && plan.files[kind]) || []).forEach((f) => rows.push({ kind, file: f }));
  });
  if (!rows.length) {
    wrap.appendChild(el("p", { className: "empty", text: "No filesystem changes in this plan." }));
    return wrap;
  }
  const table = el("table", { className: "assessment-table action-preview-table" });
  table.appendChild(el("thead", null, [el("tr", null, [
    el("th", { text: "Action" }), el("th", { text: "Source path" }), el("th", { text: "Destination path" }),
    el("th", { text: "Tool target" }), el("th", { text: "Hash impact" }),
  ])]));
  const tbody = el("tbody");
  rows.forEach(({ kind, file }) => {
    const source = installsByPath[file.source] || {};
    tbody.appendChild(el("tr", null, [
      el("td", null, [badge(kind, kind === "remove" ? "danger" : kind === "skip" ? "warn" : "ok")]),
      el("td", { className: "path-cell", text: file.source || "" }),
      el("td", { className: "path-cell", text: file.path || "" }),
      el("td", { text: plan.target_tool_id || source.tool_id || "" }),
      el("td", { className: "hash-cell", text: source.content_sha256 ? source.content_sha256.slice(0, 12) + " -> target recalculated" : "target recalculated" }),
    ]));
  });
  table.appendChild(tbody);
  wrap.appendChild(table);
  return wrap;
}

render();
