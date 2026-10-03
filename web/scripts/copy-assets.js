import fs from "node:fs/promises";
import path from "node:path";

import { assetPaths } from "./assets.js";

/**
 * インストール済みパッケージのブラウザ向け成果物を、内容を変えずに配信ディレクトリへコピーする。
 */
async function copyAssets() {
  for (const file of assetPaths()) {
    await fs.mkdir(path.dirname(file.destination), { recursive: true });
    await fs.copyFile(file.source, file.destination);
  }
}

copyAssets().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
