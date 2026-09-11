package acp

type AgentInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}
type InitializeResult struct {
	ProtocolVersion   int            `json:"protocolVersion"`
	AgentCapabilities map[string]any `json:"agentCapabilities"`
	AgentInfo         AgentInfo      `json:"agentInfo"`
	AuthMethods       []any          `json:"authMethods"`
}

const ProtocolVersion = 1
