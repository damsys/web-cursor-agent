import { state, app } from "/static/js/state.js";
import { api } from "/static/js/api.js";
import { setDocumentTitle } from "/static/js/title.js";
import { isCurrentRoute } from "/static/js/router.js";
import { clearAttachID } from "/static/js/attach.js";

export function renderLogin(message) {
  setDocumentTitle();
  app.innerHTML = `
    <main class="screen narrow">
      <h1>ログイン</h1>
      <nav id="connection-links" class="connection-links stack" aria-label="接続案内"></nav>
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
  fillConnectionLinks(app);
  const error = document.querySelector("#login-error");
  error.textContent = message || "";
  document
    .querySelector("#login-form")
    .addEventListener("submit", async (event) => {
      event.preventDefault();
      const data = new FormData(event.currentTarget);
      error.textContent = "";
      try {
        state.me = await api("/api/login", {
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

// HTTPS 誘導と証明書セットアップへのリンクを置く。
export async function fillConnectionLinks(root) {
  const nav = root.querySelector("#connection-links");
  if (!nav) {
    return;
  }
  let site;
  try {
    site = await api("/api/site");
  } catch {
    nav.hidden = true;
    return;
  }
  nav.replaceChildren();
  if (site.tlsEnabled && location.protocol === "http:" && site.httpsUrl) {
    const httpsLink = document.createElement("a");
    httpsLink.className = "secondary";
    httpsLink.href = site.httpsUrl;
    httpsLink.textContent = "HTTPS で開く";
    nav.append(httpsLink);
  }
  const setupLink = document.createElement("a");
  setupLink.className = "secondary";
  setupLink.href = site.setupUrl || "/setup";
  setupLink.textContent = "証明書のインストール";
  nav.append(setupLink);
}

export async function renderProjects() {
  setDocumentTitle();
  app.innerHTML = `
    <main class="screen narrow">
      <div class="bar" style="padding:0 0 12px; border:0">
        <h1>プロジェクト</h1>
        <button class="secondary" id="logout" type="button">ログアウト</button>
      </div>
      <nav id="connection-links" class="connection-links stack" aria-label="接続案内"></nav>
      <div id="project-list" class="stack"></div>
      <section class="maintenance" id="maintenance">
        <h2>メンテナンス</h2>
        <p class="meta">ビルド済みのバイナリでサービスを上げ直します。接続中の端末は切断されます。</p>
        <button class="secondary" id="restart-service" type="button">サービスを再起動</button>
      </section>
      <p class="meta build-info" id="build-info">ビルド: …</p>
    </main>
  `;
  fillConnectionLinks(app);
  document.querySelector("#logout").addEventListener("click", logout);
  document
    .querySelector("#restart-service")
    .addEventListener("click", onRestartServiceClick);
  const list = document.querySelector("#project-list");
  renderBuildInfo();
  try {
    const body = await api("/api/projects");
    if (!isCurrentRoute("projects")) {
      return;
    }
    state.projects = body.projects;
    if (state.projects.length === 0) {
      list.innerHTML = `<p class="meta">プロジェクトがありません</p>`;
      return;
    }
    for (const project of state.projects) {
      const link = document.createElement("a");
      link.className = "card";
      link.href = `#/projects/${encodeURIComponent(project.id)}`;
      link.innerHTML = `<strong></strong><span></span>`;
      link.querySelector("strong").textContent = project.name;
      link.querySelector("span").textContent = project.path;
      list.append(link);
    }
  } catch (error) {
    if (error.status === 401) {
      return;
    }
    list.innerHTML = `<p class="error"></p>`;
    list.querySelector(".error").textContent = error.message;
  }
}

export function onRestartServiceClick() {
  const confirmed = window.confirm(
    "サービスを再起動します。接続中の端末セッションは切断されます。よろしいですか？",
  );
  if (!confirmed) {
    return;
  }
  restartService();
}

export async function restartService() {
  app.innerHTML = `
    <main class="screen narrow">
      <h1>メンテナンス</h1>
      <p id="restart-status">再起動しています…（数十秒かかることがあります）</p>
      <p class="error" id="restart-error"></p>
    </main>
  `;
  const status = document.querySelector("#restart-status");
  const error = document.querySelector("#restart-error");
  try {
    await api("/api/maintenance/restart", {
      method: "POST",
      body: "{}",
    });
  } catch (caught) {
    error.textContent = caught.message;
    status.textContent = "再起動を開始できませんでした。";
    const back = document.createElement("button");
    back.className = "secondary";
    back.type = "button";
    back.textContent = "戻る";
    back.addEventListener("click", () => {
      renderProjects();
    });
    app.querySelector("main").append(back);
    return;
  }
  try {
    await waitForServerAfterRestart();
    await renderProjects();
  } catch (caught) {
    status.textContent = "再起動の完了を確認できませんでした。";
    error.textContent = caught.message;
  }
}

// サーバ停止〜起動のあいだ fetch が失敗するので、復帰するまで /api/me を待つ。
export async function waitForServerAfterRestart() {
  await sleep(3000);
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    try {
      state.me = await api("/api/me");
      return;
    } catch (error) {
      if (error.status === 401) {
        throw new Error("再起動後にログインが必要です");
      }
      await sleep(1500);
    }
  }
  throw new Error("ページを再読み込みしてください");
}

export function sleep(ms) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

// 配信中の版を判別できるよう、ビルド時に生成した build-info.js の値を表示する。
// 未コミット時は同一ハッシュが続くため、ビルド日時も併記する。
export function renderBuildInfo() {
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
  const builtAt = formatBuildAt(info.builtAt);
  const when = builtAt ? ` (${builtAt})` : "";
  el.textContent = `ビルド: ${info.commit || "unknown"}${dirty}${when}`;
}

// 画面表示用に ISO 8601 のビルド日時をローカル時刻へ直す。
export function formatBuildAt(value) {
  if (typeof value !== "string" || value === "") {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString("ja-JP");
}

export async function renderSessions(projectId) {
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
        <a class="primary" id="new-session" href="#/projects/${encodeURIComponent(projectId)}/terminal">新しいセッション</a>
        <div id="session-list" class="stack"></div>
      </div>
    </main>
  `;
  document.querySelector("h1").textContent = project.name;
  setDocumentTitle(project.name);
  document.querySelector("#back").addEventListener("click", () => {
    location.hash = "#/projects";
  });
  // 同一タブで新規を開くときは前回の未確定 attach を捨て、意図せず再接続しないようにする。
  document.querySelector("#new-session").addEventListener("click", (event) => {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }
    clearAttachID(projectId, "");
  });
  const list = document.querySelector("#session-list");
  try {
    const body = await api(
      `/api/projects/${encodeURIComponent(projectId)}/sessions`,
    );
    if (!isCurrentRoute("sessions", projectId)) {
      return;
    }
    if (body.sessions.length === 0) {
      list.innerHTML = `<p class="meta">セッションがありません</p>`;
      return;
    }
    for (const session of body.sessions) {
      const row = document.createElement("div");
      row.className = "session-row";

      const link = document.createElement("a");
      link.className = "card session-open";
      link.href = `#/projects/${encodeURIComponent(projectId)}/terminal/${encodeURIComponent(session.id)}`;
      link.innerHTML = `<strong></strong><span></span>`;
      link.querySelector("strong").textContent = session.title || "無題";
      link.querySelector("span").textContent = new Date(
        session.updated_at_ms,
      ).toLocaleString("ja-JP");

      const hide = document.createElement("button");
      hide.className = "secondary session-hide";
      hide.type = "button";
      hide.textContent = "非表示";
      hide.addEventListener("click", async () => {
        const confirmed = window.confirm(
          "このセッションを一覧から外しますか？履歴は残ります。",
        );
        if (!confirmed) {
          return;
        }
        hide.disabled = true;
        try {
          await api(
            `/api/projects/${encodeURIComponent(projectId)}/sessions/${encodeURIComponent(session.id)}/hide`,
            { method: "POST" },
          );
          row.remove();
          if (!list.querySelector(".session-row")) {
            list.innerHTML = `<p class="meta">セッションがありません</p>`;
          }
        } catch (caught) {
          hide.disabled = false;
          if (caught.status === 401) {
            return;
          }
          window.alert(caught.message);
        }
      });

      row.append(link, hide);
      list.append(row);
    }
  } catch (error) {
    if (error.status === 401) {
      return;
    }
    list.innerHTML = `<p class="error"></p>`;
    list.querySelector(".error").textContent = error.message;
  }
}

export async function ensureProject(projectId) {
  if (!state.projects.some((project) => project.id === projectId)) {
    try {
      const body = await api("/api/projects");
      state.projects = body.projects;
    } catch (error) {
      if (error.status === 401) {
        return null;
      }
    }
  }
  return (
    state.projects.find((project) => project.id === projectId) || {
      id: projectId,
      name: projectId,
    }
  );
}

export async function loadSessionTitle(projectId, chatId) {
  if (!chatId) {
    return "";
  }
  try {
    const body = await api(
      `/api/projects/${encodeURIComponent(projectId)}/sessions`,
    );
    const session = body.sessions.find((item) => item.id === chatId);
    return session?.title || "";
  } catch (error) {
    if (error.status === 401) {
      return "";
    }
    return "";
  }
}

export async function logout() {
  await api("/api/logout", { method: "POST" });
  state.me = null;
  location.hash = "";
  renderLogin("");
}
