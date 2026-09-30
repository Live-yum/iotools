package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/Live-yum/iotools/internal/sample"
	"github.com/Live-yum/iotools/internal/tui"
)

var version = "dev"

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "iotools:", e)
		os.Exit(1)
	}
}
func run(args []string) error {
	flags := flag.NewFlagSet("iotools", flag.ContinueOnError)
	path := flags.String("file", "iotools.yaml", "collection YAML path")
	profile := flags.String("profile", "", "profile name")
	request := flags.String("run", "", "execute a request ID and emit JSON lines")
	allow := flags.Bool("allow-writes", false, "explicitly permit the selected CLI write operation")
	readonly := flags.Bool("read-only", false, "disable all write operations")
	init := flags.Bool("init", false, "create a localhost-only example collection (does not overwrite)")
	validate := flags.Bool("validate", false, "validate collection without connecting")
	showVersion := flags.Bool("version", false, "show version")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *showVersion {
		fmt.Println("iotools", version)
		return nil
	}
	if *init {
		f, e := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(sample.Collection)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		fmt.Println("Created", *path, "• start: iotools --profile local")
		return nil
	}
	c, _, e := config.Load(*path)
	if e != nil {
		return fmt.Errorf("%w (create a collection with --init)", e)
	}
	if *validate {
		fmt.Printf("Valid collection: %d requests, %d profiles\n", len(c.Requests), len(c.Profiles))
		return nil
	}
	if *request == "" {
		return tui.Run(*path, *profile, *readonly)
	}
	for _, r := range c.Requests {
		if r.ID == *request {
			r, e = c.Resolve(r, *profile)
			if e != nil {
				return e
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			enc := json.NewEncoder(os.Stdout)
			return engine.Run(ctx, r, *allow && !*readonly, func(e engine.Event) { _ = enc.Encode(e) })
		}
	}
	return fmt.Errorf("request %q not found", *request)
}
