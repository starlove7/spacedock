package app

import (
	"github.com/starlove7/spacedock/internal/acp"
	"github.com/starlove7/spacedock/internal/agent"
	"github.com/starlove7/spacedock/internal/command"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/filesystem"
	"github.com/starlove7/spacedock/internal/git"
	"github.com/starlove7/spacedock/internal/recall"
	"github.com/starlove7/spacedock/internal/tools"
	"github.com/starlove7/spacedock/internal/workspace"
)

type App struct {
	Config     config.Config
	Workspaces *workspace.Manager
	Commands   *command.Manager
	ACP        *acp.SessionManager
	Agents     *agent.Manager
	Tools      *tools.Registry
}

func New(c config.Config) (*App, error) {
	r, e := recall.NewManager(c.StateDir)
	if e != nil {
		return nil, e
	}
	w, e := workspace.NewManager(c, r)
	if e != nil {
		return nil, e
	}
	fs, e := filesystem.NewService(c.Security.SensitivePaths.AdditionalPatterns)
	if e != nil {
		return nil, e
	}
	cm := command.NewManager()
	gs := git.NewService()
	_ = fs
	_ = gs
	ar, e := acp.NewRegistry(c.ACP)
	if e != nil {
		return nil, e
	}
	am := acp.NewSessionManager(ar)
	ag := agent.NewManager(c, am)
	w.AddCloseHook(ag)
	w.AddCloseHook(am)
	w.AddCloseHook(cm)
	tr := tools.NewRegistry()
	for _, t := range tools.WorkspaceTools(w) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	for _, t := range tools.FileTools(w, fs) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	for _, t := range tools.CommandTools(w, cm) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	for _, t := range tools.GitTools(w, gs) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	for _, t := range tools.RecallTools(w, r) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	for _, t := range tools.ACPTools(w, am, ar) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	for _, t := range tools.AgentTools(w, ag) {
		if e := tr.Register(t); e != nil {
			return nil, e
		}
	}
	return &App{Config: c, Workspaces: w, Commands: cm, ACP: am, Agents: ag, Tools: tr}, nil
}
func (a *App) Close() {
	if a == nil {
		return
	}
	a.Workspaces.Shutdown()
	a.Agents.CloseAll()
	a.ACP.CloseAll()
	a.Commands.CloseAll()
}
