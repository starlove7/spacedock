package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/starlove7/spacedock/internal/acp"
)

type acpProviderSession struct {
	manager     *acp.SessionManager
	workspaceID string
	sessionID   string
	mu          sync.Mutex
	activeRunID string
}

func newACPProviderSession(manager *acp.SessionManager, workspaceID string, snapshot acp.SessionSnapshot) providerSession {
	return &acpProviderSession{manager: manager, workspaceID: workspaceID, sessionID: snapshot.SessionID}
}

func (s *acpProviderSession) Provider() string  { return "acp" }
func (s *acpProviderSession) SessionID() string { return s.sessionID }

func (s *acpProviderSession) Run(ctx context.Context, prompt string) (providerTurnResult, error) {
	started, err := s.manager.StartPrompt(ctx, s.workspaceID, s.sessionID, prompt)
	if err != nil {
		return providerTurnResult{}, err
	}
	s.mu.Lock()
	s.activeRunID = started.RunID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.activeRunID == started.RunID {
			s.activeRunID = ""
		}
		s.mu.Unlock()
	}()

	after := uint64(0)
	events := make([]acp.Event, 0, 32)
	historyTruncated := false
	for {
		page, pageErr := s.manager.PromptEvents(ctx, s.workspaceID, started.RunID, after, 200, 25_000_000_000)
		if pageErr != nil {
			if ctx.Err() != nil {
				_ = s.manager.CancelPrompt(context.Background(), s.workspaceID, s.sessionID, started.RunID)
				return providerTurnResult{}, ctx.Err()
			}
			return providerTurnResult{}, pageErr
		}
		if after == 0 && page.Truncated {
			historyTruncated = true
		}
		events = append(events, page.Events...)
		if len(page.Events) > 0 {
			after = page.Events[len(page.Events)-1].Seq
		}
		if page.Status == acp.RunRunning {
			continue
		}
		response, responseTruncated := acpResponse(events)
		result := providerTurnResult{Response: response, ResponseTruncated: responseTruncated || historyTruncated, ProviderSessionID: s.sessionID}
		switch page.Status {
		case acp.RunCompleted:
			return result, nil
		case acp.RunCancelled:
			return result, context.Canceled
		case acp.RunInterrupted:
			return result, fmt.Errorf("%w: ACP session interrupted", ErrProviderInterrupted)
		case acp.RunFailed:
			if strings.TrimSpace(page.Error) == "" {
				return result, fmt.Errorf("ACP turn failed")
			}
			return result, fmt.Errorf("ACP turn failed: %s", page.Error)
		default:
			return result, fmt.Errorf("unexpected ACP run status: %s", page.Status)
		}
	}
}

func (s *acpProviderSession) Cancel() error {
	s.mu.Lock()
	runID := s.activeRunID
	s.mu.Unlock()
	if runID == "" {
		return nil
	}
	return s.manager.CancelPrompt(context.Background(), s.workspaceID, s.sessionID, runID)
}

func (s *acpProviderSession) Close() error {
	return s.manager.Disconnect(s.workspaceID, s.sessionID)
}

func acpResponse(events []acp.Event) (string, bool) {
	var b strings.Builder
	for _, event := range events {
		if event.Type != "agent_message_chunk" {
			continue
		}
		var update struct {
			Content any `json:"content"`
		}
		if json.Unmarshal(event.Update, &update) != nil {
			continue
		}
		switch value := update.Content.(type) {
		case string:
			b.WriteString(value)
		case map[string]any:
			if text, ok := value["text"].(string); ok {
				b.WriteString(text)
			}
		}
	}
	return truncateResponse(b.String())
}

func truncateResponse(value string) (string, bool) {
	if len([]byte(value)) <= 1<<20 {
		return value, false
	}
	bytes := []byte(value)
	n := 1 << 20
	for n > 0 && !utf8.Valid(bytes[:n]) {
		n--
	}
	return string(bytes[:n]), true
}
