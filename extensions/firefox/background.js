importScripts("api.js");

const ext = typeof browser !== "undefined" ? browser : chrome;

async function lastFolder() {
  const { lastFolderId } = await ext.storage.local.get("lastFolderId");
  return lastFolderId || null;
}

async function addTab(tab, confirmDup) {
  const parentId = (await lastFolder()) || null;
  try {
    await BM.api("/api/nodes", {
      method: "POST",
      body: JSON.stringify({
        type: "bookmark",
        title: tab.title || tab.url,
        url: tab.url,
        parentId,
        confirmDuplicate: !!confirmDup,
      }),
    });
    if (ext.notifications && ext.notifications.create) {
      ext.notifications.create({
        type: "basic",
        iconUrl: "icons/icon-128.png",
        title: "Закладки",
        message: "Сохранено: " + (tab.title || tab.url),
      });
    }
    if (ext.action && ext.action.setBadgeText) {
      await ext.action.setBadgeText({ text: "OK" });
      setTimeout(() => ext.action.setBadgeText({ text: "" }), 1500);
    }
  } catch (e) {
    if (e.status === 409 && !confirmDup) {
      await addTab(tab, true);
      return;
    }
    if (ext.notifications && ext.notifications.create) {
      ext.notifications.create({
        type: "basic",
        iconUrl: "icons/icon-128.png",
        title: "Закладки",
        message: e.message,
      });
    }
    throw e;
  }
}

ext.runtime.onInstalled.addListener(async () => {
  await BM.getDeviceId();
  ext.contextMenus.removeAll(() => {
    ext.contextMenus.create({
      id: "add-bookmark",
      title: "В закладки",
      contexts: ["page", "link"],
    });
  });
});

ext.contextMenus.onClicked.addListener(async (info, tab) => {
  const t = { title: tab && tab.title, url: info.linkUrl || (tab && tab.url) };
  try {
    await addTab(t);
  } catch (_) {
    if (ext.action && ext.action.openPopup) ext.action.openPopup();
  }
});

if (ext.commands && ext.commands.onCommand) {
  ext.commands.onCommand.addListener(async (command) => {
    if (command !== "add-tab") return;
    const { token, lastFolderId } = await ext.storage.local.get(["token", "lastFolderId"]);
    const tabs = await ext.tabs.query({ active: true, currentWindow: true });
    if (token && lastFolderId && tabs[0]) {
      try {
        await addTab(tabs[0]);
        return;
      } catch (_) {}
    }
    if (ext.action && ext.action.openPopup) await ext.action.openPopup();
  });
}

if (ext.omnibox) {
  ext.omnibox.setDefaultSuggestion({ description: "Поиск по закладкам" });
  ext.omnibox.onInputChanged.addListener(async (text, suggest) => {
    try {
      const list = await BM.api("/api/search?q=" + encodeURIComponent(text));
      suggest(
        (list || []).slice(0, 6).map((n) => ({
          content: n.url,
          description: n.title + (n.path ? " — " + n.path : ""),
        }))
      );
    } catch (_) {
      suggest([]);
    }
  });
  ext.omnibox.onInputEntered.addListener((url) => {
    if (url.indexOf("http") === 0) ext.tabs.create({ url: url });
  });
}
