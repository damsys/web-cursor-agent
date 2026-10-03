package password

import (
	"bytes"
	"os"
	"testing"
)

func TestReadNewFromPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.Write([]byte("secret\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	got, err := ReadNew(reader, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret" {
		t.Fatalf("password = %q", got)
	}
}
