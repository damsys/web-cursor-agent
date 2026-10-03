import fs from "node:fs/promises";

import { assetPaths } from "./assets.js";

/**
 * 配信ディレクトリへコピーしたファイルが、取得したパッケージとバイト単位で同じことを確認する。
 */
async function verifyAssets() {
  const mismatches = [];
  for (const file of assetPaths()) {
    const source = await fs.readFile(file.source);
    let copied;
    try {
      copied = await fs.readFile(file.destination);
    } catch (error) {
      if (error && error.code === "ENOENT") {
        mismatches.push(file.destination);
        continue;
      }
      throw error;
    }
    if (!source.equals(copied)) {
      mismatches.push(file.destination);
    }
  }
  if (mismatches.length > 0) {
    throw new Error(
      `vendor output does not match installed packages: ${mismatches.join(", ")}`,
    );
  }
}

verifyAssets().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
