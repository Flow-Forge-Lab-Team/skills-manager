const views = [
  { id: "overview", label: "Overview" },
  { id: "library", label: "Library" },
  { id: "updates", label: "Updates" },
  { id: "projects", label: "Projects" },
  { id: "matrix", label: "Matrix" },
];

let currentView = "overview";
let cacheBust = () => Date.now();
let sessionToken = null;
let libraryState = { search: "", category: "", tag: "", source: "", compatibility: "", requirements: "", sort: "name", page: 1, pageSize: 25 };

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

async function renderOverview(content) {
  const o = await api("/api/v1/overview");
  document.getElementById("page-subtitle").textContent =
    "Live state from disk · " + (o.generated_at || "");
  setHomePill(o.home || "");
  content.appendChild(el("div", { className: "stats" }, [
    statCard("Library skills", o.library_skills),
    statCard("Projects", o.projects),
    statCard("Pending updates", o.pending_updates),
    statCard("Unregistered", o.unregistered),
  ]));
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
    else if (currentView.startsWith("skill:")) await renderSkillDetail(content, currentView.slice("skill:".length));
    else if (currentView.startsWith("project:")) await renderProjectDetail(content, currentView.slice("project:".length));
  } catch (e) {
    content.appendChild(el("div", { className: "error", text: e.message }));
  }
}

render();
