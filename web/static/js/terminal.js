import { state, app, TERM_TOAST_MIN_VISIBLE_MS } from "/static/js/state.js";
import {
  FLOAT_KEY_SEQUENCES,
  KEYBOARD_KEY_SEQUENCES,
} from "/static/js/keys.js";
import { setDocumentTitle } from "/static/js/title.js";
import { isCurrentTerminalRoute } from "/static/js/router.js";
import { ensureProject, loadSessionTitle } from "/static/js/screens.js";
import {
  loadAttachID,
  saveAttachID,
  clearAttachID,
} from "/static/js/attach.js";

export function bindTerminalChat(chatId) {
  if (
    !state.terminalSession ||
    !chatId ||
    state.terminalSession.chatId === chatId
  ) {
    return;
  }
  const projectId = state.terminalSession.projectId;
  const prevChatId = state.terminalSession.chatId;
  const attachID = state.terminalSession.attachID;
  if (attachID) {
    clearAttachID(projectId, prevChatId);
    saveAttachID(projectId, chatId, attachID);
  }
  state.terminalSession.chatId = chatId;
  const next = `#/projects/${encodeURIComponent(projectId)}/terminal/${encodeURIComponent(chatId)}`;
  if (location.hash !== next) {
    // hashchange を起こさず、表示中の端末を作り直さない。
    history.replaceState(null, "", next);
  }
}

export async function renderTerminal(projectId, chatId) {
  const project = await ensureProject(projectId);
  if (!project) {
    return;
  }
  // 非同期のあいだに別画面へ移った場合は、古い結果で描画しない。
  if (!isCurrentTerminalRoute(projectId, chatId)) {
    return;
  }
  const sessionTitle = await loadSessionTitle(projectId, chatId);
  if (!state.me || !isCurrentTerminalRoute(projectId, chatId)) {
    return;
  }
  destroyTerminal();
  const route = chatId
    ? `#/projects/${encodeURIComponent(projectId)}/terminal/${encodeURIComponent(chatId)}`
    : `#/projects/${encodeURIComponent(projectId)}/terminal`;
  app.innerHTML = `
    <main class="terminal-screen">
      <div class="bar">
        <button class="secondary" id="back" type="button">戻る</button>
        <h1></h1>
        <a class="top-link" href="#/projects">トップ</a>
      </div>
      <div class="term-wrap" id="term-wrap">
        <div id="term"></div>
        <div
          class="term-scroll-proxy"
          id="term-scroll-proxy"
          aria-hidden="true"
        >
          <div class="term-scroll-spacer" id="term-scroll-spacer"></div>
        </div>
      </div>
      <div class="term-toast is-hidden" id="term-toast" role="status" aria-live="polite"></div>
      <form class="composer" id="composer">
        <textarea id="draft" placeholder="メッセージ"></textarea>
        <div class="composer-actions">
          <button
            class="secondary speech-toggle"
            id="speech-toggle"
            type="button"
            aria-label="音声入力"
            aria-pressed="false"
          >
            音声
          </button>
          <button class="primary" type="submit">送信</button>
        </div>
      </form>
      <button class="scroll-bottom is-hidden" id="scroll-bottom" type="button">末尾にスクロール</button>
      <div class="floats" id="floats">
        <button class="hide" id="hide-floats" type="button" aria-label="操作キーを隠す">×</button>
        <button class="esc" type="button" data-key="esc">Esc</button>
        <button class="up" type="button" data-key="up">↑</button>
        <button class="select" id="term-select-open" type="button">選択</button>
        <button class="left" type="button" data-key="left">←</button>
        <button class="down" type="button" data-key="down">↓</button>
        <button class="right" type="button" data-key="right">→</button>
        <button class="enter" type="button" data-key="enter">Enter</button>
      </div>
    </main>
  `;
  // fixed のずれを避けるため、トップレイヤーの dialog で全画面表示する。
  const selectOverlay = document.createElement("dialog");
  selectOverlay.className = "term-select-overlay";
  selectOverlay.id = "term-select-overlay";
  selectOverlay.innerHTML = `
    <div class="term-select-bar">
      <span>範囲を選んで OS のコピーを使えます</span>
      <button type="button" id="term-select-close">完了</button>
    </div>
    <textarea
      class="term-select-text"
      id="term-select-text"
      readonly
      inputmode="none"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      spellcheck="false"
    ></textarea>
  `;
  document.body.appendChild(selectOverlay);
  document.querySelector("h1").textContent = project.name;
  setDocumentTitle(sessionTitle || "無題", project.name);
  document.querySelector("#back").addEventListener("click", () => {
    location.hash = `#/projects/${encodeURIComponent(projectId)}`;
  });
  const termWrap = document.querySelector("#term-wrap");
  const termElement = document.querySelector("#term");
  const scrollBottom = document.querySelector("#scroll-bottom");
  const floats = document.querySelector("#floats");
  const term = new Terminal({
    // 入力は下書き欄経由だけにし、端末タップで iOS キーボードが開くのを避ける。
    disableStdin: true,
    cursorBlink: true,
    fontSize: 14,
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
    // 長い対話でも上方向へ遡れるよう、既定より広く保持する。
    scrollback: 10000,
    // proxy からの連続 scrollToLine とアニメが競合しないようにする。
    smoothScrollDuration: 0,
    theme: { background: "#0e1116" },
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(termElement);
  silenceTerminalHelperTextarea(term);
  const syncScrollBottom = () => {
    const buffer = term.buffer.active;
    // 末尾より上へ遡っているときだけ、末尾へ戻る導線を出す。
    // proxy 同期の丸め誤差で 1〜2 行ずれることがあるため許容する。
    scrollBottom.classList.toggle("is-hidden", isTerminalNearBottom(buffer));
  };
  const scrollDisposable = term.onScroll(syncScrollBottom);
  scrollBottom.addEventListener("click", () => {
    // 慣性中でも proxy の scrollTop を直接動かして止め、末尾へ揃える。
    state.terminalSession?.scrollProxyToBottom?.();
  });
  state.terminalSession = {
    term,
    fit,
    socket: null,
    route,
    projectId,
    projectName: project.name,
    chatId,
    sessionTitle: sessionTitle || "",
    attachID: loadAttachID(projectId, chatId),
    // 新しい xterm ではサーバ側 scrollback を再生する。同一画面の再接続では差分だけにする。
    replayScrollback: true,
    seenHello: false,
    activity: "unknown",
    hidden: false,
    leaving: false,
    agentExited: false,
    selectOverlayOpen: false,
    showSelectOverlay: null,
    hideSelectOverlay: null,
    reconnectTimer: null,
    // 本文送信後〜Enter+行クリア完了前。切断すると PTY に未確定行が残り次送信へ付く。
    pendingSubmit: false,
    pendingText: "",
    submitTimer: null,
    // Enter 後も CLI 入力行に本文が残ることがある。残っているあいだ true。
    ptyLineDirty: false,
    // 行クリアの Ctrl+U が誘発する waiting を短時間無視する。
    suppressWaitingUntil: 0,
    unbindScrollProxy: null,
    syncScrollProxy: null,
    scrollProxyToBottom: null,
    unbindSelectOverlay: null,
    unbindFloatReveal: null,
    abortSpeechInput: null,
    resizeObserver: null,
    disposeScroll: () => scrollDisposable.dispose(),
    onVisibility: null,
    onOnline: null,
  };
  state.terminalSession.unbindScrollProxy = bindTerminalScrollProxy(
    term,
    termWrap,
  );
  state.terminalSession.unbindSelectOverlay = bindTerminalSelectOverlay(
    term,
    termWrap,
  );
  fitTerminal();
  syncScrollBottom();
  const resizeObserver = new ResizeObserver(() => fitTerminal());
  resizeObserver.observe(termElement);
  state.terminalSession.resizeObserver = resizeObserver;
  bindDraftComposer(document.querySelector("#composer"));
  floats.addEventListener("click", (event) => {
    const button = event.target.closest("button");
    if (!button) {
      return;
    }
    if (button.id === "term-select-open") {
      state.terminalSession.showSelectOverlay?.();
      return;
    }
    const key = button.dataset.key;
    if (!key) {
      return;
    }
    sendInput(FLOAT_KEY_SEQUENCES[key]);
    if (key === "esc") {
      unlockSendAfterEsc();
    }
  });
  document.querySelector("#hide-floats").addEventListener("click", () => {
    setFloatsHidden(true);
  });
  document.addEventListener("keydown", onTerminalKeydown);
  state.terminalSession.onVisibility = () => {
    if (document.visibilityState === "visible") {
      reconnectTerminal();
    }
  };
  state.terminalSession.onOnline = () => reconnectTerminal();
  document.addEventListener(
    "visibilitychange",
    state.terminalSession.onVisibility,
  );
  window.addEventListener("online", state.terminalSession.onOnline);
  connectTerminal();
}

export function connectTerminal() {
  if (
    !state.terminalSession ||
    state.terminalSession.leaving ||
    state.terminalSession.agentExited
  ) {
    return;
  }
  if (
    state.terminalSession.socket &&
    (state.terminalSession.socket.readyState === WebSocket.OPEN ||
      state.terminalSession.socket.readyState === WebSocket.CONNECTING)
  ) {
    return;
  }
  // CLOSING/CLOSED のまま残っていると close が差し替え後に握りつぶされるので先に捨てる。
  const staleSocket = state.terminalSession.socket;
  if (staleSocket) {
    state.terminalSession.socket = null;
    try {
      staleSocket.close();
    } catch (_error) {
      // ignore
    }
  }
  const { projectId, term } = state.terminalSession;
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams({ project: projectId });
  if (state.terminalSession.chatId) {
    params.set("chat", state.terminalSession.chatId);
  }
  if (state.terminalSession.attachID) {
    params.set("attach", state.terminalSession.attachID);
  }
  if (state.terminalSession.replayScrollback) {
    params.set("replay", "1");
  }
  // フルリロードでは close ハンドラが走らないため、attach がある初回接続で再接続中と出す。
  if (
    state.terminalSession.replayScrollback &&
    state.terminalSession.attachID
  ) {
    showTermToast("再接続しています…");
  } else if (!state.terminalSession.replayScrollback) {
    // 同一画面の再接続。close を取りこぼしてもここで出す。
    showTermToast("接続が切れました。再接続しています…");
  }
  const socket = new WebSocket(
    `${protocol}//${location.host}/ws/terminal?${params.toString()}`,
  );
  socket.binaryType = "arraybuffer";
  state.terminalSession.socket = socket;
  state.terminalSession.seenHello = false;
  socket.addEventListener("open", () => {
    fitTerminal();
    // 再表示直後はセル高計測前で末尾同期が空振りし得るため、後でもう一度寄せる。
    state.terminalSession?.scrollProxyToBottom?.();
  });
  socket.addEventListener("message", (event) => {
    if (!state.terminalSession || state.terminalSession.socket !== socket) {
      return;
    }
    if (typeof event.data === "string") {
      const message = JSON.parse(event.data);
      if (message.type === "hello" && message.attach) {
        state.terminalSession.seenHello = true;
        state.terminalSession.attachID = message.attach;
        hideTermToast();
        applyAgentActivity(message.activity);
        applySessionTitle(message.title);
        bindTerminalChat(message.chat || "");
        // ページ再読込前や Enter 欠落時は PTY に未確定行が残る。
        // 毎送信の Ctrl+U は agent の確定を妨げるため、必要なときだけ消す。
        const clearStaleLine =
          state.terminalSession.replayScrollback ||
          state.terminalSession.pendingSubmit;
        const retryText = state.terminalSession.pendingSubmit
          ? state.terminalSession.pendingText
          : "";
        clearSubmitTimer();
        markSubmitCommitted();
        // hello 以降は同一画面の再接続になるので、差分バッファだけを要求する。
        state.terminalSession.replayScrollback = false;
        saveAttachID(projectId, state.terminalSession.chatId, message.attach);
        if (clearStaleLine) {
          clearPtyLine();
        }
        // 切断で Enter が落ちた本文は、行クリア後に送り直す。
        if (retryText) {
          window.setTimeout(() => {
            sendDraft(retryText);
          }, 0);
        }
        // replay バイナリの直前・直後どちらでも末尾へ寄せられるよう、ここでも同期する。
        fitTerminal();
        state.terminalSession.scrollProxyToBottom?.();
        window.setTimeout(() => {
          state.terminalSession?.scrollProxyToBottom?.();
        }, 0);
        return;
      }
      if (message.type === "activity") {
        applyAgentActivity(message.state);
        applySessionTitle(message.title);
        bindTerminalChat(message.chat || "");
        return;
      }
      if (message.type === "exit") {
        state.terminalSession.agentExited = true;
        clearAttachID(projectId, state.terminalSession.chatId);
        // 端末ログ末尾に混ざると会話と区別しづらいので、トーストで出す。
        showTermToast(`セッションが終了しました (code ${message.code})`);
      }
      return;
    }
    term.write(new Uint8Array(event.data));
  });
  socket.addEventListener("close", () => {
    if (!state.terminalSession || state.terminalSession.socket !== socket) {
      return;
    }
    state.terminalSession.socket = null;
    if (state.terminalSession.leaving || state.terminalSession.agentExited) {
      return;
    }
    // 期限切れの attach では upgrade 前に失敗するため、捨てて新規/chat 再接続に切り替える。
    if (!state.terminalSession.seenHello && state.terminalSession.attachID) {
      clearAttachID(projectId, state.terminalSession.chatId);
      state.terminalSession.attachID = "";
    }
    showTermToast("接続が切れました。再接続しています…");
    scheduleTerminalReconnect(1000);
  });
}

// 再接続・終了通知は端末ログと紛らわしいので、独立したトーストで知らせる。
export function showTermToast(message) {
  const toast = document.querySelector("#term-toast");
  if (!toast) {
    return;
  }
  if (state.termToastHideTimer) {
    window.clearTimeout(state.termToastHideTimer);
    state.termToastHideTimer = null;
  }
  toast.textContent = message;
  toast.classList.remove("is-hidden");
  state.termToastShownAt = Date.now();
}

export function hideTermToast() {
  const toast = document.querySelector("#term-toast");
  if (!toast || toast.classList.contains("is-hidden")) {
    return;
  }
  if (state.termToastHideTimer) {
    window.clearTimeout(state.termToastHideTimer);
    state.termToastHideTimer = null;
  }
  // 再接続が速いときも、最低表示時間だけ残して視認できるようにする。
  const remain =
    TERM_TOAST_MIN_VISIBLE_MS - (Date.now() - state.termToastShownAt);
  if (remain > 0) {
    state.termToastHideTimer = window.setTimeout(() => {
      state.termToastHideTimer = null;
      hideTermToastNow();
    }, remain);
    return;
  }
  hideTermToastNow();
}

export function hideTermToastNow() {
  const toast = document.querySelector("#term-toast");
  if (!toast) {
    return;
  }
  toast.textContent = "";
  toast.classList.add("is-hidden");
}

export function scheduleTerminalReconnect(delayMs) {
  if (
    !state.terminalSession ||
    state.terminalSession.leaving ||
    state.terminalSession.agentExited
  ) {
    return;
  }
  if (state.terminalSession.reconnectTimer) {
    window.clearTimeout(state.terminalSession.reconnectTimer);
  }
  state.terminalSession.reconnectTimer = window.setTimeout(() => {
    if (
      !state.terminalSession ||
      state.terminalSession.leaving ||
      state.terminalSession.agentExited
    ) {
      return;
    }
    connectTerminal();
  }, delayMs);
}

export function reconnectTerminal() {
  if (
    !state.terminalSession ||
    state.terminalSession.leaving ||
    state.terminalSession.agentExited
  ) {
    return;
  }
  const socket = state.terminalSession.socket;
  if (
    socket &&
    (socket.readyState === WebSocket.OPEN ||
      socket.readyState === WebSocket.CONNECTING)
  ) {
    return;
  }
  // close 前に差し替えるとトーストが出ないので、先に通知して古い参照を捨てる。
  if (socket) {
    state.terminalSession.socket = null;
    try {
      socket.close();
    } catch (_error) {
      // ignore
    }
  }
  showTermToast("接続が切れました。再接続しています…");
  scheduleTerminalReconnect(0);
}

export function onTerminalKeydown(event) {
  // 下書き入力中だけショートカットを避け、xterm 内部の textarea フォーカスは対象にする。
  if (!state.terminalSession || event.target.closest("#draft")) {
    return;
  }
  const sequence = KEYBOARD_KEY_SEQUENCES[event.key];
  if (!sequence) {
    return;
  }
  event.preventDefault();
  sendInput(sequence);
  if (event.key === "Escape") {
    unlockSendAfterEsc();
  }
}

// CLI の選択・確認待ちでは送信ボタンを止め、誤って Enter が届くのを防ぐ。
export function applyAgentActivity(state) {
  if (!state.terminalSession) {
    return;
  }
  const normalized =
    state === "idle" ||
    state === "busy" ||
    state === "waiting" ||
    state === "unknown"
      ? state
      : "unknown";
  // 自前の行クリア直後の waiting は、送信不能にしない。
  if (
    normalized === "waiting" &&
    Date.now() < state.terminalSession.suppressWaitingUntil
  ) {
    return;
  }
  state.terminalSession.activity = normalized;
  syncComposerSubmitState(normalized === "waiting");
}

export function syncComposerSubmitState(waiting) {
  const submit = document.querySelector("#composer button[type='submit']");
  if (!submit) {
    return;
  }
  submit.disabled = waiting;
  submit.title = waiting ? "選択または確認の入力待ちのため送信できません" : "";
}

export function isSendBlockedByWaiting() {
  if (!state.terminalSession || state.terminalSession.activity !== "waiting") {
    return false;
  }
  return Date.now() >= state.terminalSession.suppressWaitingUntil;
}

const PTY_CLEAR_WAITING_SUPPRESS_MS = 1500;
const DRAFT_ENTER_DELAY_BASE_MS = 80;
const DRAFT_ENTER_DELAY_MAX_MS = 500;
const DRAFT_CLEAR_DELAY_BASE_MS = 120;
const DRAFT_CLEAR_DELAY_MAX_MS = 600;
const DRAFT_CLEAR_RETRY_MIN_MS = 80;

// PTY 入力行を消し、Enter 後に残った前回本文も取り除く。
export function clearPtyLine(markClean = true) {
  if (!state.terminalSession) {
    return false;
  }
  state.terminalSession.suppressWaitingUntil =
    Date.now() + PTY_CLEAR_WAITING_SUPPRESS_MS;
  // 行クリア起因の waiting で送信ボタンが止まっていたら戻す。
  if (state.terminalSession.activity === "waiting") {
    state.terminalSession.activity = "idle";
    syncComposerSubmitState(false);
  }
  if (!sendInput("\u0015")) {
    return false;
  }
  if (markClean) {
    state.terminalSession.ptyLineDirty = false;
  }
  return true;
}

// status indicators のセッション名が付いたらタブタイトルへ反映する。
export function applySessionTitle(title) {
  if (!state.terminalSession) {
    return;
  }
  const trimmed = String(title || "").trim();
  if (!trimmed || trimmed === state.terminalSession.sessionTitle) {
    return;
  }
  state.terminalSession.sessionTitle = trimmed;
  setDocumentTitle(trimmed, state.terminalSession.projectName);
}

// Esc 後は自由テキスト入力へ移ることがあり、タイトルは waiting のまま残る。
// タイトルだけでは区別できないため、Esc 押下で送信を再開する。
export function unlockSendAfterEsc() {
  if (state.terminalSession?.activity !== "waiting") {
    return;
  }
  applyAgentActivity("idle");
}

// 操作パネルは表示中に選択を邪魔しないよう、隠したときだけタップ検出で戻す。
export function setFloatsHidden(hidden) {
  if (!state.terminalSession) {
    return;
  }
  state.terminalSession.hidden = hidden;
  document.querySelector("#floats")?.classList.toggle("is-hidden", hidden);
  if (hidden) {
    if (!state.terminalSession.unbindFloatReveal) {
      const termWrap = document.querySelector("#term-wrap");
      if (termWrap) {
        state.terminalSession.unbindFloatReveal =
          bindFloatRevealOnTap(termWrap);
      }
    }
    return;
  }
  if (state.terminalSession.unbindFloatReveal) {
    state.terminalSession.unbindFloatReveal();
    state.terminalSession.unbindFloatReveal = null;
  }
}

// ドラッグ開始の pointerdown では戻さず、移動の少ないタップだけを再表示に使う。
export function bindFloatRevealOnTap(element) {
  const tapSlopPx = 10;
  let pointerId = null;
  let startX = 0;
  let startY = 0;
  const onPointerDown = (event) => {
    if (
      pointerId !== null ||
      event.isPrimary === false ||
      state.terminalSession?.selectOverlayOpen ||
      event.target.closest("#term-select-overlay")
    ) {
      return;
    }
    pointerId = event.pointerId;
    startX = event.clientX;
    startY = event.clientY;
  };
  const onPointerUp = (event) => {
    if (pointerId !== event.pointerId) {
      return;
    }
    pointerId = null;
    if (
      !state.terminalSession?.hidden ||
      state.terminalSession.selectOverlayOpen
    ) {
      return;
    }
    const dx = event.clientX - startX;
    const dy = event.clientY - startY;
    if (dx * dx + dy * dy > tapSlopPx * tapSlopPx) {
      return;
    }
    setFloatsHidden(false);
  };
  const onPointerCancel = (event) => {
    if (pointerId === event.pointerId) {
      pointerId = null;
    }
  };
  element.addEventListener("pointerdown", onPointerDown);
  element.addEventListener("pointerup", onPointerUp);
  element.addEventListener("pointercancel", onPointerCancel);
  return () => {
    element.removeEventListener("pointerdown", onPointerDown);
    element.removeEventListener("pointerup", onPointerUp);
    element.removeEventListener("pointercancel", onPointerCancel);
  };
}

// xterm の隠し textarea がフォーカスされると iOS でキーボードが開くため抑止する。
export function silenceTerminalHelperTextarea(term) {
  const helper = term.textarea;
  if (!helper) {
    return;
  }
  helper.setAttribute("readonly", "readonly");
  helper.setAttribute("inputmode", "none");
  helper.setAttribute("aria-hidden", "true");
  helper.tabIndex = -1;
  helper.addEventListener("focus", () => {
    helper.blur();
  });
}

// iOS Safari では xterm 上の OS 選択が使えないため、
// 「選択」操作で readonly textarea にバッファを載せ、標準の選択・コピーに任せる。
export function bindTerminalSelectOverlay(term, termWrap) {
  const overlay = document.querySelector("#term-select-overlay");
  const textEl = overlay?.querySelector("#term-select-text");
  const closeButton = overlay?.querySelector("#term-select-close");
  const floats = document.querySelector("#floats");
  const scrollBottom = document.querySelector("#scroll-bottom");
  if (!overlay || !textEl || !closeButton) {
    return () => {};
  }
  const onViewportChange = () => layoutSelectOverlay(overlay);
  const hideOverlay = () => {
    if (!state.terminalSession?.selectOverlayOpen) {
      return;
    }
    state.terminalSession.selectOverlayOpen = false;
    window.visualViewport?.removeEventListener("resize", onViewportChange);
    window.visualViewport?.removeEventListener("scroll", onViewportChange);
    if (typeof overlay.close === "function" && overlay.open) {
      overlay.close();
    }
    overlay.removeAttribute("style");
    document.body.classList.remove("is-term-selecting");
    termWrap.classList.remove("is-selecting");
    textEl.value = "";
    textEl.blur();
    // 選択前にユーザーが隠していなければ操作パネルを戻す。
    if (floats && !state.terminalSession.hidden) {
      floats.classList.remove("is-hidden");
    }
    if (scrollBottom) {
      scrollBottom.classList.toggle(
        "is-hidden",
        isTerminalNearBottom(term.buffer.active),
      );
    }
    state.terminalSession.syncScrollProxy?.();
  };
  const showOverlay = () => {
    if (!state.terminalSession) {
      return;
    }
    const buffer = term.buffer.active;
    const lines = [];
    for (let y = 0; y < buffer.length; y += 1) {
      const line = buffer.getLine(y);
      lines.push(line ? line.translateToString(true) : "");
    }
    while (lines.length > 0 && lines[lines.length - 1].trim() === "") {
      lines.pop();
    }
    textEl.value = lines.join("\n");
    textEl.style.fontFamily = term.options.fontFamily;
    state.terminalSession.selectOverlayOpen = true;
    // 完了ボタンが操作パネルに隠れないよう、選択中はパネル類を隠す。
    floats?.classList.add("is-hidden");
    scrollBottom?.classList.add("is-hidden");
    document.body.classList.add("is-term-selecting");
    termWrap.classList.add("is-selecting");
    if (typeof overlay.showModal === "function") {
      if (!overlay.open) {
        overlay.showModal();
      }
    } else {
      overlay.setAttribute("open", "");
    }
    // UA 既定サイズをインラインで上書きし、画面全体へ広げる。
    layoutSelectOverlay(overlay);
    window.visualViewport?.addEventListener("resize", onViewportChange);
    window.visualViewport?.addEventListener("scroll", onViewportChange);
    // いま見ている付近から選べるよう、表示位置をビューポートへ合わせる。
    const rowHeight = textEl.scrollHeight / Math.max(lines.length, 1);
    textEl.scrollTop = Math.max(0, buffer.viewportY * rowHeight);
  };
  const onClose = () => hideOverlay();
  const onDialogCancel = (event) => {
    event.preventDefault();
    hideOverlay();
  };
  closeButton.addEventListener("click", onClose);
  overlay.addEventListener("cancel", onDialogCancel);
  state.terminalSession.showSelectOverlay = showOverlay;
  state.terminalSession.hideSelectOverlay = hideOverlay;
  return () => {
    closeButton.removeEventListener("click", onClose);
    overlay.removeEventListener("cancel", onDialogCancel);
    hideOverlay();
    overlay.remove();
  };
}

// Safari の dialog 既定レイアウトを避け、見た目の画面いっぱいに張り付ける。
export function layoutSelectOverlay(overlay) {
  if (!overlay) {
    return;
  }
  const viewport = window.visualViewport;
  const top = viewport ? viewport.offsetTop : 0;
  const left = viewport ? viewport.offsetLeft : 0;
  const width = viewport ? viewport.width : window.innerWidth;
  const height = viewport ? viewport.height : window.innerHeight;
  overlay.style.position = "fixed";
  overlay.style.top = `${top}px`;
  overlay.style.left = `${left}px`;
  overlay.style.right = "auto";
  overlay.style.bottom = "auto";
  overlay.style.width = `${width}px`;
  overlay.style.height = `${height}px`;
  overlay.style.maxWidth = "none";
  overlay.style.maxHeight = "none";
  overlay.style.margin = "0";
  overlay.style.border = "1px solid #2a3342";
  overlay.style.borderRadius = "0";
  overlay.style.outline = "none";
  overlay.style.boxShadow = "none";
  overlay.style.padding = "0";
  overlay.style.display = "flex";
  overlay.style.flexDirection = "column";
  overlay.style.boxSizing = "border-box";
  overlay.style.background = "#0e1116";
  overlay.style.color = "#e8edf5";
  const bar = overlay.querySelector(".term-select-bar");
  const textEl = overlay.querySelector(".term-select-text");
  if (!textEl) {
    return;
  }
  // textarea は flex 伸縮が不安定なため、バー以外の高さを直接割り当てる。
  const barHeight = bar ? bar.getBoundingClientRect().height : 0;
  textEl.style.flex = "1 1 auto";
  textEl.style.width = "100%";
  textEl.style.minHeight = "0";
  textEl.style.height = `${Math.max(0, height - barHeight)}px`;
}

// proxy と xterm の行換算で末尾が 1〜2 行ずれることがあるため、この範囲は末尾扱いする。
const TERMINAL_BOTTOM_LINE_SLOP = 2;

export function isTerminalNearBottom(buffer) {
  return buffer.baseY - buffer.viewportY <= TERMINAL_BOTTOM_LINE_SLOP;
}

// 透明なネイティブスクロール面の scrollTop と xterm の表示行を双方向同期する。
// 慣性・ホイールはブラウザに任せ、選択面表示中は操作しない。
export function bindTerminalScrollProxy(term, termWrap) {
  const proxy = termWrap.querySelector("#term-scroll-proxy");
  const spacer = termWrap.querySelector("#term-scroll-spacer");
  if (!proxy || !spacer) {
    return () => {};
  }

  let applyingFromProxy = false;
  let applyingFromTerminal = false;
  let applyReleaseTimer = 0;
  // 末尾追随中。スペーサー伸長で出る偽の scroll で解除しない。
  let stickToBottom = true;
  // touch/wheel/pointer など実操作のあいだだけ proxy→xterm を許可する。
  let userGesture = false;
  let userGestureTimer = 0;

  const measureCellHeight = () => {
    const screen = term.element?.querySelector(".xterm-screen");
    const height = screen?.clientHeight || 0;
    if (!(height > 0) || !(term.rows > 0)) {
      return 0;
    }
    return height / term.rows;
  };

  const maxProxyScrollTop = () =>
    Math.max(0, proxy.scrollHeight - proxy.clientHeight);

  const isProxyNearBottom = (cellHeight) => {
    return (
      maxProxyScrollTop() - proxy.scrollTop <=
      cellHeight * TERMINAL_BOTTOM_LINE_SLOP
    );
  };

  const markUserGesture = () => {
    userGesture = true;
    if (userGestureTimer) {
      window.clearTimeout(userGestureTimer);
    }
    // scrollend が来ない環境でも、操作後しばらくはジェスチャ扱いする。
    userGestureTimer = window.setTimeout(() => {
      userGestureTimer = 0;
      userGesture = false;
      if (stickToBottom) {
        syncFromTerminal();
      }
    }, 180);
  };

  const beginTerminalApply = () => {
    applyingFromTerminal = true;
    if (applyReleaseTimer) {
      window.clearTimeout(applyReleaseTimer);
    }
    // スペーサー変更に続く遅延 scroll をユーザー操作と誤認しない。
    applyReleaseTimer = window.setTimeout(() => {
      applyReleaseTimer = 0;
      applyingFromTerminal = false;
    }, 50);
  };

  const updateSpacerHeight = (cellHeight) => {
    const buffer = term.buffer.active;
    const nextHeight = `${Math.max(buffer.length, term.rows) * cellHeight}px`;
    if (spacer.style.height !== nextHeight) {
      spacer.style.height = nextHeight;
    }
  };

  const snapTerminalToBottom = () => {
    const buffer = term.buffer.active;
    if (buffer.viewportY >= buffer.baseY) {
      return;
    }
    applyingFromProxy = true;
    term.scrollToBottom();
    applyingFromProxy = false;
  };

  // forcePosition: fit など、履歴閲覧中でも viewport に proxy を合わせるとき。
  const syncFromTerminal = (options = {}) => {
    if (applyingFromProxy || state.terminalSession?.selectOverlayOpen) {
      return;
    }
    const cellHeight = measureCellHeight();
    if (!(cellHeight > 0)) {
      return;
    }
    const buffer = term.buffer.active;
    // xterm 側が末尾近傍なら追随を復帰（replay・丸め誤差対策）。
    if (isTerminalNearBottom(buffer)) {
      stickToBottom = true;
    }
    beginTerminalApply();
    updateSpacerHeight(cellHeight);
    if (stickToBottom) {
      snapTerminalToBottom();
      proxy.scrollTop = maxProxyScrollTop();
    } else if (options.forcePosition) {
      proxy.scrollTop = buffer.viewportY * cellHeight;
    }
  };

  // 慣性を打ち切り、proxy と xterm を末尾へ強制同期する。
  const scrollProxyToBottom = () => {
    if (state.terminalSession?.selectOverlayOpen) {
      return;
    }
    stickToBottom = true;
    userGesture = false;
    if (userGestureTimer) {
      window.clearTimeout(userGestureTimer);
      userGestureTimer = 0;
    }
    const cellHeight = measureCellHeight();
    beginTerminalApply();
    if (cellHeight > 0) {
      updateSpacerHeight(cellHeight);
    }
    term.scrollToBottom();
    proxy.scrollTop = maxProxyScrollTop();
  };

  const onProxyScroll = () => {
    if (applyingFromTerminal || state.terminalSession?.selectOverlayOpen) {
      return;
    }
    const cellHeight = measureCellHeight();
    if (!(cellHeight > 0)) {
      return;
    }
    // スペーサー伸長などで出た scroll は無視し、追随中なら末尾へ戻す。
    if (!userGesture) {
      if (stickToBottom) {
        beginTerminalApply();
        proxy.scrollTop = maxProxyScrollTop();
      }
      return;
    }
    stickToBottom = isProxyNearBottom(cellHeight);
    const buffer = term.buffer.active;
    const line = stickToBottom
      ? buffer.baseY
      : Math.max(
          0,
          Math.min(buffer.baseY, Math.round(proxy.scrollTop / cellHeight)),
        );
    if (line === buffer.viewportY) {
      return;
    }
    applyingFromProxy = true;
    term.scrollToLine(line);
    applyingFromProxy = false;
  };

  const onProxyScrollEnd = () => {
    userGesture = false;
    if (userGestureTimer) {
      window.clearTimeout(userGestureTimer);
      userGestureTimer = 0;
    }
    const cellHeight = measureCellHeight();
    if (cellHeight > 0 && isProxyNearBottom(cellHeight)) {
      stickToBottom = true;
    }
    syncFromTerminal();
  };

  const onTerminalScroll = () => {
    if (applyingFromProxy) {
      return;
    }
    syncFromTerminal();
  };

  const onWriteParsed = () => {
    syncFromTerminal();
  };

  proxy.addEventListener("scroll", onProxyScroll, { passive: true });
  proxy.addEventListener("scrollend", onProxyScrollEnd);
  proxy.addEventListener("touchstart", markUserGesture, { passive: true });
  proxy.addEventListener("wheel", markUserGesture, { passive: true });
  proxy.addEventListener("pointerdown", markUserGesture, { passive: true });
  const scrollDisposable = term.onScroll(onTerminalScroll);
  const writeDisposable = term.onWriteParsed(onWriteParsed);
  if (state.terminalSession) {
    state.terminalSession.syncScrollProxy = syncFromTerminal;
    state.terminalSession.scrollProxyToBottom = scrollProxyToBottom;
  }
  syncFromTerminal();

  return () => {
    if (userGestureTimer) {
      window.clearTimeout(userGestureTimer);
    }
    if (applyReleaseTimer) {
      window.clearTimeout(applyReleaseTimer);
    }
    proxy.removeEventListener("scroll", onProxyScroll);
    proxy.removeEventListener("scrollend", onProxyScrollEnd);
    proxy.removeEventListener("touchstart", markUserGesture);
    proxy.removeEventListener("wheel", markUserGesture);
    proxy.removeEventListener("pointerdown", markUserGesture);
    scrollDisposable.dispose();
    writeDisposable.dispose();
    if (state.terminalSession?.syncScrollProxy === syncFromTerminal) {
      state.terminalSession.syncScrollProxy = null;
    }
    if (state.terminalSession?.scrollProxyToBottom === scrollProxyToBottom) {
      state.terminalSession.scrollProxyToBottom = null;
    }
  };
}

export function fitTerminal() {
  if (!state.terminalSession) {
    return;
  }
  state.terminalSession.fit.fit();
  // 接続前や再接続待ちでは socket が null になり得る。
  if (state.terminalSession.socket?.readyState === WebSocket.OPEN) {
    state.terminalSession.socket.send(
      JSON.stringify({
        type: "resize",
        cols: state.terminalSession.term.cols,
        rows: state.terminalSession.term.rows,
      }),
    );
  }
  // fit 後はセル高が変わるので、追随中は末尾、閲覧中は viewport に合わせる。
  state.terminalSession.syncScrollProxy?.({ forcePosition: true });
}

export function bindDraftComposer(form) {
  const draft = form.querySelector("#draft");
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    // 選択・確認待ち中は Enter が意図せず確定されるのを防ぐ。
    if (isSendBlockedByWaiting()) {
      return;
    }
    const text = draft.value;
    if (text.length === 0) {
      return;
    }
    draft.value = "";
    // 未接続だと本文が捨てられるので、失敗時は下書きへ戻す。
    if (!sendDraft(text)) {
      draft.value = text;
    }
  });
  bindSpeechInput(form, draft);
}

// Web Speech API のコンストラクタを返す。未対応なら null。
export function getSpeechRecognitionConstructor() {
  return window.SpeechRecognition || window.webkitSpeechRecognition || null;
}

// 音声入力が使えない理由を日本語で返す。使えるときは空文字。
export function speechInputUnavailableReason() {
  if (!window.isSecureContext) {
    return "音声入力には HTTPS で開く必要があります";
  }
  if (!getSpeechRecognitionConstructor()) {
    return "このブラウザは音声入力に対応していません";
  }
  return "";
}

// 下書き欄へ音声認識結果を書き込むトグルをバインドする。
export function bindSpeechInput(form, draft) {
  const button = form.querySelector("#speech-toggle");
  if (!button) {
    return;
  }
  const unavailable = speechInputUnavailableReason();
  if (unavailable) {
    button.disabled = true;
    button.title = unavailable;
    return;
  }
  button.title = "音声入力";
  const SpeechRecognition = getSpeechRecognitionConstructor();
  let recognition = null;
  let listening = false;
  let prefix = "";

  const setListening = (next) => {
    listening = next;
    button.classList.toggle("is-listening", next);
    button.setAttribute("aria-pressed", next ? "true" : "false");
    button.textContent = next ? "停止" : "音声";
    button.title = next ? "音声入力を停止" : "音声入力";
  };

  const abortSpeechInput = () => {
    if (!recognition) {
      setListening(false);
      return;
    }
    const active = recognition;
    recognition = null;
    try {
      active.abort();
    } catch (_error) {
      // ignore
    }
    setListening(false);
  };

  if (state.terminalSession) {
    state.terminalSession.abortSpeechInput = abortSpeechInput;
  }

  button.addEventListener("click", () => {
    if (listening) {
      try {
        recognition?.stop();
      } catch (_error) {
        abortSpeechInput();
      }
      return;
    }
    recognition = new SpeechRecognition();
    recognition.lang = "ja-JP";
    // 長い下書きでも一息で止めない。停止はボタン操作に任せる。
    recognition.continuous = true;
    recognition.interimResults = true;
    prefix = draft.value;
    if (prefix.length > 0 && !/\s$/.test(prefix)) {
      prefix += " ";
    }
    recognition.onresult = (event) => {
      let finalPart = "";
      let interimPart = "";
      for (let i = 0; i < event.results.length; i += 1) {
        const transcript = event.results[i][0].transcript;
        if (event.results[i].isFinal) {
          finalPart += transcript;
        } else {
          interimPart += transcript;
        }
      }
      draft.value = prefix + finalPart + interimPart;
    };
    recognition.onerror = (event) => {
      if (event.error === "aborted" || event.error === "no-speech") {
        return;
      }
      const messages = {
        "not-allowed": "マイクの使用が許可されていません",
        "service-not-allowed": "音声認識サービスを利用できません",
        network: "音声認識のネットワークエラーです",
      };
      showTermToast(messages[event.error] || "音声入力でエラーが発生しました");
    };
    recognition.onend = () => {
      recognition = null;
      setListening(false);
    };
    try {
      recognition.start();
      setListening(true);
    } catch (_error) {
      recognition = null;
      setListening(false);
      showTermToast("音声入力を開始できませんでした");
    }
  });
}

export function markSubmitCommitted() {
  if (!state.terminalSession) {
    return;
  }
  state.terminalSession.pendingSubmit = false;
  state.terminalSession.pendingText = "";
}

// 長い本文ほど PTY / CLI の反映が遅れるので、待ちを伸ばす。
export function draftEnterDelayMs(text) {
  return Math.min(
    DRAFT_ENTER_DELAY_MAX_MS,
    DRAFT_ENTER_DELAY_BASE_MS + Math.ceil(String(text).length / 2),
  );
}

export function draftClearDelayMs(text) {
  return Math.min(
    DRAFT_CLEAR_DELAY_MAX_MS,
    DRAFT_CLEAR_DELAY_BASE_MS + Math.ceil(String(text).length / 2),
  );
}

export function draftClearRetryDelayMs(clearDelay) {
  return Math.max(DRAFT_CLEAR_RETRY_MIN_MS, Math.floor(clearDelay / 2));
}

// 送信の本体: 本文 → Enter → Ctrl+U で PTY に残った入力行を消す。
// 次送信の前だけ消すと、二通目の本文ごと消えて送れなくなることがある。
export function finishDraftSubmit() {
  if (!state.terminalSession) {
    return;
  }
  const pendingText = state.terminalSession.pendingText || "";
  if (!sendInput("\r")) {
    // Enter だけ落ちた場合は pending を残し、再接続時にクリアして再送する。
    return;
  }
  // Enter 処理後に残行を消す。短いと確定前に消える／残ることがある。
  const clearDelay = draftClearDelayMs(pendingText);
  state.terminalSession.submitTimer = window.setTimeout(() => {
    if (!state.terminalSession) {
      return;
    }
    clearPtyLine(false);
    // CLI が行を描き直すことがあるので、もう一度消す。
    state.terminalSession.submitTimer = window.setTimeout(() => {
      if (!state.terminalSession) {
        return;
      }
      clearPtyLine(true);
      markSubmitCommitted();
      state.terminalSession.submitTimer = null;
    }, draftClearRetryDelayMs(clearDelay));
  }, clearDelay);
}

// sendDraft は本文を一度に送り、その後に Enter でエージェントの入力を確定する。
// 同じ入力に含めた復帰は本文の改行になるため、Enter は別のキーとして届ける。
// 成功時 true。未接続や直前の未確定行掃除に失敗したときは false。
export function sendDraft(text) {
  if (!state.terminalSession) {
    return false;
  }
  // 直前の本文が Enter 待ちなら、先に確定して残行を消してから次を送る。
  if (state.terminalSession.pendingSubmit) {
    clearSubmitTimer();
    if (!sendInput("\r")) {
      return false;
    }
    if (!clearPtyLine()) {
      return false;
    }
    markSubmitCommitted();
  } else if (state.terminalSession.ptyLineDirty) {
    // Enter 後クリアが残ったままのときだけ、送信前に消してから本文を送る。
    if (!clearPtyLine()) {
      return false;
    }
  }
  if (!sendInput(text)) {
    return false;
  }
  state.terminalSession.ptyLineDirty = true;
  state.terminalSession.pendingSubmit = true;
  state.terminalSession.pendingText = text;
  state.terminalSession.submitTimer = window.setTimeout(() => {
    if (!state.terminalSession || !state.terminalSession.pendingSubmit) {
      return;
    }
    finishDraftSubmit();
  }, draftEnterDelayMs(text));
  return true;
}

export function clearSubmitTimer() {
  if (!state.terminalSession?.submitTimer) {
    return;
  }
  window.clearTimeout(state.terminalSession.submitTimer);
  state.terminalSession.submitTimer = null;
}

export function sendInput(data) {
  if (
    !state.terminalSession ||
    state.terminalSession.socket?.readyState !== WebSocket.OPEN
  ) {
    return false;
  }
  state.terminalSession.socket.send(JSON.stringify({ type: "input", data }));
  return true;
}

export function destroyTerminal() {
  document.removeEventListener("keydown", onTerminalKeydown);
  if (!state.terminalSession) {
    return;
  }
  state.terminalSession.leaving = true;
  clearSubmitTimer();
  if (state.termToastHideTimer) {
    window.clearTimeout(state.termToastHideTimer);
    state.termToastHideTimer = null;
  }
  if (state.terminalSession.reconnectTimer) {
    window.clearTimeout(state.terminalSession.reconnectTimer);
  }
  if (state.terminalSession.onVisibility) {
    document.removeEventListener(
      "visibilitychange",
      state.terminalSession.onVisibility,
    );
  }
  if (state.terminalSession.onOnline) {
    window.removeEventListener("online", state.terminalSession.onOnline);
  }
  if (state.terminalSession.unbindScrollProxy) {
    state.terminalSession.unbindScrollProxy();
  }
  if (state.terminalSession.unbindSelectOverlay) {
    state.terminalSession.unbindSelectOverlay();
  }
  if (state.terminalSession.unbindFloatReveal) {
    state.terminalSession.unbindFloatReveal();
  }
  if (state.terminalSession.abortSpeechInput) {
    state.terminalSession.abortSpeechInput();
  }
  state.terminalSession.disposeScroll();
  if (state.terminalSession.resizeObserver) {
    state.terminalSession.resizeObserver.disconnect();
    state.terminalSession.resizeObserver = null;
  }
  const socket = state.terminalSession.socket;
  if (socket && socket.readyState === WebSocket.OPEN) {
    // 意図的な離脱を伝え、アイドルなら即終了・処理中なら猶予付きで残す。
    try {
      socket.send(JSON.stringify({ type: "close" }));
    } catch (_error) {
      // ignore
    }
    socket.close();
  } else if (socket) {
    socket.close();
  }
  state.terminalSession.term.dispose();
  state.terminalSession = null;
}
