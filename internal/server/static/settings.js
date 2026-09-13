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

const $ = (id) => document.getElementById(id);

function shortDevice(id) {
  return id.length > 10 ? id.slice(0, 8) + "…" : id;
}

async function load() {
  const s = await api("/api/settings");
  $("reqExt").checked = s.requireExtensionAuth;
  $("extWarn").classList.toggle("hidden", s.requireExtensionAuth);
  $("listen").textContent = "http://" + s.listen;
  $("backupDir").value = s.backupDir || "";
  $("backupIv").value = s.backupIntervalHours || "24";
  $("backupRet").value = s.backupRetention || "14";
  $("lastBackup").textContent = s.lastBackupAt || "нет";
  const tb = $("sessions");
  tb.innerHTML = (s.extensionSessions || [])
    .map(
      (x) => `<tr>
        <td>${x.browser || ""}</td>
        <td>${x.ip || ""}</td>
        <td title="${x.deviceId}">${shortDevice(x.deviceId)}</td>
        <td>${x.lastSeenAt || ""}</td>
        <td><button data-id="${x.id}" class="ghost">Отозвать</button></td>
      </tr>`
    )
    .join("") || `<tr><td colspan="5" class="muted">Нет сессий</td></tr>`;
  tb.querySelectorAll("button").forEach((b) => {
    b.onclick = async () => {
      await api(`/api/ext/sessions/${b.dataset.id}`, { method: "DELETE" });
      await load();
    };
  });
}

$("reqExt").onchange = async () => {
  await api("/api/settings", {
    method: "PATCH",
    body: JSON.stringify({ requireExtensionAuth: $("reqExt").checked }),
  });
  $("extWarn").classList.toggle("hidden", $("reqExt").checked);
};

$("passForm").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await api("/api/password", {
      method: "POST",
      body: JSON.stringify({ current: $("curPass").value, next: $("newPass").value }),
    });
    location.href = "/login";
  } catch (err) {
    alert(err.message);
  }
};

$("backupForm").onsubmit = async (e) => {
  e.preventDefault();
  await api("/api/settings", {
    method: "PATCH",
    body: JSON.stringify({
      backupDir: $("backupDir").value,
      backupIntervalHours: $("backupIv").value,
      backupRetention: $("backupRet").value,
    }),
  });
  await load();
};

$("backupNow").onclick = async () => {
  try {
    const r = await api("/api/backup", { method: "POST", body: "{}" });
    alert("Записано:\n" + r.json + "\n" + r.sqlite);
    await load();
  } catch (err) {
    alert(err.message);
  }
};

$("importForm").onsubmit = async (e) => {
  e.preventDefault();
  const f = $("importFile").files[0];
  if (!f) return;
  const doc = JSON.parse(await f.text());
  await api("/api/import", {
    method: "POST",
    body: JSON.stringify({ replace: $("importReplace").checked, nodes: doc.nodes || doc }),
  });
  alert("Импортировано");
};

$("checkAll").onclick = async () => {
  await api("/api/links/check-all", { method: "POST", body: "{}" });
  const tick = async () => {
    const st = await api("/api/links/status");
    $("checkStatus").textContent = st.running ? `Проверено ${st.checked} / ${st.total}` : `Готово: ${st.checked} / ${st.total}`;
    if (st.running) setTimeout(tick, 800);
  };
  tick();
};

load();
