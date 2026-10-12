import { state } from "/static/js/state.js";

let onUnauthorized = null;

// 401 時にログイン画面へ戻す処理を登録する。
export function setUnauthorizedHandler(handler) {
  onUnauthorized = handler;
}

export function handleUnauthorized() {
  state.me = null;
  if (onUnauthorized) {
    onUnauthorized();
  }
}

export async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
  });
  const text = await response.text();
  const body = text ? JSON.parse(text) : null;
  if (!response.ok) {
    const error = new Error(body?.error || "リクエストに失敗しました");
    error.status = response.status;
    if (response.status === 401) {
      handleUnauthorized();
    }
    throw error;
  }
  return body;
}
