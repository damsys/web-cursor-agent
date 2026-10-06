package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTerminalDetachAndReattach(t *testing.T) {
	srv, ts := testServer(t)
	srv.cfg.DetachGrace = time.Minute
	defer ts.Close()
	defer srv.Close()

	cookie := loginCookie(t, ts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn1 := dialTerminal(t, ctx, ts, cookie, "project=app")
	hello := readJSONMessage(t, ctx, conn1)
	if hello["type"] != "hello" {
		t.Fatalf("hello = %#v", hello)
	}
	attach, _ := hello["attach"].(string)
	if attach == "" {
		t.Fatal("missing attach id")
	}
	if err := conn1.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"keep-alive\n"}`)); err != nil {
		t.Fatal(err)
	}
	waitOutputContains(t, ctx, conn1, "keep-alive")
	_ = conn1.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.runtimes.Lookup(attach, "alice"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := srv.runtimes.Lookup(attach, "alice"); !ok {
		t.Fatal("runtime should remain after websocket close")
	}

	conn2 := dialTerminal(t, ctx, ts, cookie, "project=app&attach="+attach)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	hello2 := readJSONMessage(t, ctx, conn2)
	if hello2["attach"] != attach {
		t.Fatalf("reattach hello = %#v", hello2)
	}
	if err := conn2.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"again\n"}`)); err != nil {
		t.Fatal(err)
	}
	waitOutputContains(t, ctx, conn2, "again")
}

func TestTerminalReplayScrollbackOnReattach(t *testing.T) {
	srv, ts := testServer(t)
	srv.cfg.DetachGrace = time.Minute
	defer ts.Close()
	defer srv.Close()

	cookie := loginCookie(t, ts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn1 := dialTerminal(t, ctx, ts, cookie, "project=app")
	hello := readJSONMessage(t, ctx, conn1)
	attach, _ := hello["attach"].(string)
	if attach == "" {
		t.Fatal("missing attach id")
	}
	if err := conn1.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"before-reload\n"}`)); err != nil {
		t.Fatal(err)
	}
	waitOutputContains(t, ctx, conn1, "before-reload")
	_ = conn1.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.runtimes.Lookup(attach, "alice"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := srv.runtimes.Lookup(attach, "alice"); !ok {
		t.Fatal("runtime should remain after websocket close")
	}

	// ページ再読込相当: replay=1 で切断前の出力も受け取る。
	conn2 := dialTerminal(t, ctx, ts, cookie, "project=app&attach="+attach+"&replay=1")
	defer conn2.Close(websocket.StatusNormalClosure, "")
	hello2 := readJSONMessage(t, ctx, conn2)
	if hello2["attach"] != attach {
		t.Fatalf("reattach hello = %#v", hello2)
	}
	waitOutputContains(t, ctx, conn2, "before-reload")
}

func TestTerminalIdleCloseTerminates(t *testing.T) {
	srv, ts := testServer(t)
	srv.cfg.DetachGrace = time.Minute
	defer ts.Close()
	defer srv.Close()

	cookie := loginCookie(t, ts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := dialTerminal(t, ctx, ts, cookie, "project=app")
	hello := readJSONMessage(t, ctx, conn)
	attach, _ := hello["attach"].(string)
	rt, ok := srv.runtimes.Lookup(attach, "alice")
	if !ok {
		t.Fatal("runtime missing")
	}
	rt.setTitle("Ready | demo")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"close"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.runtimes.Lookup(attach, "alice"); !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idle close should terminate runtime")
}

func TestTerminalBusyCloseDetaches(t *testing.T) {
	srv, ts := testServer(t)
	srv.cfg.DetachGrace = time.Minute
	defer ts.Close()
	defer srv.Close()

	cookie := loginCookie(t, ts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := dialTerminal(t, ctx, ts, cookie, "project=app")
	hello := readJSONMessage(t, ctx, conn)
	attach, _ := hello["attach"].(string)
	rt, ok := srv.runtimes.Lookup(attach, "alice")
	if !ok {
		t.Fatal("runtime missing")
	}
	rt.setTitle("Working… | demo")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"close"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.runtimes.Lookup(attach, "alice"); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("busy close should keep runtime for grace period")
}

func TestTerminalWaitingCloseDetaches(t *testing.T) {
	srv, ts := testServer(t)
	srv.cfg.DetachGrace = time.Minute
	defer ts.Close()
	defer srv.Close()

	cookie := loginCookie(t, ts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := dialTerminal(t, ctx, ts, cookie, "project=app")
	hello := readJSONMessage(t, ctx, conn)
	attach, _ := hello["attach"].(string)
	rt, ok := srv.runtimes.Lookup(attach, "alice")
	if !ok {
		t.Fatal("runtime missing")
	}
	rt.setTitle("Waiting for you | demo")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"close"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.runtimes.Lookup(attach, "alice"); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("waiting close should keep runtime for grace period")
}

func loginCookie(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	res := postJSON(t, ts, "/api/login", `{"username":"alice","password":"secret"}`, "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", res.StatusCode)
	}
	return res.Cookies()[0]
}

func dialTerminal(t *testing.T, ctx context.Context, ts *httptest.Server, cookie *http.Cookie, query string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/terminal?" + query
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{cookie.Name + "=" + cookie.Value},
			"Origin": []string{ts.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func readJSONMessage(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		typ, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	t.Fatal("timed out waiting for json message")
	return nil
}

func waitOutputContains(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) {
	t.Helper()
	var output []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
		if bytes.Contains(output, []byte(want)) {
			return
		}
	}
	t.Fatalf("output = %q", output)
}
