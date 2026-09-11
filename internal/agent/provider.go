package agent

import (
	"context"
	"errors"
)

var ErrProviderInterrupted = errors.New("agent provider interrupted")

type providerTurnResult struct {
	Response          string
	ResponseTruncated bool
	ProviderSessionID string
}

type providerSession interface {
	Provider() string
	SessionID() string
	Run(context.Context, string) (providerTurnResult, error)
	Cancel() error
	Close() error
}
