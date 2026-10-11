package localtls

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// Mobileconfig はルート CA を入れる未署名の iOS 構成プロファイルを返す。
func Mobileconfig(caCertDER []byte) ([]byte, error) {
	if len(caCertDER) == 0 {
		return nil, fmt.Errorf("CA certificate is empty")
	}
	payloadUUID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	profileUUID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	certB64 := wrapBase64(base64.StdEncoding.EncodeToString(caCertDER))
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>web-cursor-agent-ca.cer</string>
			<key>PayloadContent</key>
			<data>
%s
			</data>
			<key>PayloadDescription</key>
			<string>web-cursor-agent の家庭内 HTTPS 用ルート証明書</string>
			<key>PayloadDisplayName</key>
			<string>web-cursor-agent CA</string>
			<key>PayloadIdentifier</key>
			<string>com.web-cursor-agent.ca</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>%s</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>web-cursor-agent</string>
	<key>PayloadIdentifier</key>
	<string>com.web-cursor-agent.profile</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>%s</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`, certB64, payloadUUID, profileUUID)
	return []byte(body), nil
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func wrapBase64(s string) string {
	const width = 64
	var b strings.Builder
	for len(s) > width {
		b.WriteString("\t\t\t\t")
		b.WriteString(s[:width])
		b.WriteByte('\n')
		s = s[width:]
	}
	if s != "" {
		b.WriteString("\t\t\t\t")
		b.WriteString(s)
	}
	return b.String()
}
