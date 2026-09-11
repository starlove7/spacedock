package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type rwc struct {
	io.Reader
	io.Writer
}

func (rwc) Close() error { return nil }
func TestTransportIDsRoutingAndNotification(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	tr := NewTransport(rwc{inR, nil}, rwc{nil, outW}, nil, nil)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Scan()
		var q map[string]any
		json.Unmarshal(sc.Bytes(), &q)
		json.NewEncoder(inW).Encode(map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": map[string]any{"ok": true}})
		sc.Scan() // drain the notification; notifications must not receive a response
	}()
	var out map[string]any
	if e := tr.Request(context.Background(), "x", map[string]any{}, &out); e != nil || out["ok"] != true {
		t.Fatalf("response=%v err=%v", out, e)
	}
	if e := tr.Notify("notice", nil); e != nil {
		t.Fatal(e)
	}
	tr.Close()
}
func TestTransportUnknownInboundIDsEchoRawAndMalformedClose(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	tr := NewTransport(rwc{inR, nil}, rwc{nil, outW}, nil, nil)
	go func() { _, _ = io.WriteString(inW, `{"jsonrpc":"2.0","id":"abc","method":"unknown"}`+"\n") }()
	b, err := bufio.NewReader(outR).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	_ = inW.Close()
	select {
	case <-tr.Closed():
	case <-time.After(time.Second):
		t.Fatal("not closed")
	}
	if !strings.Contains(b, `"id":"abc"`) {
		t.Fatalf("echo=%s", b)
	}
	r2 := strings.NewReader(`{"jsonrpc":"1.0","id":1,"result":null}` + "\n")
	tr2 := NewTransport(rwc{r2, nil}, rwc{nil, io.Discard}, nil, nil)
	select {
	case <-tr2.Closed():
	case <-time.After(time.Second):
		t.Fatal("wrong jsonrpc not closed")
	}
}
func TestTransportCancelEmitsNumericRequestID(t *testing.T) {
	inR, inW := io.Pipe()
	var b strings.Builder
	tr := NewTransport(rwc{inR, nil}, rwc{nil, &b}, nil, nil)
	ctx, c := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- tr.Request(ctx, "slow", nil, &map[string]any{}) }()
	time.Sleep(20 * time.Millisecond)
	c()
	if e := <-done; e == nil {
		t.Fatal("cancel did not return")
	}
	if !strings.Contains(b.String(), `$/cancel_request`) || !strings.Contains(b.String(), `"requestId":1`) {
		t.Fatalf("cancel=%s", b.String())
	}
	inW.Close()
	tr.Close()
}
