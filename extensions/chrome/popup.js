const serverEl = document.getElementById("server");
const passwordEl = document.getElementById("password");
const treeEl = document.getElementById("tree");
const qEl = document.getElementById("q");

let treeData = [];
let collapsed = new Set();
let parentFolderId = "";

async function persistServer(emptyToDefault) {
  const raw = serverEl.value.trim();
  if (!raw) {
    if (!emptyToDefault) return;
    const server = BM.defaultServer();
    await ext.storage.local.set({ serverUrl: server });
    return server;
  }
  if (!BM.isCompleteServer(raw)) return;
  const server = BM.normalizeServer(raw);
  await ext.storage.local.set({ serverUrl: server });
  return server;
}

async function persistDraft() {
  try {
    await persistServer();
    if (ext.storage && ext.storage.session) {
      await ext.storage.session.set({ draftPassword: passwordEl.value });
    }
  } catch (_) {}
}

function show(msg, isErr) {
  const el = document.getElementById("status");
  el.textContent = msg || "";
  el.className = isErr ? "err" : "ok";
}

function showTab(name) {
  document.getElementById("paneBrowse").hidden = name !== "browse";
  document.getElementById("paneAdd").hidden = name !== "add";
  document.getElementById("tabBrowse").classList.toggle("on", name === "browse");
  document.getElementById("tabAdd").classList.toggle("on", name === "add");
}

async function currentTab() {
  const tabs = await ext.tabs.query({ active: true, currentWindow: true });
  return tabs[0];
}

function matchesFilter(n, q) {
  if (!q) return true;
  const stack = [n];
  const seen = new Set();
  while (stack.length) {
    const cur = stack.pop();
    if (!cur || (cur.id && seen.has(cur.id))) continue;
    if (cur.id) seen.add(cur.id);
    if (cur.type !== "folder") {
      const hay = ((cur.title || "") + " " + (cur.url || "")).toLowerCase();
      if (hay.includes(q)) return true;
    }
    const ch = cur.children;
    if (Array.isArray(ch)) for (let i = 0; i < ch.length; i++) stack.push(ch[i]);
  }
  return false;
}

function flattenHits(nodes, pfx, q, out) {
  const stack = [{ n: null, list: nodes || [], pfx }];
  const seen = new Set();
  while (stack.length) {
    const cur = stack.pop();
    for (const n of cur.list || []) {
      if (!n || (n.id && seen.has(n.id))) continue;
      if (n.id) seen.add(n.id);
      if (n.type === "folder") {
        if (Array.isArray(n.children) && n.children.length) {
          stack.push({ list: n.children, pfx: cur.pfx ? cur.pfx + " / " + n.title : n.title });
        }
      } else if (matchesFilter(n, q)) {
        out.push({ node: n, path: cur.pfx });
      }
    }
  }
}

function findNode(id, nodes = treeData) {
  const stack = [...(nodes || [])];
  const seen = new Set();
  while (stack.length) {
    const n = stack.pop();
    if (!n || (n.id && seen.has(n.id))) continue;
    if (n.id) seen.add(n.id);
    if (n.id === id) return n;
    if (Array.isArray(n.children)) {
      for (let i = 0; i < n.children.length; i++) stack.push(n.children[i]);
    }
  }
  return null;
}

function h(tag, attrs, ...kids) {
  const node = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false || v === "") continue;
      if (k === "class") node.className = v;
      else if (k === "text") node.textContent = v;
      else if (k === "dataset") Object.assign(node.dataset, v);
      else node.setAttribute(k, v);
    }
  }
  for (const kid of kids) {
    if (kid == null) continue;
    node.append(typeof kid === "string" ? document.createTextNode(kid) : kid);
  }
  return node;
}

function setEmpty(msg) {
  treeEl.replaceChildren(h("div", { class: "empty" }, msg));
}

function rowEl(n, depth, extra) {
  const row = h("div", {
    class: "row" + (n.linkStatus && n.linkStatus >= 400 ? " dead" : "") + (n.type === "folder" && n.id === parentFolderId ? " selected" : ""),
    title: n.url || n.title || "",
  });
  row.dataset.id = n.id;
  row.dataset.type = n.type;
  if (depth != null) row.style.paddingLeft = 6 + depth * 12 + "px";
  const tw = h("span", { class: "tw" }, n.type === "folder" ? (extra && extra.open ? "▾" : "▸") : "");
  if (n.type === "folder") tw.dataset.tw = n.id;
  const ico = h("span", { class: "ico" });
  if (n.favicon && n.type === "bookmark" && /^https?:/i.test(n.favicon)) {
    ico.append(h("img", { src: n.favicon, alt: "" }));
  } else {
    ico.textContent = n.type === "folder" ? "📁" : "🔖";
  }
  row.append(tw, ico, h("span", { class: "title" }, n.title || n.url || ""));
  if (extra && extra.path) row.append(h("span", { class: "path" }, extra.path));
  return row;
}

function appendTree(parent, nodes, depth, q) {
  const stack = [];
  const list = nodes || [];
  for (let i = list.length - 1; i >= 0; i--) stack.push({ n: list[i], depth });
  const seen = new Set();
  while (stack.length) {
    const { n, depth: d } = stack.pop();
    if (!n || (n.id && seen.has(n.id))) continue;
    if (n.id) seen.add(n.id);
    if (q && !matchesFilter(n, q)) continue;
    const open = q || !collapsed.has(n.id);
    parent.append(rowEl(n, d, { open }));
    if (n.type === "folder" && open && Array.isArray(n.children) && n.children.length) {
      for (let i = n.children.length - 1; i >= 0; i--) stack.push({ n: n.children[i], depth: d + 1 });
    }
  }
}

function renderBrowse() {
  const q = qEl.value.trim().toLowerCase();
  if (!treeData.length) {
    setEmpty("Пока пусто — добавьте вкладку или откройте кабинет.");
    return;
  }
  if (q) {
    const hits = [];
    flattenHits(treeData, "", q, hits);
    if (!hits.length) {
      setEmpty("Ничего не найдено");
      return;
    }
    const frag = document.createDocumentFragment();
    for (const { node: n, path } of hits.slice(0, 80)) frag.append(rowEl(n, null, { path }));
    treeEl.replaceChildren(frag);
    return;
  }
  const frag = document.createDocumentFragment();
  appendTree(frag, treeData, 0, "");
  if (!frag.childNodes.length) setEmpty("Пока пусто");
  else treeEl.replaceChildren(frag);
}

async function loadTree() {
  try {
    treeData = (await BM.api("/api/tree")) || [];
    renderBrowse();
    updateFolderHint();
  } catch (e) {
    setEmpty(e.message);
    if (e.code === 401) {
      document.getElementById("auth").hidden = false;
      document.getElementById("app").hidden = true;
    }
  }
}

function fillSelect(sel, items, selected) {
  const frag = document.createDocumentFragment();
  for (const it of items) {
    const opt = h("option");
    opt.value = it.id;
    opt.textContent = it.title;
    frag.append(opt);
  }
  sel.replaceChildren(frag);
  if (selected) sel.value = selected;
}

async function fillFolders(selected) {
  const sel = document.getElementById("folder");
  try {
    const folders = BM.flattenFolders(treeData.length ? treeData : await BM.api("/api/tree"));
    fillSelect(sel, folders, selected);
  } catch (e) {
    fillSelect(sel, [{ id: "", title: "Корень" }]);
    if (e.code === 401) show(e.message, true);
  }
}

async function fillTags() {
  try {
    const tags = await BM.api("/api/tags");
    const dl = document.getElementById("taglist");
    const frag = document.createDocumentFragment();
    for (const t of tags) {
      const opt = h("option");
      opt.value = t.name;
      frag.append(opt);
    }
    dl.replaceChildren(frag);
  } catch (_) {}
}

function updateFolderHint() {
  const el = document.getElementById("folderParent");
  if (!el) return;
  if (parentFolderId && !findNode(parentFolderId)) parentFolderId = "";
  if (!parentFolderId) {
    el.textContent = "новая папка в корне";
    return;
  }
  const n = findNode(parentFolderId);
  el.textContent = n ? "новая папка в «" + n.title + "»" : "новая папка в корне";
}

async function createFolder(title, parentId) {
  title = String(title || "").trim();
  if (!title) {
    show("Введите имя папки", true);
    return;
  }
  try {
    const created = await BM.api("/api/nodes", {
      method: "POST",
      body: JSON.stringify({ type: "folder", title, parentId: parentId || null }),
    });
    parentFolderId = created.id;
    if (parentId) collapsed.delete(parentId);
    await ext.storage.local.set({ lastFolderId: created.id, collapsedIds: [...collapsed] });
    await loadTree();
    await fillFolders(created.id);
    updateFolderHint();
    show("Папка создана");
    return created;
  } catch (e) {
    show(e.message, true);
  }
}

async function openUrl(url) {
  if (!url || /^(javascript|data|vbscript):/i.test(url)) return;
  await ext.tabs.create({ url });
}

let starting = false;

async function init() {
  if (starting) return;
  starting = true;
  try {
  const { token, lastFolderId, serverUrl, collapsedIds } = await ext.storage.local.get([
    "token",
    "lastFolderId",
    "serverUrl",
    "collapsedIds",
  ]);
  collapsed = new Set(Array.isArray(collapsedIds) ? collapsedIds : []);
  parentFolderId = lastFolderId || "";
  serverEl.value = BM.isCompleteServer(serverUrl) ? BM.normalizeServer(serverUrl) : BM.defaultServer();
  if (!token && ext.storage && ext.storage.session) {
    const { draftPassword } = await ext.storage.session.get("draftPassword");
    if (draftPassword) passwordEl.value = draftPassword;
  }
  document.getElementById("auth").hidden = !!token;
  document.getElementById("app").hidden = !token;
  document.getElementById("device").textContent = "device: " + (await BM.getDeviceId()).slice(0, 8);
  if (!token) return;
  showTab("browse");
  await loadTree();
  await fillFolders(parentFolderId);
  await fillTags();
  updateFolderHint();
  const tab = await currentTab();
  if (tab) {
    document.getElementById("title").value = tab.title || "";
    document.getElementById("url").value = tab.url || "";
  }
  } finally {
    starting = false;
  }
}

document.getElementById("loginBtn").onclick = async () => {
  const raw = serverEl.value.trim();
  const server = await persistServer(!raw);
  if (!server) {
    show("Укажите полный адрес сервера, например example.com или localhost:8080", true);
    return;
  }
  await BM.ensureHostPermission(server);
  try {
    const data = await BM.api("/api/ext/login", {
      method: "POST",
      body: JSON.stringify({
        password: passwordEl.value,
        deviceId: await BM.getDeviceId(),
      }),
    });
    await ext.storage.local.set({ token: data.token });
    if (ext.storage && ext.storage.session) await ext.storage.session.remove("draftPassword");
    passwordEl.value = "";
    show("Вход выполнен");
    await init();
  } catch (e) {
    show(e.message, true);
  }
};

serverEl.addEventListener("input", () => persistServer());
serverEl.addEventListener("change", () => persistServer().then((s) => {
  if (s) serverEl.value = s;
}));
passwordEl.addEventListener("input", persistDraft);
document.addEventListener("visibilitychange", persistDraft);
window.addEventListener("pagehide", persistDraft);

document.getElementById("tabBrowse").onclick = () => {
  showTab("browse");
  loadTree();
};
document.getElementById("tabAdd").onclick = async () => {
  showTab("add");
  const { lastFolderId } = await ext.storage.local.get("lastFolderId");
  await fillFolders(lastFolderId);
};

qEl.oninput = () => renderBrowse();

treeEl.onclick = async (e) => {
  const tw = e.target.closest("[data-tw]");
  const row = e.target.closest(".row");
  if (tw && tw.dataset.tw) {
    const id = tw.dataset.tw;
    if (collapsed.has(id)) collapsed.delete(id);
    else collapsed.add(id);
    await ext.storage.local.set({ collapsedIds: [...collapsed] });
    renderBrowse();
    return;
  }
  if (!row) return;
  const n = findNode(row.dataset.id);
  if (!n) return;
  if (n.type === "folder") {
    if (collapsed.has(n.id)) collapsed.delete(n.id);
    else collapsed.add(n.id);
    await ext.storage.local.set({ collapsedIds: [...collapsed], lastFolderId: n.id });
    parentFolderId = n.id;
    document.getElementById("folder").value = n.id;
    updateFolderHint();
    renderBrowse();
    return;
  }
  if (n.url) await openUrl(n.url);
};

document.getElementById("openCabinet").onclick = async () => {
  const server = await BM.getServer();
  await ext.tabs.create({ url: server + "/" });
};

document.getElementById("saveBtn").onclick = async () => {
  const folder = document.getElementById("folder").value || null;
  const body = {
    type: "bookmark",
    title: document.getElementById("title").value,
    url: document.getElementById("url").value,
    tags: document.getElementById("tags").value.split(",").map((s) => s.trim()).filter(Boolean),
    parentId: folder,
    confirmDuplicate: document.getElementById("dup").dataset.ok === "1",
  };
  try {
    await BM.api("/api/nodes", { method: "POST", body: JSON.stringify(body) });
    await ext.storage.local.set({ lastFolderId: folder || "" });
    show("Сохранено");
    document.getElementById("dup").hidden = true;
    document.getElementById("dup").dataset.ok = "";
    await loadTree();
  } catch (e) {
    if (e.status === 409 && e.data && e.data.duplicates) {
      document.getElementById("dup").hidden = false;
      document.getElementById("dup").dataset.ok = "1";
      document.getElementById("dup").textContent =
        "Уже есть: " + e.data.duplicates.map((d) => d.path).join("; ") + ". Нажмите ещё раз, чтобы подтвердить.";
    } else show(e.message, true);
  }
};

document.getElementById("importBtn").onclick = async () => {
  const name =
    document.getElementById("importName").value.trim() ||
    `Импорт ${/Firefox/i.test(navigator.userAgent) ? "Firefox" : "Chrome"} — ${new Date().toLocaleDateString("ru-RU")}`;
  try {
    const tree = await ext.bookmarks.getTree();
    const payload = BM.browserTreeToPayload(tree);
    const res = await BM.api("/api/import/browser", {
      method: "POST",
      body: JSON.stringify({ folderName: name, tree: payload }),
    });
    show("Импортировано узлов: " + res.created + ". Уже известных URL: " + res.knownUrls);
    await loadTree();
  } catch (e) {
    show(e.message, true);
  }
};

document.getElementById("logoutBtn").onclick = async () => {
  await ext.storage.local.remove("token");
  await init();
};

document.getElementById("folderBtn").onclick = async () => {
  const input = document.getElementById("folderName");
  const created = await createFolder(input.value, parentFolderId || null);
  if (created) input.value = "";
};
document.getElementById("folderName").addEventListener("keydown", (e) => {
  if (e.key === "Enter") document.getElementById("folderBtn").click();
});

document.getElementById("addFolderBtn").onclick = async () => {
  const input = document.getElementById("addFolderName");
  const parent = document.getElementById("folder").value || null;
  const created = await createFolder(input.value, parent);
  if (created) input.value = "";
};
document.getElementById("addFolderName").addEventListener("keydown", (e) => {
  if (e.key === "Enter") document.getElementById("addFolderBtn").click();
});

document.getElementById("folder").addEventListener("change", () => {
  parentFolderId = document.getElementById("folder").value || "";
  updateFolderHint();
});

init();
