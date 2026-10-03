// Package password は管理コマンドが新しいパスワードを標準入力から受け取る。
package password

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ReadNew は対話端末では確認入力付きで、パイプでは 1 行でパスワードを読む。
func ReadNew(in *os.File, out io.Writer) (string, error) {
	if term.IsTerminal(int(in.Fd())) {
		first, err := readTerminal(in, out, "パスワード: ")
		if err != nil {
			return "", err
		}
		second, err := readTerminal(in, out, "パスワード (確認): ")
		if err != nil {
			return "", err
		}
		if first != second {
			return "", errors.New("passwords do not match")
		}
		if first == "" {
			return "", errors.New("password is empty")
		}
		return first, nil
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("password is empty")
	}
	return password, nil
}

func readTerminal(in *os.File, out io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return "", err
	}
	raw, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(raw), nil
}
