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
      className: "nav-item" + (v.id === currentView ? " active" : ""),
      onClick: () => { currentView = v.id; render(); },
    }, [v.label]));
  });
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
    tbody.appendChild(el("tr", null, [
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

async function renderUpdates(content) {
  const updates = await api("/api/v1/updates");
  document.getElementById("page-subtitle").textContent = updates.length + " pending";
  if (!updates.length) {
    content.appendChild(el("p", { className: "muted", text: "No pending updates." }));
    return;
  }
  updates.forEach((u) => {
    const card = el("div", { className: "stat" });
    card.style.marginBottom = "12px";
    card.appendChild(el("div", { className: "value", text: u.skill_name }));
    const from = (u.from_version || "").slice(0, 7);
    const to = (u.to_version || "").slice(0, 7);
    card.appendChild(el("p", { className: "muted", text: from + " → " + to + " · " + (u.source || "") }));
    const diffBtn = el("button", { className: "btn", text: "View diff" });
    diffBtn.onclick = async () => {
      try {
        const diff = await api("/api/v1/updates/" + encodeURIComponent(u.skill_name) + "/diff");
        content.querySelectorAll("pre.diff").forEach((n) => n.remove());
        card.appendChild(el("pre", { className: "diff", text: diff || "(no changes)" }));
      } catch (e) {
        alert(e.message);
      }
    };
    card.appendChild(diffBtn);
    content.appendChild(card);
  });
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
    tbody.appendChild(el("tr", null, [
      el("td", { text: p.slug }),
      el("td", { text: p.project_path }),
      el("td", { text: String(p.skill_count || 0) }),
    ]));
  });
  table.appendChild(tbody);
  content.appendChild(table);
}

async function renderMatrix(content) {
  const t0 = performance.now();
  const m = await api("/api/v1/matrix");
  const ms = Math.round(performance.now() - t0);
  document.getElementById("page-subtitle").textContent =
    m.skills.length + " skills × " + m.projects.length + " projects · loaded in " + ms + "ms";
  const wrap = el("div", { className: "matrix-wrap" });
  const table = el("table");
  const headRow = [el("th", { text: "Skill" })];
  m.projects.forEach((p) => headRow.push(el("th", { text: p })));
  const thead = el("thead");
  thead.appendChild(el("tr", null, headRow));
  table.appendChild(thead);
  const tbody = el("tbody");
  const installed = m.cells || {};
  m.skills.forEach((skill) => {
    const row = [el("td", { text: skill })];
    m.projects.forEach((proj) => {
      const hits = (installed[proj] || []).includes(skill);
      row.push(el("td", { className: hits ? "installed" : "", text: hits ? "●" : "" }));
    });
    tbody.appendChild(el("tr", null, row));
  });
  table.appendChild(tbody);
  wrap.appendChild(table);
  content.appendChild(wrap);
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
  document.getElementById("page-title").textContent = view.label;
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
  } catch (e) {
    content.appendChild(el("div", { className: "error", text: e.message }));
  }
}

render();
