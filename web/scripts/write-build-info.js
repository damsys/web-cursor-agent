import { execFileSync } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const repoRoot = path.resolve(packageRoot, "..");
const staticDir = path.join(packageRoot, "static");
const jsonPath = path.join(staticDir, "build-info.json");
const jsPath = path.join(staticDir, "build-info.js");

/**
 * 画面で配信中の版を判別できるよう、git のコミットと作業ツリー状態を書き出す。
 * スーパーリロード確認のため、フロントが読む JS にも同じ内容を埋め込む。
 */
function readGitInfo() {
  const run = (args) =>
    execFileSync("git", args, {
      cwd: repoRoot,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  try {
    const commit = run(["rev-parse", "--short", "HEAD"]);
    const dirty = run(["status", "--porcelain"]).length > 0;
    return { commit, dirty };
  } catch (_error) {
    return { commit: "unknown", dirty: false };
  }
}

async function writeBuildInfo() {
  const { commit, dirty } = readGitInfo();
  const info = {
    commit,
    dirty,
    builtAt: new Date().toISOString(),
  };
  await fs.mkdir(staticDir, { recursive: true });
  await fs.writeFile(jsonPath, `${JSON.stringify(info, null, 2)}\n`);
  // app.js より先に読み、キャッシュされた古い app.js でも版表示だけは新しい値にできる。
  await fs.writeFile(
    jsPath,
    `window.__WCA_BUILD__ = ${JSON.stringify(info)};\n`,
  );
  const mark = dirty ? " dirty" : "";
  console.log(
    `wrote build-info (${commit}${mark}) -> static/build-info.json, static/build-info.js`,
  );
}

writeBuildInfo().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
