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
    </main>
  `;
  document.querySelector("#logout").addEventListener("click", logout);
  const list = document.querySelector("#project-list");
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
      <div class="term-wrap" id="term"></div>
      <form class="composer" id="composer">
        <textarea id="draft" placeholder="メッセージ"></textarea>
        <button class="primary" type="submit">送信</button>
      </form>
      <button class="scroll-bottom is-hidden" id="scroll-bottom" type="button">末尾にスクロール</button>
      <div class="floats" id="floats">
        <button class="hide" id="hide-floats" type="button" aria-label="操作キーを隠す">×</button>
        <button class="up" type="button" data-key="up">↑</button>
        <button class="left" type="button" data-key="left">←</button>
        <button class="down" type="button" data-key="down">↓</button>
        <button class="right" type="button" data-key="right">→</button>
        <button class="enter" type="button" data-key="enter">Enter</button>
      </div>
    </main>
  `;
  document.querySelector("h1").textContent = project ? project.name : projectId;
  document.querySelector("#back").addEventListener("click", () => {
    location.hash = `#/projects/${encodeURIComponent(projectId)}`;
  });
  const termElement = document.querySelector("#term");
  const scrollBottom = document.querySelector("#scroll-bottom");
  const term = new Terminal({
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
    reconnectTimer: null,
    unbindTouchScroll: bindTerminalTouchScroll(term, termElement),
    disposeScroll: () => scrollDisposable.dispose(),
    onVisibility: null,
    onOnline: null,
  };
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
  document.querySelector("#floats").addEventListener("click", (event) => {
    const key = event.target.closest("button")?.dataset.key;
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
    terminalSession.hidden = true;
    document.querySelector("#floats").classList.add("is-hidden");
  });
  termElement.addEventListener("pointerdown", () => {
    if (!terminalSession?.hidden) {
      return;
    }
    terminalSession.hidden = false;
    document.querySelector("#floats").classList.remove("is-hidden");
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
  if (!terminalSession || event.target.closest("textarea")) {
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

// xterm.js 6.0.0 はタッチでのバッファスクロールが動かないため、
// 縦ドラッグ量を公開 API の scrollToLine に変換して履歴を遡れるようにする。
function bindTerminalTouchScroll(term, element) {
  let startY = null;
  let startViewportY = 0;
  const onTouchStart = (event) => {
    if (event.touches.length !== 1) {
      startY = null;
      return;
    }
    startY = event.touches[0].clientY;
    startViewportY = term.buffer.active.viewportY;
  };
  const onTouchMove = (event) => {
    if (startY === null || event.touches.length !== 1) {
      return;
    }
    const rowHeight = element.clientHeight / term.rows;
    if (!(rowHeight > 0)) {
      return;
    }
    const deltaLines = (startY - event.touches[0].clientY) / rowHeight;
    term.scrollToLine(Math.round(startViewportY + deltaLines));
  };
  const onTouchEnd = () => {
    startY = null;
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
  if (terminalSession.socket.readyState === WebSocket.OPEN) {
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
    terminalSession.socket.readyState !== WebSocket.OPEN
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
  terminalSession.unbindTouchScroll();
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
