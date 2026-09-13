const ext = typeof browser !== "undefined" ? browser : chrome;

(async () => {
  const { serverUrl } = await ext.storage.local.get("serverUrl");
  document.getElementById("server").value = serverUrl || BM.defaultServer();
})();

document.getElementById("save").onclick = async () => {
  const server = document.getElementById("server").value.trim() || BM.defaultServer();
  await ext.storage.local.set({ serverUrl: server });
  await BM.ensureHostPermission(server);
  document.getElementById("status").textContent = "Сохранено";
};
