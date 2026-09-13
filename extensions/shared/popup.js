const ext = typeof browser !== "undefined" ? browser : chrome;

document.getElementById("loginBtn").onclick = async () => {
  const server = document.getElementById("server").value.trim();
  await ext.storage.local.set({ serverUrl: server });
  await BM.ensureHostPermission(server);
  try {
    const data = await BM.api("/api/ext/login", {
      method: "POST",
      body: JSON.stringify({
        password: document.getElementById("password").value,
        deviceId: await BM.getDeviceId(),
      }),
    });
    await ext.storage.local.set({ token: data.token });
    show("Вход выполнен");
    await init();
  } catch (e) {
    show(e.message, true);
  }
};

function show(msg, isErr) {
  const el = document.getElementById("status");
  el.textContent = msg || "";
  el.className = isErr ? "err" : "ok";
}

async function fillFolders(selected) {
  const sel = document.getElementById("folder");
  try {
    const tree = await BM.api("/api/tree");
    const folders = BM.flattenFolders(tree);
    sel.innerHTML = folders.map((f) => `<option value="${f.id}">${escapeHtml(f.title)}</option>`).join("");
    if (selected) sel.value = selected;
  } catch (e) {
    sel.innerHTML = `<option value="">Корень</option>`;
    if (e.code === 401) show(e.message, true);
  }
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function fillTags() {
  try {
    const tags = await BM.api("/api/tags");
    document.getElementById("taglist").innerHTML = tags.map((t) => `<option value="${escapeHtml(t.name)}">`).join("");
  } catch (_) {}
}

async function currentTab() {
  const tabs = await ext.tabs.query({ active: true, currentWindow: true });
  return tabs[0];
}

async function init() {
  const { token, lastFolderId, serverUrl } = await ext.storage.local.get(["token", "lastFolderId", "serverUrl"]);
  document.getElementById("server").value = serverUrl || BM.defaultServer();
  document.getElementById("auth").hidden = !!token;
  document.getElementById("form").hidden = !token;
  const tab = await currentTab();
  if (tab) {
    document.getElementById("title").value = tab.title || "";
    document.getElementById("url").value = tab.url || "";
  }
  if (token) {
    await fillFolders(lastFolderId);
    await fillTags();
  }
  document.getElementById("device").textContent = "device: " + (await BM.getDeviceId()).slice(0, 8);
}

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
  } catch (e) {
    show(e.message, true);
  }
};

document.getElementById("logoutBtn").onclick = async () => {
  await ext.storage.local.remove("token");
  await init();
};

init();
