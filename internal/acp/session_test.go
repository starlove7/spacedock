package acp

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

const fakeACPScript = `IFS= read -r initialize_request || exit 0
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"agentInfo":{"name":"fake-agent","title":"Fake Agent","version":"test"},"authMethods":[]}}'
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"session/new"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"remote-1\"}}";;
    *'"method":"session/close"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}";;
    *) :;;
  esac
done`

func newSessionManagerTestFixture(t *testing.T) (*SessionManager, *workspace.Workspace) {
	t.Helper()

	r, err := NewRegistry(config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{
		ID:      "fake",
		Name:    "Fake ACP",
		Command: "/bin/sh",
		Args:    []string{"-c", fakeACPScript},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	return NewSessionManager(r), &workspace.Workspace{ID: "w1", Root: t.TempDir()}
}

func connectTestSession(t *testing.T, m *SessionManager, w *workspace.Workspace) SessionSnapshot {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := m.Connect(ctx, w, ConnectOptions{EndpointID: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestSessionOwnershipUnknown(t *testing.T) {
	r, _ := NewRegistry(config.ACPConfig{})
	m := NewSessionManager(r)
	if _, e := m.Get("w", "missing"); e == nil {
		t.Fatal("unknown session accepted")
	}
}

func TestSessionOwnershipAndDisconnect(t *testing.T) {
	m, w := newSessionManagerTestFixture(t)
	snap := connectTestSession(t, m, w)

	if snap.Initialize.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %d, want %d", snap.Initialize.ProtocolVersion, ProtocolVersion)
	}
	if got, ok := snap.Initialize.AgentCapabilities["loadSession"].(bool); !ok || !got {
		t.Fatalf("agent capabilities = %#v, want loadSession=true", snap.Initialize.AgentCapabilities)
	}
	if snap.Initialize.AgentInfo.Name != "fake-agent" || snap.Initialize.AgentInfo.Title != "Fake Agent" || snap.Initialize.AgentInfo.Version != "test" {
		t.Fatalf("agent info = %+v", snap.Initialize.AgentInfo)
	}
	if len(snap.Initialize.AuthMethods) != 0 {
		t.Fatalf("auth methods = %#v, want empty", snap.Initialize.AuthMethods)
	}

	got, err := m.Get("w1", snap.SessionID)
	if err != nil {
		t.Fatalf("Get(w1, sid): %v", err)
	}
	if !reflect.DeepEqual(got, snap) {
		t.Fatalf("cached snapshot = %#v, want %#v", got, snap)
	}
	if _, err := m.Get("other", snap.SessionID); err == nil {
		t.Fatal("Get(other, sid) succeeded")
	}
	if err := m.Disconnect("other", snap.SessionID); err == nil {
		t.Fatal("Disconnect(other, sid) succeeded")
	}
	if _, err := m.Get("w1", snap.SessionID); err != nil {
		t.Fatalf("Get(w1, sid) after wrong-owner disconnect: %v", err)
	}
	if err := m.Disconnect("w1", snap.SessionID); err != nil {
		t.Fatalf("Disconnect(w1, sid): %v", err)
	}
	if _, err := m.Get("w1", snap.SessionID); err == nil {
		t.Fatal("Get(w1, sid) succeeded after disconnect")
	}
}

func TestSessionCloseWorkspaceIsIdempotent(t *testing.T) {
	m, w := newSessionManagerTestFixture(t)
	snap := connectTestSession(t, m, w)

	m.CloseWorkspace("w1")
	if _, err := m.Get("w1", snap.SessionID); err == nil {
		t.Fatal("Get(w1, sid) succeeded after CloseWorkspace")
	}
	m.CloseWorkspace("w1")
	m.CloseAll()
	m.CloseAll()
}
