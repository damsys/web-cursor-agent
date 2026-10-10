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
- GitHub CLI (`gh`) は `mise.toml` でバージョンを固定し、`mise install` で入れる。
- フロントエンド資産の取得には Node.js を使う。バージョンは `mise.toml` で固定する。

## フロントエンド資産

端末表示には [@xterm/xterm](https://www.npmjs.com/package/@xterm/xterm) と [@xterm/addon-fit](https://www.npmjs.com/package/@xterm/addon-fit) を使う。npm が配布するブラウザ向け成果物をビルド時に取得し、内容を変えずに `web/dist/vendor/` へコピーする。バージョン、配布 URL、整合性ハッシュは `web/package-lock.json` に記録される。

```bash
make -f Makefile.agent build
```

ビルド時に git の短ハッシュ・未コミット有無・ビルド日時が `web/static/build-info.js` へ書き出される。ログイン後のプロジェクト一覧下部に `ビルド: <hash> (日時)` または `ビルド: <hash> +未コミット (日時)` と表示され、フロント資産のスーパーリロード確認にも使える。index.html 配信時は app.js/css/build-info.js にその版の query を付け、古い画面資産の再利用を避ける。

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

`user upsert` はユーザー用の Cursor ディレクトリを用意し、切断時の状態判定に使う CLI の status indicators も有効にする。

MAC アドレスは、スマートフォンの設定か、無線 LAN ルーターの接続クライアント一覧で確認する。サーバが接続元の MAC を観測できる環境 (Linux や WSL のミラーモード) では、登録したアドレス以外からのログインを拒否する。WSL2 の NAT ではスマートフォンの MAC を観測できないので、`config.yaml` の `mac_check` を `false` にする。パスワードと同一セグメントの制限は残る。

4. そのユーザーの Cursor アカウントで CLI にログインする。

```bash
bin/web-cursor-agent cursor-login --config config.yaml --username alice
```

ブラウザを開かず URL だけ出す場合は、先頭に `NO_OPEN_BROWSER=1` を付けて実行する。API キーを使う場合は、ログインの代わりに `var/cursor/alice/api_key` へキーを 1 行で書き、権限を `600` にする。

WebSearch を毎回承認せずに使いたいときは、ユーザーごとの `CURSOR_CONFIG_DIR` (`var/cursor/<username>/cursor/cli-config.json`) で自動承認を有効にする。`~/.cursor` ではなく、このパスを書き換える。

```bash
bin/web-cursor-agent cursor-auto-websearch --config config.yaml --username alice
```

5. そのユーザー環境で GitHub CLI (`gh`) にログインする。エージェント起動時は `XDG_CONFIG_HOME` をユーザーごとに切り替えるため、OS ユーザーの `~/.config/gh` は参照されない。

```bash
bin/web-cursor-agent gh-login --config config.yaml --username alice
```

状態の確認は `gh-status` を使う。`gh` 自体は `mise install` で入れる。

6. サーバを起動する。

```bash
bin/web-cursor-agent serve --config config.yaml
```

7. ブラウザでサーバを開く。WSL2 の `0.0.0.0` は WSL の仮想 NIC だけを指す。Windows 自身のブラウザは `http://127.0.0.1:8787` か、WSL の eth0 アドレスで開く。

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

セッション名は、対話ターンが 3 / 10 / 20 / 以降 10 ごとになると自動で日本語へ更新する (既定オン)。`agent -p --mode ask` で名前を生成し、idle 時に `/rename` する。命名用エージェントはチャットあたり最大 3 回までしか起動しない。止めるときは `config.yaml` に次を書く。

```yaml
auto_rename:
  enabled: false
```

手動で `/rename` したあとは、そのセッションの自動更新は止める。

## 常駐 / PC 起動時自動起動

PC 起動後にサーバを自動で待受状態にするため、次の二段で常駐させる。

1. Windows のタスクスケジューラが WSL distro を起こし、終了しないプロセスで維持する。
2. WSL 内の systemd user サービスが `web-cursor-agent serve` を起動する。

事前条件は、`make -f Makefile.agent build` 済みであること、`config.yaml` があること、LAN 公開する場合は上記のミラーモードと Hyper-V ファイアウォール設定が済んでいることである。`/etc/wsl.conf` に `[boot] systemd=true` があることも必要である。

### WSL 側

リポジトリ直下で次を実行する。unit を `~/.config/systemd/user/` へ置き、`sudo loginctl enable-linger` でログイン前でも user サービスを起動できるようにし、すぐ有効化する。linger の有効化では sudo のパスワードを求められることがある。

```bash
make -f Makefile.agent install-service
systemctl --user status web-cursor-agent
```

停止・無効化・更新の手順は後述する。

### サービスの停止

一時的に止めるだけなら、WSL 内で次を実行する。PC を再起動すると、linger と有効化が残っているため再び起動する。

```bash
systemctl --user stop web-cursor-agent
```

再起動後も自動起動させたくないときは、無効化してから止める。

```bash
systemctl --user disable --now web-cursor-agent
```

常駐設定ごと外すときは次を使う。linger は他用途の可能性があるため外さない。不要なら手動で `sudo loginctl disable-linger "$USER"` する。Windows 側の WSL keep-alive も不要なら `unregister-wsl-autostart.ps1` を実行する。

```bash
make -f Makefile.agent uninstall-service
```

### アプリケーションの更新

リポジトリ直下でソースを取り込み、ビルドし直してからサービスを上げ直す。`config.yaml` と `users.yaml`、`var/` はそのまま使う。

```bash
git pull
mise install
make -f Makefile.agent build
systemctl --user restart web-cursor-agent
systemctl --user status web-cursor-agent
```

ビルドまで済ませたあとなら、プロジェクト一覧の「メンテナンス」からサービス再起動だけを実行できる。再起動は systemd user サービス経由で予約され、接続中の端末セッションは切断される。

unit テンプレート (`deploy/systemd/web-cursor-agent.user.service`) やインストール手順が変わったときだけ、再ビルドのあとに `make -f Makefile.agent install-service` をやり直す。Windows の登録スクリプトが変わったときだけ、`register-wsl-autostart.ps1` を再実行する。

### Windows 側 (ログイン前起動)

WSL は Windows 側が起動しないと distro が起きない。所有者の Windows ユーザーで、起動時に distro を維持するタスクを登録する。PowerShell でリポジトリのスクリプトを実行する。既定の distro 名は `Debian` である。違う名前なら `-Distro` を付ける。

```powershell
cd \\wsl$\Debian\home\<user>\workspace\web-cursor-agent\deploy\windows
powershell -ExecutionPolicy Bypass -File .\register-wsl-autostart.ps1
```

S4U 登録がアクセス拒否されるのはよくある。その場合はパスワード入力に進む。パスワード経路でもアクセス拒否になるときは、管理者として開いた PowerShell で同じコマンドを実行する。

確認は次のとおり。

```powershell
Start-ScheduledTask -TaskName web-cursor-agent-wsl
wsl -l -v
```

WSL が `Running` になったあと、WSL 内で `systemctl --user status web-cursor-agent` が active なら成功である。

手動でタスクを作る場合の要点は次のとおり。

- プログラムは `C:\Program Files\WSL\wsl.exe` を優先する。無ければ `C:\Windows\System32\wsl.exe`。
- 引数は `-d Debian -u root -- sleep infinity`。
- トリガーは「スタートアップ時」、遅延は 30〜60 秒。
- 「ユーザーがログオンしているかどうかにかかわらず実行する」。
- 「タスクを停止するまでの時間」は無効にする。

解除は次を実行する。

```powershell
powershell -ExecutionPolicy Bypass -File .\unregister-wsl-autostart.ps1
```

### ログイン前起動が失敗したとき

起動時タスクを実行しても WSL が `Running` にならない、または `wsl.exe` がすぐ失敗する場合は、同じスクリプトをログオン時起動へ切り替える。

```powershell
powershell -ExecutionPolicy Bypass -File .\register-wsl-autostart.ps1 -AtLogOn
```

WSL 内の systemd 設定はそのままでよい。

設計の詳細は [README.design.md](README.design.md) にまとめてある。

## 利用パターン

1. モバイル端末から Web ブラウザーを開き、サイトにアクセスする。
2. ログインフォームでログインする。
    - ログインしたユーザーにしたがってユーザーごとの Cursor 認証情報を使用する。
3. プロジェクトの一覧を表示し、選択する。
4. 既存セッションの一覧の表示し、そこから選択または新規セッションの開始を選択する。
    - 会話のない空セッションは一覧に出さない。
    - 使わないセッションは「非表示」で一覧から外せる（履歴は残る）。
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
        - `detach_grace` (WebSocket 切断後に agent を残す時間。既定 `30m`)
        - `auto_rename.enabled` (ターン数に応じたセッション名の自動更新。既定 `true`)
    - users.yaml
        - ユーザーの一覧
            - ユーザー名
            - パスワード (ハッシュ化されたもの)
            - MAC アドレスリスト

- 設定ツール
    - ユーザーの追加・更新
        - ユーザー名の入力とパスワードの入力を受け付けて `users.yaml` に追加・更新する。
        - あわせてそのユーザーの Cursor ホームと CLI status indicators を用意する。
    - `cursor-auto-websearch`
        - ユーザーごとの `CURSOR_CONFIG_DIR/cli-config.json` で WebSearch の自動承認 (`autoAcceptWebSearch`) を有効にする。
    - `gh-login` / `gh-status`
        - ユーザーごとの `XDG_CONFIG_HOME` で GitHub CLI のログインと状態確認を行う。
