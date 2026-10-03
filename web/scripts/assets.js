import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

// 公開パッケージに含まれるブラウザ向け成果物。配信時もこの相対配置を保つ。
const assetFiles = [
  ["node_modules/@xterm/xterm/LICENSE", "dist/vendor/@xterm/xterm/LICENSE"],
  [
    "node_modules/@xterm/xterm/css/xterm.css",
    "dist/vendor/@xterm/xterm/css/xterm.css",
  ],
  [
    "node_modules/@xterm/xterm/lib/xterm.js",
    "dist/vendor/@xterm/xterm/lib/xterm.js",
  ],
  [
    "node_modules/@xterm/xterm/lib/xterm.js.map",
    "dist/vendor/@xterm/xterm/lib/xterm.js.map",
  ],
  [
    "node_modules/@xterm/addon-fit/LICENSE",
    "dist/vendor/@xterm/addon-fit/LICENSE",
  ],
  [
    "node_modules/@xterm/addon-fit/lib/addon-fit.js",
    "dist/vendor/@xterm/addon-fit/lib/addon-fit.js",
  ],
  [
    "node_modules/@xterm/addon-fit/lib/addon-fit.js.map",
    "dist/vendor/@xterm/addon-fit/lib/addon-fit.js.map",
  ],
];

/**
 * 取得元と配信先の絶対パスを返す。
 * ソースマップが同じディレクトリのファイルを参照できるよう、パッケージ内の相対パスは変えない。
 */
export function assetPaths() {
  return assetFiles.map(([source, destination]) => ({
    source: path.join(packageRoot, source),
    destination: path.join(packageRoot, destination),
  }));
}
