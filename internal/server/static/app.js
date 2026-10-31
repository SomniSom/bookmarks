const treeEl = document.getElementById("tree");
const preview = document.getElementById("preview");
const tagsEl = document.getElementById("tags");
const searchEl = document.getElementById("search");
const dlg = document.getElementById("dlg");

let tree = [];
let tags = [];
let selected = null;
let filterTag = null;
let query = "";
let collapsed = new Set(JSON.parse(localStorage.getItem("collapsed") || "[]"));

const $ = (id) => document.getElementById(id);

async function api(path, opts = {}) {
  const res = await fetch(path, {
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (res.status === 401) {
    location.href = "/login";
    throw new Error("unauthorized");
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) {
    const err = new Error(data.error || res.statusText);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

function saveCollapsed() {
  localStorage.setItem("collapsed", JSON.stringify([...collapsed]));
}

function walk(nodes, fn, acc = []) {
  for (const n of nodes || []) {
    fn(n, acc);
    if (n.children) walk(n.children, fn, acc);
  }
}

function findNode(id, nodes = tree) {
  for (const n of nodes) {
    if (n.id === id) return n;
    const c = findNode(id, n.children || []);
    if (c) return c;
  }
  return null;
}

function folderOptions(nodes = tree, prefix = "", skip = null) {
  const skipSet = new Set();
  if (skip) {
    const stack = [...(nodes || [])];
    let found = null;
    const seen = new Set();
    while (stack.length) {
      const n = stack.pop();
      if (!n || seen.has(n.id)) continue;
      seen.add(n.id);
      if (n.id === skip) {
        found = n;
        break;
      }
      if (Array.isArray(n.children)) stack.push(...n.children);
    }
    if (found) {
      const q = [found];
      while (q.length) {
        const n = q.pop();
        if (!n || skipSet.has(n.id)) continue;
        skipSet.add(n.id);
        if (Array.isArray(n.children)) q.push(...n.children);
      }
    } else skipSet.add(skip);
  }
  let html = `<option value="">Корень</option>`;
  const stack = [];
  for (let i = (nodes || []).length - 1; i >= 0; i--) stack.push({ n: nodes[i], pfx: "" });
  const seen = new Set();
  while (stack.length) {
    const { n, pfx } = stack.pop();
    if (!n || seen.has(n.id)) continue;
    seen.add(n.id);
    if (n.type === "folder" && !skipSet.has(n.id)) {
      html += `<option value="${n.id}">${esc(pfx + n.title)}</option>`;
      const ch = n.children || [];
      for (let i = ch.length - 1; i >= 0; i--) stack.push({ n: ch[i], pfx: pfx + n.title + " / " });
    }
  }
  return html;
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

function tagColor(name) {
  let h = 0;
  for (const ch of name) h = (h * 33 + ch.charCodeAt(0)) >>> 0;
  return `hsl(${h % 360} 28% 42%)`;
}

function matchesFilter(n) {
  if (n.type === "folder") {
    return (n.children || []).some(matchesFilter);
  }
  if (filterTag && !(n.tags || []).some((t) => t.id === filterTag)) return false;
  if (query) {
    const q = query.toLowerCase();
    const hit =
      n.title.toLowerCase().includes(q) ||
      (n.url || "").toLowerCase().includes(q) ||
      (n.tags || []).some((t) => t.name.toLowerCase().includes(q));
    if (!hit) return false;
  }
  return true;
}

function renderTags() {
  tagsEl.innerHTML = tags
    .map(
      (t) =>
        `<button type="button" class="chip ${filterTag === t.id ? "active" : ""}" data-id="${t.id}" style="border-color:${tagColor(t.name)}">${esc(t.name)}</button>`
    )
    .join("");
  tagsEl.querySelectorAll(".chip").forEach((b) => {
    b.onclick = () => {
      filterTag = filterTag === b.dataset.id ? null : b.dataset.id;
      render();
    };
  });
}

function renderTree(nodes = tree, depth = 0) {
  let html = "";
  for (const n of nodes) {
    if (query || filterTag) {
      if (n.type === "folder" && !matchesFilter(n)) continue;
      if (n.type === "bookmark" && !matchesFilter(n)) continue;
    }
    const pad = `padding-left:${0.6 + depth * 0.9}rem`;
    const open = !collapsed.has(n.id);
    const tw = n.type === "folder" ? (open ? "▾" : "▸") : "";
    const ico = n.type === "folder" ? "📁" : "🔖";
    const dead = n.linkStatus && n.linkStatus >= 400 ? "dead" : "";
    html += `<div class="row ${selected === n.id ? "selected" : ""} ${dead}" data-id="${n.id}" data-type="${n.type}" style="${pad}" draggable="true">
      <span class="tw" data-tw="${n.id}">${tw}</span>
      <span class="ico">${n.favicon && n.type === "bookmark" ? `<img src="${esc(n.favicon)}" width="14" height="14" alt="">` : ico}</span>
      <span class="title">${esc(n.title)}</span>
    </div>`;
    if (n.type === "folder" && open) html += renderTree(n.children || [], depth + 1);
  }
  return html;
}

function bindTree() {
  treeEl.querySelectorAll(".row").forEach((row) => {
    row.onclick = (e) => {
      if (e.target.dataset.tw != null) {
        const id = e.target.dataset.tw;
        if (collapsed.has(id)) collapsed.delete(id);
        else collapsed.add(id);
        saveCollapsed();
        render();
        return;
      }
      selected = row.dataset.id;
      render();
    };
    row.ondblclick = () => {
      const n = findNode(row.dataset.id);
      if (n?.url) window.open(n.url, "_blank", "noopener");
    };
    row.ondragstart = (e) => {
      e.dataTransfer.setData("text/plain", row.dataset.id);
    };
    row.ondragover = (e) => {
      e.preventDefault();
      row.classList.add("drop");
    };
    row.ondragleave = () => row.classList.remove("drop");
    row.ondrop = async (e) => {
      e.preventDefault();
      row.classList.remove("drop");
      const id = e.dataTransfer.getData("text/plain");
      const target = findNode(row.dataset.id);
      const parentId = target.type === "folder" ? target.id : target.parentId;
      try {
        await api(`/api/nodes/${id}/move`, {
          method: "POST",
          body: JSON.stringify({ parentId: parentId || null, beforeId: target.type === "bookmark" ? target.id : null }),
        });
        await reload();
      } catch (err) {
        alert(err.message);
      }
    };
  });
}

function linkBadge(n) {
  if (!n.linkCheckedAt) return "";
  if (n.linkError) return `<p class="flash">Ошибка: ${esc(n.linkError)}</p>`;
  if (n.linkStatus >= 400) return `<p class="flash">HTTP ${n.linkStatus}</p>`;
  if (n.linkStatus) return `<p class="muted">HTTP ${n.linkStatus}</p>`;
  return "";
}

function renderPreview() {
  const n = selected ? findNode(selected) : null;
  if (!n) {
    preview.innerHTML = `<div class="empty">${tree.length ? "Выберите закладку или папку." : "Пока пусто — создайте папку или ссылку."}</div>`;
    return;
  }
  if (n.type === "folder") {
    preview.innerHTML = `<article class="card">
      <p class="eyebrow">папка</p>
      <h1>${esc(n.title)}</h1>
      <p class="muted">${(n.children || []).length} элементов</p>
      <div class="actions">
        <button type="button" data-act="rename">Изменить</button>
        <button type="button" class="danger" data-act="del">В корзину</button>
      </div></article>`;
  } else {
    const chips = (n.tags || [])
      .map((t) => `<span class="chip" style="border-color:${tagColor(t.name)}">${esc(t.name)}</span>`)
      .join("");
    preview.innerHTML = `<article class="card">
      ${n.ogImage ? `<a class="hero-wrap" href="${esc(n.ogImage)}" target="_blank" rel="noopener"><img class="hero" src="${esc(n.ogImage)}" alt=""></a>` : ""}
      <p class="eyebrow">закладка</p>
      <h1>${esc(n.ogTitle || n.title)}</h1>
      <p class="meta">${esc(n.ogDescription || "")}</p>
      <p class="url"><a href="${esc(n.url)}" target="_blank" rel="noopener">${esc(n.url)}</a></p>
      ${linkBadge(n)}
      <p>${chips}</p>
      <p class="muted">${esc(n.notes || "")}</p>
      <div class="actions">
        <button type="button" data-act="edit">Изменить</button>
        <button type="button" data-act="og">Обновить превью</button>
        <button type="button" data-act="check">Проверить ссылку</button>
        <button type="button" class="danger" data-act="del">В корзину</button>
      </div></article>`;
  }
  preview.querySelectorAll("[data-act]").forEach((b) => {
    b.onclick = () => act(b.dataset.act, n);
  });
}

async function act(kind, n) {
  try {
    if (kind === "del") {
      await api(`/api/nodes/${n.id}`, { method: "DELETE" });
      selected = null;
      await reload();
    } else if (kind === "og") {
      await api(`/api/nodes/${n.id}/og`, { method: "POST", body: "{}" });
      await reload();
    } else if (kind === "check") {
      await api(`/api/nodes/${n.id}/check-link`, { method: "POST", body: "{}" });
      await reload();
    } else if (kind === "rename" || kind === "edit") {
      openDialog(n);
    }
  } catch (e) {
    alert(e.message);
  }
}

function render() {
  renderTags();
  treeEl.innerHTML = renderTree() || `<div class="empty" style="padding:1rem">Ничего не найдено</div>`;
  bindTree();
  renderPreview();
}

async function reload() {
  const [t, tg] = await Promise.all([api("/api/tree"), api("/api/tags")]);
  tree = t;
  tags = tg;
  render();
}

function openDialog(edit) {
  $("dupWarn").classList.add("hidden");
  $("dlgTitle").textContent = edit ? (edit.type === "folder" ? "Папка" : "Закладка") : $("dlgTitle").textContent;
  $("fTitle").value = edit?.title || "";
  $("fUrl").value = edit?.url || "";
  $("fNotes").value = edit?.notes || "";
  $("fTags").value = (edit?.tags || []).map((t) => t.name).join(", ");
  $("fParent").innerHTML = folderOptions(tree, "", edit?.id);
  $("fParent").value = edit?.parentId || "";
  const isFolder = edit ? edit.type === "folder" : $("dlgTitle").textContent.includes("папка") || $("dlgTitle").dataset.type === "folder";
  const type = edit?.type || $("dlgTitle").dataset.type || "bookmark";
  $("urlWrap").classList.toggle("hidden", type === "folder");
  $("tagsWrap").classList.toggle("hidden", type === "folder");
  $("notesWrap").classList.toggle("hidden", type === "folder");
  dlg.dataset.edit = edit ? edit.id : "";
  dlg.dataset.type = type;
  dlg.showModal();
}

$("btnFolder").onclick = () => {
  $("dlgTitle").textContent = "Новая папка";
  $("dlgTitle").dataset.type = "folder";
  openDialog(null);
};
$("btnBookmark").onclick = () => {
  $("dlgTitle").textContent = "Новая закладка";
  $("dlgTitle").dataset.type = "bookmark";
  openDialog(null);
};
$("dlgCancel").onclick = () => dlg.close();

$("dlgForm").onsubmit = (e) => e.preventDefault();
$("dlgOk").onclick = async (e) => {
  e.preventDefault();
  const type = dlg.dataset.type || "bookmark";
  const body = {
    type,
    title: $("fTitle").value,
    url: $("fUrl").value,
    notes: $("fNotes").value,
    tags: $("fTags").value.split(",").map((s) => s.trim()).filter(Boolean),
    parentId: $("fParent").value || null,
    confirmDuplicate: !$("dupWarn").classList.contains("hidden"),
  };
  try {
    if (dlg.dataset.edit) {
      const id = dlg.dataset.edit;
      const prev = findNode(id);
      await api(`/api/nodes/${id}`, { method: "PATCH", body: JSON.stringify(body) });
      const nextParent = $("fParent").value || "";
      const prevParent = prev?.parentId || "";
      if (nextParent !== prevParent) {
        await api(`/api/nodes/${id}/move`, {
          method: "POST",
          body: JSON.stringify({ parentId: nextParent || null }),
        });
      }
    } else {
      await api("/api/nodes", { method: "POST", body: JSON.stringify(body) });
    }
    dlg.close();
    await reload();
  } catch (err) {
    if (err.status === 409 && err.data?.duplicates) {
      $("dupWarn").classList.remove("hidden");
      $("dupWarn").textContent =
        "Уже есть: " + err.data.duplicates.map((d) => d.path).join("; ") + ". Сохраните ещё раз, чтобы подтвердить.";
    } else alert(err.message);
  }
};

searchEl.oninput = () => {
  query = searchEl.value.trim();
  render();
};

treeEl.onkeydown = async (e) => {
  const rows = [...treeEl.querySelectorAll(".row")];
  const idx = rows.findIndex((r) => r.dataset.id === selected);
  if (e.key === "/" && document.activeElement !== searchEl) {
    e.preventDefault();
    searchEl.focus();
  } else if (e.key === "ArrowDown") {
    e.preventDefault();
    const n = rows[Math.min(idx + 1, rows.length - 1)];
    if (n) {
      selected = n.dataset.id;
      render();
    }
  } else if (e.key === "ArrowUp") {
    e.preventDefault();
    const n = rows[Math.max(idx - 1, 0)];
    if (n) {
      selected = n.dataset.id;
      render();
    }
  } else if (e.key === "Enter" && selected) {
    const n = findNode(selected);
    if (n?.url) window.open(n.url, "_blank", "noopener");
  } else if (e.key === " " && selected) {
    e.preventDefault();
    const n = findNode(selected);
    if (n?.type === "folder") {
      if (collapsed.has(n.id)) collapsed.delete(n.id);
      else collapsed.add(n.id);
      saveCollapsed();
      render();
    }
  } else if (e.key === "F2" && selected) {
    openDialog(findNode(selected));
  } else if (e.key === "Delete" && selected) {
    if (confirm("В корзину?")) {
      await api(`/api/nodes/${selected}`, { method: "DELETE" });
      selected = null;
      await reload();
    }
  }
};

reload().catch((e) => {
  preview.innerHTML = `<p class="flash">${esc(e.message)}</p>`;
});
