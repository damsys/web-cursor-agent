package terminal

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestStartEchoesInput(t *testing.T) {
	session, err := Start("/bin/cat", nil, t.TempDir(), os.Environ(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = session.pty.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := session.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf[:n], []byte("hi")) {
		t.Fatalf("output = %q", buf[:n])
	}
}
