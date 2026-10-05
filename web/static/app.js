const app = document.querySelector("#app");

let me = null;
let projects = [];
let terminalSession = null;

document.addEventListener("DOMContentLoaded", () => {
  syncViewport();
  window.addEventListener("hashchange", onHashChange);
  boot();
});

function syncViewport() {
  const viewport = window.visualViewport;
  if (!viewport) {
    return;
  }
  const apply = () => {
    document.body.style.height = `${viewport.height}px`;
    if (terminalSession) {
      fitTerminal();
    }
  };
  viewport.addEventListener("resize", apply);
  apply();
}

async function boot() {
  try {
    me = await api("/api/me");
  } catch (error) {
    if (error.status !== 401) {
      renderLogin(error.message);
      return;
    }
    me = null;
  }
  if (!me) {
    renderLogin("");
    return;
  }
  if (!location.hash) {
    location.hash = "#/projects";
    return;
  }
  renderRoute();
}

function onHashChange() {
  if (terminalSession && parseRoute().name !== "terminal") {
    destroyTerminal();
  }
  if (!me) {
    renderLogin("");
    return;
  }
  renderRoute();
}

function parseRoute() {
  const parts = location.hash.replace(/^#/, "").split("/").filter(Boolean);
  if (parts[0] !== "projects") {
    return { name: "projects" };
  }
  if (parts.length === 1) {
    return { name: "projects" };
  }
  const projectId = decodeURIComponent(parts[1]);
  if (parts[2] === "terminal") {
    return {
      name: "terminal",
      projectId,
      chatId: parts[3] ? decodeURIComponent(parts[3]) : "",
    };
  }
  return { name: "sessions", projectId };
}

function renderRoute() {
  const route = parseRoute();
  if (route.name === "projects") {
    renderProjects();
    return;
  }
  if (route.name === "sessions") {
    renderSessions(route.projectId);
    return;
  }
  renderTerminal(route.projectId, route.chatId);
}

function renderLogin(message) {
  app.innerHTML = `
    <main class="screen narrow">
      <h1>ログイン</h1>
      <form id="login-form" class="stack">
        <label>
          <span>ユーザー名</span>
          <input name="username" autocomplete="username" required />
        </label>
        <label>
          <span>パスワード</span>
          <input name="password" type="password" autocomplete="current-password" required />
        </label>
        <p class="error" id="login-error"></p>
        <button class="primary" type="submit">ログイン</button>
      </form>
    </main>
  `;
  const error = document.querySelector("#login-error");
  error.textContent = message || "";
  document
    .querySelector("#login-form")
    .addEventListener("submit", async (event) => {
      event.preventDefault();
      const data = new FormData(event.currentTarget);
      error.textContent = "";
      try {
        me = await api("/api/login", {
          method: "POST",
          body: JSON.stringify({
            username: data.get("username"),
            password: data.get("password"),
          }),
        });
        location.hash = "#/projects";
      } catch (caught) {
        error.textContent = caught.message;
      }
    });
}

async function renderProjects() {
  app.innerHTML = `
    <main class="screen narrow">
      <div class="bar" style="padding:0 0 12px; border:0">
        <h1>プロジェクト</h1>
        <button class="secondary" id="logout" type="button">ログアウト</button>
      </div>
      <div id="project-list" class="stack"></div>
      <p class="meta build-info" id="build-info">ビルド: …</p>
    </main>
  `;
  document.querySelector("#logout").addEventListener("click", logout);
  const list = document.querySelector("#project-list");
  renderBuildInfo();
  try {
    const body = await api("/api/projects");
    projects = body.projects;
    if (projects.length === 0) {
      list.innerHTML = `<p class="meta">プロジェクトがありません</p>`;
      return;
    }
    for (const project of projects) {
      const button = document.createElement("button");
      button.className = "card";
      button.type = "button";
      button.innerHTML = `<strong></strong><span></span>`;
      button.querySelector("strong").textContent = project.name;
      button.querySelector("span").textContent = project.path;
      button.addEventListener("click", () => {
        location.hash = `#/projects/${encodeURIComponent(project.id)}`;
      });
      list.append(button);
    }
  } catch (error) {
    if (error.status === 401) {
      me = null;
      renderLogin("");
      return;
    }
    list.innerHTML = `<p class="error"></p>`;
    list.querySelector(".error").textContent = error.message;
  }
}

// 配信中の版を判別できるよう、ビルド時に生成した build-info.js の値を表示する。
function renderBuildInfo() {
  const el = document.querySelector("#build-info");
  if (!el) {
    return;
  }
  const info = window.__WCA_BUILD__;
  if (!info || typeof info !== "object") {
    el.textContent = "ビルド: unknown";
    return;
  }
  const dirty = info.dirty ? " +未コミット" : "";
  el.textContent = `ビルド: ${info.commit || "unknown"}${dirty}`;
}

async function renderSessions(projectId) {
  const project = await ensureProject(projectId);
  if (!project) {
    return;
  }
  app.innerHTML = `
    <main class="screen narrow">
      <div class="bar" style="padding:0 0 12px; border:0">
        <button class="secondary" id="back" type="button">戻る</button>
        <h1></h1>
        <a class="top-link" href="#/projects">トップ</a>
      </div>
      <div class="stack">
        <button class="primary" id="new-session" type="button">新しいセッション</button>
        <div id="session-list" class="stack"></div>
      </div>
    </main>
  `;
  document.querySelector("h1").textContent = project.name;
  document.querySelector("#back").addEventListener("click", () => {
    location.hash = "#/projects";
  });
  document.querySelector("#new-session").addEventListener("click", () => {
    location.hash = `#/projects/${encodeURIComponent(projectId)}/terminal`;
  });
  const list = document.querySelector("#session-list");
  try {
    const body = await api(
      `/api/projects/${encodeURIComponent(projectId)}/sessions`,
    );
    if (body.sessions.length === 0) {
      list.innerHTML = `<p class="meta">セッションがありません</p>`;
      return;
    }
    for (const session of body.sessions) {
      const button = document.createElement("button");
      button.className = "card";
      button.type = "button";
      button.innerHTML = `<strong></strong><span></span>`;
      button.querySelector("strong").textContent = session.title || "無題";
      button.querySelector("span").textContent = new Date(
        session.updated_at_ms,
      ).toLocaleString("ja-JP");
      button.addEventListener("click", () => {
        location.hash = `#/projects/${encodeURIComponent(projectId)}/terminal/${encodeURIComponent(session.id)}`;
      });
      list.append(button);
    }
  } catch (error) {
    if (error.status === 401) {
      me = null;
      renderLogin("");
      return;
    }
    list.innerHTML = `<p class="error"></p>`;
    list.querySelector(".error").textContent = error.message;
  }
}

function attachStorageKey(projectId, chatId) {
  return `wca-attach:${projectId}:${chatId || "new"}`;
}

function loadAttachID(projectId, chatId) {
  try {
    return sessionStorage.getItem(attachStorageKey(projectId, chatId)) || "";
  } catch (_error) {
    return "";
  }
}

function saveAttachID(projectId, chatId, attachID) {
  try {
    if (attachID) {
      sessionStorage.setItem(attachStorageKey(projectId, chatId), attachID);
    }
  } catch (_error) {
    // sessionStorage が使えない環境でも端末自体は動かす。
  }
}

function clearAttachID(projectId, chatId) {
  try {
    sessionStorage.removeItem(attachStorageKey(projectId, chatId));
  } catch (_error) {
    // ignore
  }
}

function renderTerminal(projectId, chatId) {
  const project = projects.find((item) => item.id === projectId);
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
      </div>
      <form class="composer" id="composer">
        <textarea id="draft" placeholder="メッセージ"></textarea>
        <button class="primary" type="submit">送信</button>
      </form>
      <button class="scroll-bottom is-hidden" id="scroll-bottom" type="button">末尾にスクロール</button>
      <div class="floats" id="floats">
        <button class="hide" id="hide-floats" type="button" aria-label="操作キーを隠す">×</button>
        <button class="select" id="term-select-open" type="button">選択</button>
        <button class="up" type="button" data-key="up">↑</button>
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
  document.querySelector("h1").textContent = project ? project.name : projectId;
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
    theme: { background: "#0e1116" },
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(termElement);
  silenceTerminalHelperTextarea(term);
  const syncScrollBottom = () => {
    const buffer = term.buffer.active;
    // 末尾より上へ遡っているときだけ、末尾へ戻る導線を出す。
    scrollBottom.classList.toggle(
      "is-hidden",
      buffer.viewportY >= buffer.baseY,
    );
  };
  const scrollDisposable = term.onScroll(syncScrollBottom);
  scrollBottom.addEventListener("click", () => {
    term.scrollToBottom();
  });
  terminalSession = {
    term,
    fit,
    socket: null,
    route,
    projectId,
    chatId,
    attachID: loadAttachID(projectId, chatId),
    seenHello: false,
    hidden: false,
    leaving: false,
    agentExited: false,
    selectOverlayOpen: false,
    showSelectOverlay: null,
    hideSelectOverlay: null,
    reconnectTimer: null,
    unbindTouchGestures: null,
    unbindSelectOverlay: null,
    unbindFloatReveal: null,
    disposeScroll: () => scrollDisposable.dispose(),
    onVisibility: null,
    onOnline: null,
  };
  terminalSession.unbindTouchGestures = bindTerminalTouchGestures(
    term,
    termWrap,
  );
  terminalSession.unbindSelectOverlay = bindTerminalSelectOverlay(
    term,
    termWrap,
  );
  fitTerminal();
  syncScrollBottom();
  new ResizeObserver(() => fitTerminal()).observe(termElement);
  document.querySelector("#composer").addEventListener("submit", (event) => {
    event.preventDefault();
    const draft = document.querySelector("#draft");
    const text = draft.value;
    if (text.length === 0) {
      return;
    }
    draft.value = "";
    sendDraft(text);
  });
  floats.addEventListener("click", (event) => {
    const button = event.target.closest("button");
    if (!button) {
      return;
    }
    if (button.id === "term-select-open") {
      terminalSession.showSelectOverlay?.();
      return;
    }
    const key = button.dataset.key;
    if (!key) {
      return;
    }
    const sequences = {
      up: "\u001b[A",
      down: "\u001b[B",
      right: "\u001b[C",
      left: "\u001b[D",
      enter: "\r",
    };
    sendInput(sequences[key]);
  });
  document.querySelector("#hide-floats").addEventListener("click", () => {
    setFloatsHidden(true);
  });
  document.addEventListener("keydown", onTerminalKeydown);
  terminalSession.onVisibility = () => {
    if (document.visibilityState === "visible") {
      reconnectTerminal();
    }
  };
  terminalSession.onOnline = () => reconnectTerminal();
  document.addEventListener("visibilitychange", terminalSession.onVisibility);
  window.addEventListener("online", terminalSession.onOnline);
  connectTerminal();
}

function connectTerminal() {
  if (
    !terminalSession ||
    terminalSession.leaving ||
    terminalSession.agentExited
  ) {
    return;
  }
  if (
    terminalSession.socket &&
    (terminalSession.socket.readyState === WebSocket.OPEN ||
      terminalSession.socket.readyState === WebSocket.CONNECTING)
  ) {
    return;
  }
  const { projectId, chatId, term } = terminalSession;
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams({ project: projectId });
  if (chatId) {
    params.set("chat", chatId);
  }
  if (terminalSession.attachID) {
    params.set("attach", terminalSession.attachID);
  }
  const socket = new WebSocket(
    `${protocol}//${location.host}/ws/terminal?${params.toString()}`,
  );
  socket.binaryType = "arraybuffer";
  terminalSession.socket = socket;
  terminalSession.seenHello = false;
  socket.addEventListener("open", () => fitTerminal());
  socket.addEventListener("message", (event) => {
    if (!terminalSession || terminalSession.socket !== socket) {
      return;
    }
    if (typeof event.data === "string") {
      const message = JSON.parse(event.data);
      if (message.type === "hello" && message.attach) {
        terminalSession.seenHello = true;
        terminalSession.attachID = message.attach;
        saveAttachID(projectId, chatId, message.attach);
        return;
      }
      if (message.type === "exit") {
        terminalSession.agentExited = true;
        clearAttachID(projectId, chatId);
        term.writeln(`\r\nセッションが終了しました (code ${message.code})`);
      }
      return;
    }
    term.write(new Uint8Array(event.data));
  });
  socket.addEventListener("close", () => {
    if (!terminalSession || terminalSession.socket !== socket) {
      return;
    }
    terminalSession.socket = null;
    if (terminalSession.leaving || terminalSession.agentExited) {
      return;
    }
    // 期限切れの attach では upgrade 前に失敗するため、捨てて新規/chat 再接続に切り替える。
    if (!terminalSession.seenHello && terminalSession.attachID) {
      clearAttachID(projectId, chatId);
      terminalSession.attachID = "";
    }
    term.writeln("\r\n接続が切れました。再接続しています…");
    scheduleTerminalReconnect(1000);
  });
}

function scheduleTerminalReconnect(delayMs) {
  if (
    !terminalSession ||
    terminalSession.leaving ||
    terminalSession.agentExited
  ) {
    return;
  }
  if (terminalSession.reconnectTimer) {
    window.clearTimeout(terminalSession.reconnectTimer);
  }
  terminalSession.reconnectTimer = window.setTimeout(() => {
    if (
      !terminalSession ||
      terminalSession.leaving ||
      terminalSession.agentExited
    ) {
      return;
    }
    connectTerminal();
  }, delayMs);
}

function reconnectTerminal() {
  if (
    !terminalSession ||
    terminalSession.leaving ||
    terminalSession.agentExited
  ) {
    return;
  }
  if (
    terminalSession.socket &&
    terminalSession.socket.readyState === WebSocket.OPEN
  ) {
    return;
  }
  scheduleTerminalReconnect(0);
}

function onTerminalKeydown(event) {
  // 下書き入力中だけショートカットを避け、xterm 内部の textarea フォーカスは対象にする。
  if (!terminalSession || event.target.closest("#draft")) {
    return;
  }
  const sequences = {
    ArrowUp: "\u001b[A",
    ArrowDown: "\u001b[B",
    ArrowRight: "\u001b[C",
    ArrowLeft: "\u001b[D",
    Enter: "\r",
  };
  const sequence = sequences[event.key];
  if (!sequence) {
    return;
  }
  event.preventDefault();
  sendInput(sequence);
}

// 操作パネルは表示中に選択を邪魔しないよう、隠したときだけタップ検出で戻す。
function setFloatsHidden(hidden) {
  if (!terminalSession) {
    return;
  }
  terminalSession.hidden = hidden;
  document.querySelector("#floats")?.classList.toggle("is-hidden", hidden);
  if (hidden) {
    if (!terminalSession.unbindFloatReveal) {
      const termWrap = document.querySelector("#term-wrap");
      if (termWrap) {
        terminalSession.unbindFloatReveal = bindFloatRevealOnTap(termWrap);
      }
    }
    return;
  }
  if (terminalSession.unbindFloatReveal) {
    terminalSession.unbindFloatReveal();
    terminalSession.unbindFloatReveal = null;
  }
}

// ドラッグ開始の pointerdown では戻さず、移動の少ないタップだけを再表示に使う。
function bindFloatRevealOnTap(element) {
  const tapSlopPx = 10;
  let pointerId = null;
  let startX = 0;
  let startY = 0;
  const onPointerDown = (event) => {
    if (
      pointerId !== null ||
      event.isPrimary === false ||
      terminalSession?.selectOverlayOpen ||
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
    if (!terminalSession?.hidden || terminalSession.selectOverlayOpen) {
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
function silenceTerminalHelperTextarea(term) {
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
function bindTerminalSelectOverlay(term, termWrap) {
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
    if (!terminalSession?.selectOverlayOpen) {
      return;
    }
    terminalSession.selectOverlayOpen = false;
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
    if (floats && !terminalSession.hidden) {
      floats.classList.remove("is-hidden");
    }
    if (scrollBottom) {
      const buffer = term.buffer.active;
      scrollBottom.classList.toggle(
        "is-hidden",
        buffer.viewportY >= buffer.baseY,
      );
    }
  };
  const showOverlay = () => {
    if (!terminalSession) {
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
    terminalSession.selectOverlayOpen = true;
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
  terminalSession.showSelectOverlay = showOverlay;
  terminalSession.hideSelectOverlay = hideOverlay;
  return () => {
    closeButton.removeEventListener("click", onClose);
    overlay.removeEventListener("cancel", onDialogCancel);
    hideOverlay();
    overlay.remove();
  };
}

// Safari の dialog 既定レイアウトを避け、見た目の画面いっぱいに張り付ける。
function layoutSelectOverlay(overlay) {
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

// xterm.js 6.0.0 はタッチでのバッファスクロールが動かないため、
// 縦ドラッグを scrollToLine に変換する。選択面表示中は触らない。
function bindTerminalTouchGestures(term, element) {
  const scrollSlopPx = 12;
  let startY = null;
  let startViewportY = 0;
  let scrolling = false;
  const onTouchStart = (event) => {
    if (
      terminalSession?.selectOverlayOpen ||
      event.target.closest("#term-select-overlay")
    ) {
      startY = null;
      scrolling = false;
      return;
    }
    if (event.touches.length !== 1) {
      startY = null;
      scrolling = false;
      return;
    }
    startY = event.touches[0].clientY;
    startViewportY = term.buffer.active.viewportY;
    scrolling = false;
  };
  const onTouchMove = (event) => {
    if (
      startY === null ||
      event.touches.length !== 1 ||
      terminalSession?.selectOverlayOpen
    ) {
      return;
    }
    const currentY = event.touches[0].clientY;
    if (!scrolling) {
      if (Math.abs(currentY - startY) < scrollSlopPx) {
        return;
      }
      scrolling = true;
      startY = currentY;
      startViewportY = term.buffer.active.viewportY;
    }
    const rowHeight = element.clientHeight / term.rows;
    if (!(rowHeight > 0)) {
      return;
    }
    const deltaLines = (startY - currentY) / rowHeight;
    term.scrollToLine(Math.round(startViewportY + deltaLines));
  };
  const onTouchEnd = () => {
    startY = null;
    scrolling = false;
  };
  element.addEventListener("touchstart", onTouchStart, { passive: true });
  element.addEventListener("touchmove", onTouchMove, { passive: true });
  element.addEventListener("touchend", onTouchEnd, { passive: true });
  element.addEventListener("touchcancel", onTouchEnd, { passive: true });
  return () => {
    element.removeEventListener("touchstart", onTouchStart);
    element.removeEventListener("touchmove", onTouchMove);
    element.removeEventListener("touchend", onTouchEnd);
    element.removeEventListener("touchcancel", onTouchEnd);
  };
}

function fitTerminal() {
  if (!terminalSession) {
    return;
  }
  terminalSession.fit.fit();
  // 接続前や再接続待ちでは socket が null になり得る。
  if (terminalSession.socket?.readyState === WebSocket.OPEN) {
    terminalSession.socket.send(
      JSON.stringify({
        type: "resize",
        cols: terminalSession.term.cols,
        rows: terminalSession.term.rows,
      }),
    );
  }
}

// sendDraft は本文を一度に送り、その後に Enter でエージェントの入力を確定する。
// 同じ入力に含めた復帰は本文の改行になるため、Enter は別のキーとして届ける。
function sendDraft(text) {
  sendInput(text);
  window.setTimeout(() => {
    sendInput("\r");
  }, 80);
}

function sendInput(data) {
  if (
    !terminalSession ||
    terminalSession.socket?.readyState !== WebSocket.OPEN
  ) {
    return;
  }
  terminalSession.socket.send(JSON.stringify({ type: "input", data }));
}

function destroyTerminal() {
  document.removeEventListener("keydown", onTerminalKeydown);
  if (!terminalSession) {
    return;
  }
  terminalSession.leaving = true;
  if (terminalSession.reconnectTimer) {
    window.clearTimeout(terminalSession.reconnectTimer);
  }
  if (terminalSession.onVisibility) {
    document.removeEventListener(
      "visibilitychange",
      terminalSession.onVisibility,
    );
  }
  if (terminalSession.onOnline) {
    window.removeEventListener("online", terminalSession.onOnline);
  }
  if (terminalSession.unbindTouchGestures) {
    terminalSession.unbindTouchGestures();
  }
  if (terminalSession.unbindSelectOverlay) {
    terminalSession.unbindSelectOverlay();
  }
  if (terminalSession.unbindFloatReveal) {
    terminalSession.unbindFloatReveal();
  }
  terminalSession.disposeScroll();
  const socket = terminalSession.socket;
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
  terminalSession.term.dispose();
  terminalSession = null;
}

async function ensureProject(projectId) {
  if (!projects.some((project) => project.id === projectId)) {
    try {
      const body = await api("/api/projects");
      projects = body.projects;
    } catch (error) {
      if (error.status === 401) {
        me = null;
        renderLogin("");
        return null;
      }
    }
  }
  return (
    projects.find((project) => project.id === projectId) || {
      id: projectId,
      name: projectId,
    }
  );
}

async function logout() {
  await api("/api/logout", { method: "POST" });
  me = null;
  location.hash = "";
  renderLogin("");
}

async function api(path, options = {}) {
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
    throw error;
  }
  return body;
}
