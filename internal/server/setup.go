package server

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"strings"

	"web-cursor-agent/internal/localtls"
)

const appleMobileconfigType = "application/x-apple-aspen-config"

func (s *Server) handleSetupIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	httpsURL := "(HTTPS 未設定)"
	hostLabel := ""
	if s.cfg.TLS.Enabled() {
		port, err := s.cfg.TLSListenPort()
		if err != nil {
			writeClientError(w, r, http.StatusInternalServerError, "サーバ設定に問題があります")
			return
		}
		httpsURL = "https://" + net.JoinHostPort(s.cfg.TLS.PublicHost, port) + "/"
		hostLabel = html.EscapeString(s.cfg.TLS.PublicHost)
	}
	hasCA := s.cfg.TLS.CAFile != ""
	if hasCA {
		if _, err := os.Stat(s.cfg.TLS.CAFile); err != nil {
			hasCA = false
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="ja">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>HTTPS セットアップ - web-cursor-agent</title>
<style>
body { font-family: system-ui, sans-serif; line-height: 1.6; margin: 1.5rem; max-width: 40rem; color: #222; }
h1 { font-size: 1.25rem; }
ol { padding-left: 1.25rem; }
.warning { background: #fff3cd; border: 1px solid #ffe69c; padding: 0.75rem 1rem; border-radius: 6px; }
a.button { display: inline-block; margin: 0.5rem 0 1rem; padding: 0.6rem 1rem; background: #0b5fff; color: #fff; text-decoration: none; border-radius: 6px; }
code { background: #f4f4f4; padding: 0.1rem 0.3rem; border-radius: 3px; word-break: break-all; }
</style>
</head>
<body>
<h1>家庭内 HTTPS のセットアップ</h1>
`)
	if !s.cfg.TLS.Enabled() {
		b.WriteString(`<p>このサーバでは TLS が有効になっていません。<code>tls init</code> で証明書を作り、<code>config.yaml</code> の <code>tls</code> を設定してください。</p></body></html>`)
		_, _ = w.Write([]byte(b.String()))
		return
	}
	if !hasCA {
		b.WriteString(`<p class="warning">CA 証明書 (<code>tls.ca_file</code>) が無いため、構成プロファイルを配信できません。</p>`)
	} else {
		b.WriteString(`<p>通常の利用は HTTP のままで問題ありません。マイクや Web Speech を使うときだけ、次の順で HTTPS を使えるようにしてください。</p>
<ol>
<li><a class="button" href="/setup/ca.mobileconfig">構成プロファイルを入れる</a></li>
<li>設定アプリが開いたら、指示に従ってプロファイルをインストールする（「署名されていません」と出ても家庭内用途では問題ありません）。</li>
<li><strong>設定 → 一般 → 情報 → 証明書信頼設定</strong> で「web-cursor-agent CA」をオンにする。ここを忘れると Safari の警告が続きます。</li>
<li>マイク / Speech 用の URL（ブックマーク推奨）:<br/><code>`)
		b.WriteString(html.EscapeString(httpsURL))
		b.WriteString(`</code></li>
</ol>
<p>ホスト名 <code>`)
		b.WriteString(hostLabel)
		b.WriteString(`</code> がこのサーバの IP に解決されるよう、ルータの DNS などを用意してください。IP アドレスのまま HTTPS を開くと証明書エラーになります。</p>
<p>補助: <a href="/setup/ca.cer">CA 証明書 (ca.cer)</a> をファイルとして保存することもできます。</p>
`)
	}
	b.WriteString(`</body></html>`)
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) handleSetupMobileconfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	der, err := s.caCertDER()
	if err != nil {
		writeClientError(w, r, http.StatusNotFound, "CA 証明書がありません")
		return
	}
	profile, err := localtls.Mobileconfig(der)
	if err != nil {
		writeClientError(w, r, http.StatusInternalServerError, "構成プロファイルを作れませんでした")
		return
	}
	w.Header().Set("Content-Type", appleMobileconfigType)
	w.Header().Set("Content-Disposition", `attachment; filename="web-cursor-agent-ca.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(profile)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(profile)
}

func (s *Server) handleSetupCACer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	der, err := s.caCertDER()
	if err != nil {
		writeClientError(w, r, http.StatusNotFound, "CA 証明書がありません")
		return
	}
	w.Header().Set("Content-Type", "application/pkix-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="web-cursor-agent-ca.cer"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(der)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(der)
}

func (s *Server) caCertDER() ([]byte, error) {
	if s.cfg.TLS.CAFile == "" {
		return nil, fmt.Errorf("tls.ca_file is not set")
	}
	return localtls.LoadCACertificateDER(s.cfg.TLS.CAFile)
}
