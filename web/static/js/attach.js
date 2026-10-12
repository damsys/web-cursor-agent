export function attachStorageKey(projectId, chatId) {
  return `wca-attach:${projectId}:${chatId || "new"}`;
}

export function loadAttachID(projectId, chatId) {
  try {
    return sessionStorage.getItem(attachStorageKey(projectId, chatId)) || "";
  } catch (_error) {
    return "";
  }
}

export function saveAttachID(projectId, chatId, attachID) {
  try {
    if (attachID) {
      sessionStorage.setItem(attachStorageKey(projectId, chatId), attachID);
    }
  } catch (_error) {
    // sessionStorage が使えない環境でも端末自体は動かす。
  }
}

export function clearAttachID(projectId, chatId) {
  try {
    sessionStorage.removeItem(attachStorageKey(projectId, chatId));
  } catch (_error) {
    // ignore
  }
}
