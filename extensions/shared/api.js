const ext = typeof browser !== "undefined" ? browser : chrome;

const BM = {
  defaultServer() {
    return "http://127.0.0.1:8080";
  },
  normalizeServer(raw) {
    let s = String(raw || "").trim();
    if (!s) return "";
    if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(s)) {
      const host = s.split("/")[0];
      const local = /^(localhost|127\.0\.0\.1|\[::1\])(:\d+)?$/i.test(host);
      s = (local ? "http://" : "https://") + s;
    }
    let u;
    try {
      u = new URL(s);
    } catch {
      return "";
    }
    if (!/^https?:$/i.test(u.protocol) || !u.hostname) return "";
    return u.origin;
  },
  isCompleteServer(raw) {
    const origin = BM.normalizeServer(raw);
    if (!origin) return false;
    let u;
    try {
      u = new URL(origin);
    } catch {
      return false;
    }
    const host = u.hostname;
    if (host === "localhost" || host === "127.0.0.1" || host === "::1") return !!u.port;
    if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host)) return true;
    const labels = host.split(".");
    if (labels.length < 2 || labels.some((p) => !p)) return false;
    const tld = labels[labels.length - 1];
    return /^[a-zA-Z]{2,}$/.test(tld) || /^xn--[a-z0-9-]+$/i.test(tld);
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
    return BM.isCompleteServer(serverUrl) ? BM.normalizeServer(serverUrl) : BM.defaultServer();
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
    const stack = [];
    const list = nodes || [];
    for (let i = list.length - 1; i >= 0; i--) stack.push({ n: list[i], pfx: "" });
    const seen = new Set();
    while (stack.length) {
      const { n, pfx } = stack.pop();
      if (!n || (n.id && seen.has(n.id))) continue;
      if (n.id) seen.add(n.id);
      if (n.type !== "folder") continue;
      out.push({ id: n.id, title: pfx + n.title });
      const ch = Array.isArray(n.children) ? n.children : [];
      for (let i = ch.length - 1; i >= 0; i--) stack.push({ n: ch[i], pfx: pfx + n.title + " / " });
    }
    return out;
  },
  browserTreeToPayload(nodes) {
    const roots = (nodes && nodes[0] && nodes[0].children) || nodes || [];
    const result = [];
    const stack = [{ items: roots, out: result }];
    const seen = new Set();
    while (stack.length) {
      const { items, out } = stack.pop();
      for (const n of items || []) {
        if (n && n.id != null) {
          const key = String(n.id);
          if (seen.has(key)) continue;
          seen.add(key);
        }
        if (n.url && (n.url.startsWith("javascript:") || n.url.startsWith("data:"))) continue;
        const item = { title: n.title || "", url: n.url || "", children: [] };
        out.push(item);
        if (n.children && n.children.length) stack.push({ items: n.children, out: item.children });
      }
    }
    return result;
  },
};
