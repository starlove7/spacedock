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
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: spacedock init|serve|service|root|version")
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
	case "root":
		if len(os.Args) < 3 {
			log.Fatal("root subcommand required")
		}
		switch os.Args[2] {
		case "add":
			fs := flag.NewFlagSet("root add", flag.ExitOnError)
			cp := fs.String("config", "", "config path")
			rp := fs.String("path", "", "root path")
			ri := fs.String("id", "", "root id")
			rn := fs.String("name", "", "root name")
			_ = fs.Parse(os.Args[3:])
			if fs.NArg() != 0 {
				log.Fatal("root add does not accept positional arguments")
			}
			if *rp == "" {
				log.Fatal("root add requires --path")
			}
			configPath, e := rootConfigPath(*cp)
			if e != nil {
				log.Fatal(e)
			}
			r, e := config.AddRoot(config.RootAddOptions{ConfigPath: configPath, Path: *rp, ID: *ri, Name: *rn})
			if e != nil {
				log.Fatal(e)
			}
			fmt.Printf("added root: id=%s, name=%s, path=%s\n", r.ID, r.Name, r.Path)
		case "list":
			fs := flag.NewFlagSet("root list", flag.ExitOnError)
			cp := fs.String("config", "", "config path")
			_ = fs.Parse(os.Args[3:])
			if fs.NArg() != 0 {
				log.Fatal("root list does not accept positional arguments")
			}
			configPath, e := rootConfigPath(*cp)
			if e != nil {
				log.Fatal(e)
			}
			roots, e := config.ListRoots(configPath)
			if e != nil {
				log.Fatal(e)
			}
			fmt.Println("ID\tNAME\tPATH\tPERMISSIONS")
			for _, r := range roots {
				fmt.Printf("%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Path, strings.Join(r.Permissions, ","))
			}
		case "remove":
			rootID, configPath, e := parseRootRemoveArgs(os.Args[3:])
			if e != nil {
				log.Fatal(e)
			}
			if e = config.RemoveRoot(config.RootRemoveOptions{ConfigPath: configPath, ID: rootID}); e != nil {
				log.Fatal(e)
			}
			fmt.Printf("removed root: %s\n", rootID)
		default:
			log.Fatal("unknown root subcommand")
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: spacedock init|serve|service|root|version")
		os.Exit(2)
	}
}

func rootConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return config.DefaultConfigPath()
}

func parseRootRemoveArgs(args []string) (string, string, error) {
	var positional []string
	var flagID, configPath string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config" || arg == "--id":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", "", fmt.Errorf("%s requires a value", arg)
			}
			value := args[i+1]
			i++
			if arg == "--config" {
				configPath = value
			} else {
				flagID = value
			}
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
			if configPath == "" {
				return "", "", fmt.Errorf("--config requires a value")
			}
		case strings.HasPrefix(arg, "--id="):
			flagID = strings.TrimPrefix(arg, "--id=")
			if flagID == "" {
				return "", "", fmt.Errorf("--id requires a value")
			}
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unknown flag: %s", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) > 1 {
		return "", "", fmt.Errorf("root remove accepts one root id")
	}
	if len(positional) == 1 && flagID != "" {
		return "", "", fmt.Errorf("root id provided both positionally and with --id")
	}
	rootID := flagID
	if len(positional) == 1 {
		rootID = positional[0]
	}
	if rootID == "" {
		return "", "", fmt.Errorf("root remove requires a root id")
	}
	if configPath == "" {
		var e error
		configPath, e = rootConfigPath("")
		if e != nil {
			return "", "", e
		}
	}
	return rootID, configPath, nil
}
