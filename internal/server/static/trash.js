async function api(path, opts = {}) {
  const res = await fetch(path, {
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

async function load() {
  const items = await api("/api/trash");
  const ul = document.getElementById("list");
  if (!items.length) {
    ul.innerHTML = "<li class='muted'>Пусто</li>";
    return;
  }
  ul.innerHTML = items
    .map(
      (n) => `<li data-id="${n.id}">
        <div class="grow"><strong>${n.title}</strong><div class="muted">${n.type} · ${n.path || ""}</div></div>
        <button data-act="restore">Восстановить</button>
        <button class="danger" data-act="purge">Удалить</button>
      </li>`
    )
    .join("");
  ul.querySelectorAll("button").forEach((b) => {
    b.onclick = async () => {
      const id = b.closest("li").dataset.id;
      if (b.dataset.act === "restore") await api(`/api/nodes/${id}/restore`, { method: "POST", body: "{}" });
      else if (confirm("Удалить навсегда?")) await api(`/api/nodes/${id}/purge`, { method: "DELETE" });
      await load();
    };
  });
}

document.getElementById("empty").onclick = async () => {
  if (!confirm("Очистить корзину?")) return;
  await api("/api/trash", { method: "DELETE" });
  await load();
};

load();
