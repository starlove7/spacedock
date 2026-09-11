package acp

import (
	"context"
	"strings"
	"testing"
)

func TestStartClientInitializesFromAbsoluteShellEndpointAndClosesIdempotently(t *testing.T) {
	const response = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"agentInfo":{"name":"fake-agent","title":"Fake Agent","version":"test"},"authMethods":[{"id":"none","name":"No auth"}]}}`
	script := "IFS= read -r request || exit 1\nprintf '%s\\n' '" + response + "'\nwhile IFS= read -r request; do :; done"

	c, err := StartClient(context.Background(), Endpoint{Command: "/bin/sh", Args: []string{"-c", script}}, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	got := c.Initialize()
	if got.ProtocolVersion != 1 {
		t.Fatalf("protocol version=%d", got.ProtocolVersion)
	}
	if got.AgentCapabilities["loadSession"] != true {
		t.Fatalf("agent capabilities=%v", got.AgentCapabilities)
	}
	if got.AgentInfo.Name != "fake-agent" || got.AgentInfo.Title != "Fake Agent" || got.AgentInfo.Version != "test" {
		t.Fatalf("agent info=%+v", got.AgentInfo)
	}
	if len(got.AuthMethods) != 1 {
		t.Fatalf("auth methods=%v", got.AuthMethods)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestStartClientMissingEnvBeforeStart(t *testing.T) {
	_, e := StartClient(context.Background(), Endpoint{Command: "/bin/true", EnvFrom: map[string]string{"SECRET": "SPACEDOCK_MISSING_TEST"}}, t.TempDir(), nil, nil)
	if e == nil || !strings.Contains(e.Error(), "missing environment variable") {
		t.Fatalf("err=%v", e)
	}
}
func TestStderrTailBounded(t *testing.T) {
	s := &stderrTail{}
	s.Write([]byte(strings.Repeat("x", 70<<10)))
	if len(s.String()) != 64<<10 {
		t.Fatalf("tail=%d", len(s.String()))
	}
}
