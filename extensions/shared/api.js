const ext = typeof browser !== "undefined" ? browser : chrome;

const BM = {
  defaultServer() {
    return "http://127.0.0.1:8080";
  },
  async getDeviceId() {
    const { deviceId } = await ext.storage.local.get("deviceId");
    if (deviceId) return deviceId;
    const id = crypto.randomUUID();
    await ext.storage.local.set({ deviceId: id });
    return id;
  },
  async getServer() {
    const { serverUrl } = await ext.storage.local.get("serverUrl");
    return (serverUrl || BM.defaultServer()).replace(/\/$/, "");
  },
  async authHeaders() {
    const [{ token }, deviceId] = await Promise.all([ext.storage.local.get("token"), BM.getDeviceId()]);
    const h = { "Content-Type": "application/json", "X-Device-Id": deviceId };
    if (token) h.Authorization = "Bearer " + token;
    return h;
  },
  async api(path, opts = {}) {
    const server = await BM.getServer();
    const headers = Object.assign({}, await BM.authHeaders(), opts.headers || {});
    let res;
    try {
      res = await fetch(server + path, Object.assign({}, opts, { headers }));
    } catch (e) {
      throw new Error("Нет связи с сервером " + server);
    }
    const text = await res.text();
    let data = {};
    try {
      data = text ? JSON.parse(text) : {};
    } catch {
      data = { error: text };
    }
    if (res.status === 401) {
      await ext.storage.local.remove("token");
      const err = new Error("Сессия сброшена, войдите снова");
      err.code = 401;
      throw err;
    }
    if (!res.ok) {
      const err = new Error(data.error || res.statusText);
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  },
  async ensureHostPermission(server) {
    const origin = new URL(server).origin + "/*";
    if (ext.permissions && ext.permissions.request) {
      try {
        await ext.permissions.request({ origins: [origin] });
      } catch (_) {}
    }
  },
  flattenFolders(nodes) {
    const out = [{ id: "", title: "Корень" }];
    const walk = (list, pfx) => {
      for (const n of list || []) {
        if (n.type === "folder") {
          out.push({ id: n.id, title: pfx + n.title });
          walk(n.children, pfx + n.title + " / ");
        }
      }
    };
    walk(nodes, "");
    return out;
  },
  browserTreeToPayload(nodes) {
    const walk = (items) => {
      const out = [];
      for (const n of items || []) {
        if (n.url && (n.url.startsWith("javascript:") || n.url.startsWith("data:"))) continue;
        const item = { title: n.title || "", url: n.url || "", children: walk(n.children) };
        out.push(item);
      }
      return out;
    };
    const roots = (nodes && nodes[0] && nodes[0].children) || nodes || [];
    return walk(roots);
  },
};
