// Package terminal は agent を仮想端末で起動し、入出力と終了を扱う。
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Session は 1 つの対話プロセスと、その仮想端末である。
type Session struct {
	pty       *os.File
	cmd       *exec.Cmd
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	exitCode  int
}

// Start は作業ディレクトリと環境を指定してプロセスを仮想端末で起動する。
func Start(name string, args []string, dir string, env []string, cols, rows int) (*Session, error) {
	if cols < 2 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	// pty が新しいセッションを作る。リーダーのプロセスグループへ終了信号を送るため、ここでは Setpgid しない。
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}
	session := &Session{
		pty:      file,
		cmd:      cmd,
		done:     make(chan struct{}),
		exitCode: -1,
	}
	go func() {
		waitErr := cmd.Wait()
		session.mu.Lock()
		session.exitCode = exitCode(waitErr)
		session.mu.Unlock()
		file.Close()
		close(session.done)
	}()
	return session, nil
}

// Read は仮想端末の出力を読む。プロセス終了後は EOF になる。
func (s *Session) Read(p []byte) (int, error) {
	return s.pty.Read(p)
}

// Write は仮想端末へキー入力を書く。
func (s *Session) Write(p []byte) (int, error) {
	return s.pty.Write(p)
}

// Resize は仮想端末の文字数を変える。
func (s *Session) Resize(cols, rows int) error {
	if cols < 2 || rows < 1 || cols > 500 || rows > 200 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Done はプロセスが終了すると閉じる。
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// ExitCode は終了コードを返す。まだ終了していなければ -1 である。
func (s *Session) ExitCode() int {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.exitCode
	default:
		return -1
	}
}

// Close はプロセスグループを終了させ、終了を待つ。
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		if s.cmd.Process != nil {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
		}
		select {
		case <-s.done:
			return
		case <-time.After(2 * time.Second):
		}
		if s.cmd.Process != nil {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		}
		<-s.done
	})
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
