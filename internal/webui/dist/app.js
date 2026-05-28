const views = [
  { id: "overview", label: "Overview" },
  { id: "library", label: "Library" },
  { id: "updates", label: "Updates" },
  { id: "projects", label: "Projects" },
  { id: "matrix", label: "Matrix" },
];

let currentView = "overview";
let cacheBust = () => Date.now();

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
  return api("/api/v1/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
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
  const skills = await api("/api/v1/skills");
  document.getElementById("page-subtitle").textContent = skills.length + " skills in catalog";
  const table = el("table");
  const thead = el("thead");
  thead.appendChild(el("tr", null, [
    el("th", { text: "Name" }),
    el("th", { text: "Categories" }),
    el("th", { text: "Summary" }),
  ]));
  table.appendChild(thead);
  const tbody = el("tbody");
  skills.forEach((s) => {
    tbody.appendChild(el("tr", null, [
      el("td", { text: s.name }),
      el("td", { text: (s.categories || []).join(", ") }),
      el("td", { text: (s.summary || "").slice(0, 80) }),
    ]));
  });
  table.appendChild(tbody);
  content.appendChild(table);
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
