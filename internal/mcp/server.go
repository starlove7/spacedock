package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/starlove7/spacedock/internal/app"
	"github.com/starlove7/spacedock/internal/buildinfo"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/tools"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

type Server struct {
	app      *app.App
	cfg      config.Config
	s        *sdk.Server
	once     sync.Once
	oauth    *OAuthHTTP
	oauthErr error
}

func New(a *app.App, c config.Config) *Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "spacedock", Version: buildinfo.Version}, &sdk.ServerOptions{Instructions: "SpaceDock is a workspace-scoped local development runtime. Open a configured workspace before using workspace-scoped tools."})
	for _, tool := range a.Tools.Tools() {
		t := tool
		s.AddTool(&sdk.Tool{Name: t.Name(), Description: t.Description(), InputSchema: json.RawMessage(t.InputSchema())}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			args := req.Params.Arguments
			if len(bytes.TrimSpace(args)) == 0 {
				args = json.RawMessage(`{}`)
			}
			var obj map[string]any
			if json.Unmarshal(args, &obj) != nil || obj == nil {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "tool arguments must be a JSON object"}
			}
			v, e := a.Tools.Call(ctx, t.Name(), args)
			if e != nil {
				te := tools.Wrap(e)
				b, _ := json.MarshalIndent(te, "", "  ")
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}, StructuredContent: te}, nil
			}
			b, _ := json.MarshalIndent(v, "", "  ")
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}, StructuredContent: v}, nil
		})
	}
	return &Server{app: a, cfg: c, s: s}
}
func (s *Server) HTTPHandler() (http.Handler, error) {
	s.once.Do(func() { s.oauth, s.oauthErr = NewOAuthHTTP(s.cfg) })
	if s.oauthErr != nil {
		return nil, s.oauthErr
	}
	mux := http.NewServeMux()
	s.oauth.RegisterRoutes(mux)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.s }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true})
	mux.Handle("/mcp", s.oauth.Protect(h))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	return s.hosts(mux), nil
}
func (s *Server) hosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Host
		if x, _, e := net.SplitHostPort(h); e == nil {
			h = x
		}
		h = strings.ToLower(strings.Trim(h, "[]"))
		ok := false
		for _, x := range s.cfg.Server.AllowedHosts {
			if x == "*" || strings.EqualFold(x, h) {
				ok = true
			}
		}
		if !ok {
			w.WriteHeader(421)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type readCloser struct{ io.Reader }

func (readCloser) Close() error { return nil }

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }
func (s *Server) ServeStdio(in io.Reader, out io.Writer) error {
	return s.s.Run(context.Background(), &sdk.IOTransport{Reader: readCloser{in}, Writer: writeCloser{out}})
}
func (s *Server) Close() error {
	if s.oauth != nil {
		return s.oauth.Close()
	}
	return nil
}

var _ = fmt.Sprint
