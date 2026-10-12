// 画面横断で共有する可変状態。
export const state = {
  me: null,
  projects: [],
  terminalSession: null,
  // hello が早く来ても再接続トーストが一瞬で消えないようにする。
  termToastShownAt: 0,
  termToastHideTimer: null,
};

export const app = document.querySelector("#app");
export const defaultDocumentTitle = "Cursor";
export const TERM_TOAST_MIN_VISIBLE_MS = 1000;
