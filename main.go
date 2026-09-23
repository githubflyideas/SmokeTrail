package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var version = "dev"

// options are everything the command line can say, parsed once and reused by the
// foreground path, the service path and `install` (which bakes them back into the
// service's command line).
type options struct {
	localOnly bool
	days      int
	data      string
	port      int
	logFile   string   // set only on the elevated re-launch; see elevate_windows.go
	rawArgs   []string // as given, for install to replay
}

func main() {
	log.SetFlags(log.LstdFlags)
	args := os.Args[1:]

	// Subcommands come before flag parsing, so `pingping install --port 9000`
	// reads the way a Windows admin expects rather than the way Go's flag package
	// would otherwise insist on.
	verb := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb = args[0]
	}
	switch verb {
	case "help":
		printHelp(os.Stdout)
		return
	case "version":
		fmt.Println("pingping", version)
		return
	case "selftest":
		runSelftest(os.Stdout)
		return
	case "run", "install", "uninstall", "tray":
		args = args[1:]
	case "":
	default:
		fmt.Fprintf(os.Stderr, "pingping: unknown command %q\nRun 'pingping help' for examples.\n", verb)
		os.Exit(2)
	}

	opt, err := parseOptions(args)
	if err != nil {
		if err == flag.ErrHelp {
			printHelp(os.Stdout)
			return
		}
		fmt.Fprintf(os.Stderr, "pingping: %v\nRun 'pingping help' for examples.\n", err)
		os.Exit(2)
	}

	// install/uninstall need Administrator. On Windows this elevates itself rather
	// than telling the operator to go and open a different window.
	if verb == "install" || verb == "uninstall" {
		os.Exit(runServiceVerb(verb, opt))
	}
	// The tray companion to an installed service: a separate process in the
	// logged-in user's session, because a service cannot display UI at all.
	if verb == "tray" {
		os.Exit(runTrayCompanion(opt))
	}

	// Started by the service control manager rather than a person: hand over to the
	// platform's service loop, which calls back into startApp.
	if isService() {
		if err := runAsService(opt); err != nil {
			log.Fatalf("service: %v", err)
		}
		return
	}
	if err := serveForeground(opt); err != nil {
		log.Fatal(err)
	}
}

func parseOptions(args []string) (options, error) {
	var opt options
	opt.rawArgs = append([]string(nil), args...)

	fs := flag.NewFlagSet("pingping", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	localOnly := fs.Bool("localhost", false, "bind 127.0.0.1 only")
	days := fs.Int("days", 0, "days of history to keep (default 300)")
	data := fs.String("data", "", "data directory (default: portable ./data beside the exe, else the system location)")
	port := fs.Int("port", 0, "console port (default 8518)")
	logFile := fs.String("log-file", "", "") // internal: the elevated child writes here
	showVer := fs.Bool("version", false, "print version")

	if err := fs.Parse(args); err != nil {
		return opt, err
	}
	if *showVer {
		fmt.Println("pingping", version)
		os.Exit(0)
	}
	if rest := fs.Args(); len(rest) > 0 {
		return opt, fmt.Errorf("unexpected argument %q", rest[0])
	}
	opt.localOnly, opt.days, opt.data, opt.port = *localOnly, *days, *data, *port
	opt.logFile = *logFile
	return opt, nil
}

// configure turns options into the runtime Config. Credentials are deliberately
// absent: they live in the database, set on first run through the console.
func configure(opt options) (*Config, bool, error) {
	cfg := defaultConfig()
	if opt.days > 0 {
		cfg.RetentionDays = opt.days
	}
	if opt.port < 0 || opt.port > 65535 {
		return nil, false, fmt.Errorf("--port must be 1-65535")
	}
	if opt.port > 0 {
		cfg.Listen = hostOf(cfg.Listen) + ":" + strconv.Itoa(opt.port)
	}
	if opt.localOnly {
		cfg.Listen = "127.0.0.1" + portOf(cfg.Listen)
	}
	dir, portable := resolveDataDir(opt.data)
	cfg.DataDir = dir
	return cfg, portable, nil
}

func serveForeground(opt options) error {
	cfg, portable, err := configure(opt)
	if err != nil {
		return err
	}
	a, err := startApp(cfg, opt.port)
	if err != nil {
		return err
	}
	a.banner(portable)

	// Windows only: a tray icon so a double-clicked copy is not a console window
	// with no other affordance. Closing it from the menu stops the program.
	quit := make(chan struct{})
	var once sync.Once
	startForegroundTray("http://localhost"+portOf(cfg.Listen),
		func() { once.Do(func() { close(quit) }) })

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-quit:
	}
	a.shutdown()
	log.Printf("pingping shut down")
	return nil
}
