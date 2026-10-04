# web-cursor-agent

## 概要

[Cursor](https://www.cursor.com/) を Web ブラウザーから利用するための Web アプリケーションです。
主に iPhone などのモバイル端末からの利用を想定しています。
同一ネットワーク内での利用を前提とします。

## 背景

- Cursor が他のサービスと大きく不利な点として、Web チャット機能がない点があります。本アプリケーションは、家庭内 LAN 限定でその制限を解消することを狙います。
- Cursor も iOS アプリでその問題を軽減していますが、以下の問題があります:
    - WSL 環境ではリモートコントロールが利用できない。
    - クラウドエージェントはクオータや課金体系が通常のエージェントと異なっており、通常のエージェントの延長線で考えて使えない。

## 前提条件

- 本システムは POSIX システム上で動作する。特に Windows 環境では WSL 環境を前提とする。
- Cursor CLI (`agent`) がインストールされているものとする。
- フロントエンド資産の取得には Node.js を使う。バージョンは `mise.toml` で固定する。

## フロントエンド資産

端末表示には [@xterm/xterm](https://www.npmjs.com/package/@xterm/xterm) と [@xterm/addon-fit](https://www.npmjs.com/package/@xterm/addon-fit) を使う。npm が配布するブラウザ向け成果物をビルド時に取得し、内容を変えずに `web/dist/vendor/` へコピーする。バージョン、配布 URL、整合性ハッシュは `web/package-lock.json` に記録される。

```bash
make -f Makefile.agent build
```

## ユーザーの追加

1. 依存ツールを入れ、サーバと画面用ファイルを作る。

```bash
mise install
make -f Makefile.agent build
```

2. 設定ファイルを用意し、プロジェクトのディレクトリを書く。

```bash
cp config.example.yaml config.yaml
```

3. ユーザーを追加する。パスワードはプロンプトで入力する。`--mac` は複数回指定できる。

```bash
bin/web-cursor-agent user upsert --config config.yaml --username alice --mac aa:bb:cc:dd:ee:ff
```

MAC アドレスは、スマートフォンの設定か、無線 LAN ルーターの接続クライアント一覧で確認する。サーバが接続元の MAC を観測できる環境 (Linux や WSL のミラーモード) では、登録したアドレス以外からのログインを拒否する。WSL2 の NAT ではスマートフォンの MAC を観測できないので、`config.yaml` の `mac_check` を `false` にする。パスワードと同一セグメントの制限は残る。

4. そのユーザーの Cursor アカウントで CLI にログインする。

```bash
bin/web-cursor-agent cursor-login --config config.yaml --username alice
```

ブラウザを開かず URL だけ出す場合は、先頭に `NO_OPEN_BROWSER=1` を付けて実行する。API キーを使う場合は、ログインの代わりに `var/cursor/alice/api_key` へキーを 1 行で書き、権限を `600` にする。

5. サーバを起動する。

```bash
bin/web-cursor-agent serve --config config.yaml
```

6. ブラウザでサーバを開く。WSL2 の `0.0.0.0` は WSL の仮想 NIC だけを指す。Windows 自身のブラウザは `http://127.0.0.1:8787` か、WSL の eth0 アドレスで開く。

常駐させて同じ LAN の別端末から開く場合は、ポート転送を使わず WSL のミラーモードにする。`%USERPROFILE%\.wslconfig` に次を書き、`wsl --shutdown` で反映する。反映には起動中の WSL がすべて止まる。

```ini
[wsl2]
networkingMode=mirrored
```

ミラーモードでは Windows の LAN アドレスが WSL からも見える。受信は Hyper-V ファイアウォールで TCP `8787` だけ許可する。管理者の PowerShell で次を実行する。`{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}` は、WSL という作成元を表すどの PC でも同じ ID である。

```powershell
New-NetFirewallHyperVRule -Name "web-cursor-agent" -DisplayName "web-cursor-agent" -Direction Inbound -VMCreatorId "{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}" -Protocol TCP -LocalPorts 8787
```

NAT のまま一時的に LAN へ出す場合だけ、管理者の PowerShell で WSL の eth0 アドレスへ転送する。接続先を `127.0.0.1` にすると、`0.0.0.0` で待つ転送自身へ戻り、接続が周回する。アドレスは WSL で `hostname -I` を実行して得る。このアドレスは WSL の再起動で変わるため、常駐の転送先にはしない。

```powershell
netsh interface portproxy add v4tov4 listenaddress=0.0.0.0 listenport=8787 connectaddress=<WSL の eth0 アドレス> connectport=8787
netsh advfirewall firewall add rule name="web-cursor-agent 8787" dir=in action=allow protocol=TCP localport=8787 profile=private
```

パスワードや MAC アドレスを変更するときも、同じ `user upsert` を使う。`--mac` を省略すると、登録済みの MAC アドレスは維持される。リストを空にするときは `--clear-macs` を付ける。

設計の詳細は [README.design.md](README.design.md) にまとめてある。

## 利用パターン

1. モバイル端末から Web ブラウザーを開き、サイトにアクセスする。
2. ログインフォームでログインする。
    - ログインしたユーザーにしたがってユーザーごとの Cursor 認証情報を使用する。
3. プロジェクトの一覧を表示し、選択する。
4. 既存セッションの一覧の表示し、そこから選択または新規セッションの開始を選択する。
5. `agent` を起動して対話モードに入る。
    - 仮想ターミナルを起動し、WebUI でその入出力を中継する。

## セキュリティ

- ユーザー名 + パスワードによる認証
- 同一セグメントからのアクセス制限
- ユーザーごとの MAC アドレスでのアクセス制限

## UI

- 画面右下に半透明の操作キーパネルを表示する。
    - カーソルキー
    - エンターキー
    - パネル右上の × で一時非表示
        - 画面タッチで再表示
- 入力したテキストをまとめて送信する。改行入力はそのまま改行文字として入力を受け付け、メッセージ送信は専用のボタンを設ける。
- 端末領域を縦にドラッグすると、チャット履歴をスクロールして遡れる。

## 設定ファイル、設定ツール

- 設定ファイル
    - config.yaml
        - プロジェクトの一覧
    - users.yaml
        - ユーザーの一覧
            - ユーザー名
            - パスワード (ハッシュ化されたもの)
            - MAC アドレスリスト

- 設定ツール
    - ユーザーの追加・更新
        - ユーザー名の入力とパスワードの入力を受け付けて `users.yaml` に追加・更新する。
