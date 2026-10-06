# 設計方針

家庭内 LAN から、ブラウザで Cursor CLI (`agent`) の対話セッションを使う。サーバは 1 プロセスで、アプリのユーザーごとに Cursor の認証とチャット履歴だけを分ける。

## 全体構成

`listen` が `0.0.0.0` または未指定のときは、IPv4 と IPv6 のソケットを別に開く。Go の単一の TCP 待受は IPv6 にまとまり、WSL2 が Windows の `127.0.0.1` をそこへ転送しないためである。WSL2 の `0.0.0.0` は WSL の仮想 NIC であり、Windows の LAN アドレスではない。Windows 自身は `127.0.0.1` と WSL の eth0 アドレスで接続する。常駐で同じ LAN の別端末から届ける場合は、WSL をミラーモードにし、Hyper-V ファイアウォールで待受ポートだけを許可する。ポート転送は使わない。NAT のまま転送する場合、接続先は WSL の eth0 アドレスに限る。`127.0.0.1` は `0.0.0.0` の転送自身に戻って接続が周回する。eth0 アドレスは WSL の再起動で変わるため、常駐の固定転送先にはしない。

ブラウザは静的な画面を受け取り、ログイン後にプロジェクトと既存セッションを選ぶ。セッションを開くと WebSocket でサーバへ接続し、サーバはそのユーザーの認証ディレクトリを環境変数で指定して `agent` を仮想端末で起動する。仮想端末の入出力を WebSocket で中継する。

```
ブラウザ --HTTP/WebSocket--> web-cursor-agent --PTY--> agent
```

- 画面と API は同一オリジンで提供する。
- プロジェクトの実体は `config.yaml` に書いたディレクトリである。ブラウザからパスは渡さない。
- 1 つのランタイムセッションが 1 つの `agent` プロセスに対応する。WebSocket はそれにアタッチする。
- 非意図の切断 (モバイルのスリープなど) では、すぐにプロセスを終了せず `detach_grace` (既定 30 分) のあいだ残す。クライアントは `attach` ID で同じプロセスへ再接続する。
- 画面からの意図的な離脱では `{"type":"close"}` を送る。CLI の status indicators (端末タイトル) が Ready なら即終了し、Working / Waiting または不明なら猶予付きで残す。
- 会話そのものは Cursor のチャット履歴にも残るので、猶予を過ぎたあとは一覧から `--resume` で開き直せる。
- 同時に開けるセッションは、切断中のものも含めてユーザーあたり 4 つまでとする。

## Cursor CLI の認証

アプリのユーザーごとに、Cursor アカウントを分けて使う。分離の対象は認証トークン、CLI 設定、チャット履歴である。

Linux 版の `agent` は、認証トークンを `$XDG_CONFIG_HOME/cursor/auth.json` (未設定時は `~/.config/cursor/auth.json`) に保存する。CLI 設定のディレクトリは `CURSOR_CONFIG_DIR`、チャット履歴のディレクトリは `CURSOR_DATA_DIR` で上書きできる。既定はどちらも `~/.cursor` である。macOS のキーチェーンとは異なり、これらはファイルなので、プロセスの環境変数だけでアカウントを切り替えられる。

採用する割り当ては次のとおり。`state_dir` の既定は設定ファイルと同じディレクトリの `var` である。

| 環境変数 | 値 |
| --- | --- |
| `XDG_CONFIG_HOME` | `<state_dir>/cursor/<username>` |
| `CURSOR_CONFIG_DIR` | `<state_dir>/cursor/<username>/cursor` |
| `CURSOR_DATA_DIR` | `<state_dir>/cursor/<username>/cursor` |

この結果、認証ファイルは `<state_dir>/cursor/<username>/cursor/auth.json` になる。初回は `cursor-login` が、この環境で `agent login` を実行する。API キーを使う場合は、ログインの代わりに `<state_dir>/cursor/<username>/api_key` にキーを 1 行で置く。サーバは起動のたびにこのファイルを読み、空でなければ `CURSOR_API_KEY` を子プロセスへ渡す。

切断時の busy/idle 判定のため、`<CURSOR_CONFIG_DIR>/cli-config.json` の `display.showStatusIndicators` を有効にする。`user upsert` とセッション開始時の `Ensure` が、既存の他設定を維持したままこの項目だけを立てる。

`HOME` は変えない。`agent` が起動するシェルの git や ssh は、サーバを実行している OS ユーザーのものを使う。一方 `gh` は設定を `$XDG_CONFIG_HOME/gh` に置くため、ユーザーごとの `XDG_CONFIG_HOME` 切り替えの影響を受ける。OS ユーザーの `~/.config/gh` は見えないので、初回は `gh-login` が同じ環境で `gh auth login` を実行する。認証情報は `<state_dir>/cursor/<username>/gh/` に保存される。

サーバプロセスが IDE のサンドボックスから環境変数を引き継いでいる場合は、子プロセスへ渡す前にそのサンドボックス用の変数を除く。親が持つ `CURSOR_API_KEY` も除き、そのユーザーのファイルがあるときだけ付け直す。

UNIX ユーザーをアプリのユーザーごとに作る方式は採らない。認証ファイルの分離には環境変数で足る。OS ユーザーを分けると、ユーザー追加のたびにアカウント、sudo、プロジェクトディレクトリの権限が必要になり、WSL では運用が重い。このシステムが分けたいのは Cursor アカウントであり、プロジェクトへの OS 権限はサーバプロセスに揃える。

チャット一覧は、対象プロジェクトの絶対パスの MD5 をディレクトリ名として `<CURSOR_DATA_DIR>/chats/<md5>/` を読む。これは `agent` が履歴を置く規則に合わせたものである。各セッションの `meta.json` からタイトルと更新時刻を読む。

## Web の認証とアクセス制限

接続は次の順で制限する。

1. 接続元 IP が、サーバのいずれかのネットワークインターフェースと同一サブネットか、loopback か、`allow_cidrs` に含まれる。`docker0`、`podman0`、`br-`、`veth`、`cni`、`flannel` で始まるインターフェースは、LAN の判定に使わない。`X-Forwarded-For` は見ない。このアプリは LAN に直接公開し、転送ヘッダを信用するとセグメント制限を偽装できるためである。
2. ユーザー名とパスワード。パスワードは bcrypt でハッシュし、`users.yaml` に保存する。失敗時の応答は、ユーザーの有無を区別しない。ログイン失敗は IP ごとに数え、5 分間に 8 回を超えたら拒否する。
3. `mac_check` が有効なとき、接続元 IP の MAC アドレスが、そのユーザーの許可リストに含まれる。loopback は MAC を持たないので確認しない。リストが空のリモート接続は拒否する。MAC は `/proc/net/arp` を見て、無ければ `ip neigh` を見る。

ログイン成功後のセッションは、ランダムなトークンを `HttpOnly` かつ `SameSite=Strict` の Cookie で持ち、`<state_dir>/sessions.json` に保存する。有効期限は 30 日で、期限はログインのたびに決める。サーバ再起動後もファイルから復元する。TLS のときだけ `Secure` を付ける。家庭内では HTTP で使うため、常時 `Secure` にはしない。

状態を変えるリクエストと WebSocket は、`Origin` がリクエストの Host と一致するときだけ受け付ける。

ログイン済みユーザーは `POST /api/maintenance/restart` でサービス再起動を予約できる。サーバは `systemd-run --user` で自プロセスから切り離し、短時間待ってから `systemctl --user restart web-cursor-agent.service` を実行する。ビルドは含めない。進行中の再起動予約があるあいだの再リクエストは拒否する。

`mac_check` の既定は有効である。WSL2 の NAT では、サーバから見える接続元が Windows 側の仮想アダプタになり、スマートフォンの MAC アドレスを観測できない。その環境では `mac_check` を無効にし、パスワードと同一セグメント制限で運用する。Linux や WSL のミラーモードのように、接続元が同一 L2 に見える場合は有効のままにする。

## 仮想端末

`agent` は次の引数で起動する。プロジェクトは管理者が設定ファイルで信頼しているので、ワークスペース信頼の確認は出さない。

- 新規: `agent --workspace <path> --trust`
- 再開: `agent --resume <chatId> --workspace <path> --trust`

チャット ID は UUID に限り、そのユーザーの履歴ディレクトリに存在するものだけを渡す。

WebSocket のテキストフレームは JSON の制御メッセージである。接続直後にサーバは `{"type":"hello","attach":"<id>","chat":"...","activity":"idle|busy|waiting|unknown"}` を送る。CLI の status indicators (端末タイトル) が変わると `{"type":"activity","state":"..."}` を送る。`waiting` (`Waiting for you` / `Waiting for confirmation`) のあいだ画面は送信ボタンを無効化し、選択・確認への誤 Enter を防ぐ。Esc 後の自由テキスト入力ではタイトルが `waiting` のまま残ることがあるため、Esc 押下で送信を再開する（リロード後の持ち越しはしない）。入力は `{"type":"input","data":"..."}`、サイズ変更は `{"type":"resize","cols":80,"rows":24}`、意図的な離脱は `{"type":"close"}` である。仮想端末の出力はバイナリフレームでそのまま送る。プロセス終了は `{"type":"exit","code":0}` で通知する。再接続は `/ws/terminal?project=...&attach=<id>` を使う。同じ `chat` の生存ランタイムがあれば、`attach` なしでもそれに繋ぐ。ページ再読込後は `&replay=1` を付けて scrollback を再生する。

切断中も PTY 出力は読み続け、再接続時に最大 512KiB までまとめて送る。読めないと agent が出力で止まるためである。接続中の出力も同じ上限の scrollback に保持する。ページ再読込など空の端末から戻るときは `/ws/terminal?...&replay=1` で scrollback 全体を再生する。同一画面での一時切断からの再接続では `replay` を付けず、切断中の差分だけを受け取り、端末上の既存表示と重複させない。

画面のテキスト入力は 1 文字ずつ送らない。テキストエリアの改行は改行文字のまま保持し、送信ボタンが本文をまとめて送ったあと、別の入力として Enter (`\r`) を送って確定する。同じ入力に含めた復帰は本文の改行になるためである。Esc・カーソルキーと Enter は、エージェントの画面操作のために個別のボタンから送る。これらのボタンは画面右下に半透明のパネルとして置き、上段は Esc・↑・選択、中段は ←・↓・→、下段は Enter とする。パネル右上の × で一時的に隠せる。隠したあとの再表示検出はパネル非表示時だけ有効にし、移動の少ないタップでのみ戻す。端末への直接入力は無効化し、タップで OS キーボードが開かないようにする。

端末の表示領域では、縦ドラッグでバッファをスクロールして履歴を遡れる。xterm.js 6.0.0 はタッチスクロールが動かないため、ドラッグ量を `scrollToLine` に変換する。scrollback は長い対話を保持できるよう広く取る。末尾より上へ遡っているときは、画面左下に半透明の「末尾にスクロール」ボタンを出す。iOS Safari などでは xterm 上の OS 選択が使えないため、操作パネルの「選択」でバッファを readonly なテキスト面に載せ替え、OS 標準の選択・コピーに任せる。選択面は `<dialog showModal()>` のトップレイヤーで全画面表示し、通常レイアウトへの混入を避ける。表示中は操作パネルを隠す。フォントは iOS のオートズームを避けるため 16px 以上にする。「完了」で通常の端末表示へ戻る。セッション画面と端末画面の右上には、プロジェクト一覧へ戻る通常の `a` リンク「トップ」を置き、別タブで複数セッションを開けるようにする。戻る操作でセッションを抜けるときは確認ダイアログを出さない。

## 常駐

常駐は二段で行う。Windows のタスクスケジューラが WSL distro を起動して維持し、WSL 内の systemd user サービスが `serve` を管理する。ログイン無しでも user サービスを動かすため linger を有効にする。unit テンプレートは `deploy/systemd/`、Windows 側の登録スクリプトは `deploy/windows/` に置く。ログイン前起動がこのマシンで成立しない場合は、同じ keep-alive タスクをログオン時起動へ切り替える。

## 設定

`config.yaml` はプロジェクト一覧、待受アドレス、ユーザーファイル、状態ディレクトリ、`agent` のコマンド、画面ファイルの場所、追加で許可する CIDR、MAC 確認の有無、WebSocket 切断後の猶予 `detach_grace` を持つ。相対パスは設定ファイルのあるディレクトリを基準に解決する。

`users.yaml` はユーザー名、パスワードハッシュ、MAC アドレスのリストを持つ。更新は `user upsert` が行い、一時ファイルへ書いてから置き換える。権限は `0600` とする。

画面の HTML と CSS とスクリプトは `web/static` に置く。xterm のブラウザ向け成果物はリポジトリに同梱せず、`make -f Makefile.agent build` が npm から取得して `web/dist/vendor/` へコピーする。バージョンと整合性は `web/package-lock.json` に記録する。Node.js と Go のバージョンは `mise.toml` で固定する。ビルド時に git の短ハッシュ・未コミット有無・ビルド日時を `web/static/build-info.js`（と json）へ書き、プロジェクト一覧がフロント資産として直接読む。未コミット時は同一ハッシュが続くため日時も表示し、スーパーリロード確認にも使う。index.html 配信時は app.js/css/build-info.js にその版を query として付け、古い画面資産の再利用を避ける。

## 画面の遷移

1. ログイン
2. プロジェクト一覧
3. そのプロジェクトのセッション一覧。ここから新規セッションも始める
4. 仮想端末

未ログインで一覧や端末を開いた場合は、ログインへ戻す。
