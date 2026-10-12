import { defaultDocumentTitle } from "/static/js/state.js";

// ブラウザタブに現在の画面文脈が分かるよう、document.title を組み立てる。
export function setDocumentTitle(...parts) {
  const title = parts
    .map((part) => String(part || "").trim())
    .filter(Boolean)
    .join(" - ");
  document.title = title || defaultDocumentTitle;
}
