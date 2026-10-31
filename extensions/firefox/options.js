(async () => {
  const { serverUrl } = await ext.storage.local.get("serverUrl");
  document.getElementById("server").value = BM.isCompleteServer(serverUrl)
    ? BM.normalizeServer(serverUrl)
    : BM.defaultServer();
})();

document.getElementById("save").onclick = async () => {
  const raw = document.getElementById("server").value;
  if (raw.trim() && !BM.isCompleteServer(raw)) {
    document.getElementById("status").textContent = "Укажите полный адрес, например example.com или localhost:8080";
    return;
  }
  const server = BM.normalizeServer(raw) || BM.defaultServer();
  document.getElementById("server").value = server;
  await ext.storage.local.set({ serverUrl: server });
  await BM.ensureHostPermission(server);
  document.getElementById("status").textContent = "Сохранено";
};
