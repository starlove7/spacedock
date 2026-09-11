package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/starlove7/spacedock/internal/app"
	"github.com/starlove7/spacedock/internal/buildinfo"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/mcp"
	"github.com/starlove7/spacedock/internal/service"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: spacedock init|serve|service|version")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(buildinfo.Version)
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		cp := fs.String("config", "", "config path")
		rp := fs.String("root", "", "root path")
		ri := fs.String("root-id", "", "root id")
		rn := fs.String("root-name", "", "root name")
		force := fs.Bool("force", false, "overwrite")
		public := fs.String("public-base-url", "", "public MCP base URL")
		_ = fs.Parse(os.Args[2:])
		r, e := config.Init(config.InitOptions{ConfigPath: *cp, RootPath: *rp, RootID: *ri, RootName: *rn, PublicBaseURL: *public, Force: *force})
		if e != nil {
			log.Fatal(e)
		}
		fmt.Printf("initialized: config path=%s, owner token path=%s, root path=%s\n", r.ConfigPath, r.TokenPath, r.RootPath)
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		cp := fs.String("config", "", "config path")
		stdio := fs.Bool("stdio", false, "stdio")
		_ = fs.Parse(os.Args[2:])
		if *cp == "" {
			*cp, _ = config.DefaultConfigPath()
		}
		c, e := config.Load(*cp)
		if e != nil {
			log.Fatal(e)
		}
		a, e := app.New(c)
		if e != nil {
			log.Fatal(e)
		}
		defer a.Close()
		sv := mcp.New(a, c)
		defer sv.Close()
		if *stdio {
			if e := sv.ServeStdio(os.Stdin, os.Stdout); e != nil {
				log.Fatal(e)
			}
			return
		}
		handler, e := sv.HTTPHandler()
		if e != nil {
			log.Fatal(e)
		}
		hs := &http.Server{Addr: c.ListenAddress(), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		serveErr := make(chan error, 1)
		go func() {
			fmt.Fprintf(os.Stderr, "SpaceDock MCP listening locally on http://%s/mcp\nSpaceDock public MCP URL: %s\n", c.ListenAddress(), c.MCPURL())
			if e := hs.ListenAndServe(); e != nil && e != http.ErrServerClosed {
				serveErr <- e
			}
		}()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
		case e := <-serveErr:
			log.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	case "service":
		if len(os.Args) < 3 {
			log.Fatal("service subcommand required")
		}
		var e error
		switch os.Args[2] {
		case "install":
			fs := flag.NewFlagSet("service install", flag.ExitOnError)
			cp := fs.String("config", "", "config path")
			_ = fs.Parse(os.Args[3:])
			if *cp == "" {
				*cp, _ = config.DefaultConfigPath()
			}
			e = service.Install(*cp)
		case "start":
			e = service.Start()
		case "stop":
			e = service.Stop()
		case "restart":
			e = service.Restart()
		case "status":
			e = service.Status(os.Stdout)
		case "uninstall":
			e = service.Uninstall()
		default:
			e = fmt.Errorf("unknown service subcommand")
		}
		if e != nil {
			log.Fatal(e)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: spacedock init|serve|service|version")
		os.Exit(2)
	}
}
