package main

import (
	"os"

	"github.com/unixsuperhero/gmtm/internal/app"
	"github.com/unixsuperhero/gmtm/internal/cli"
)

func main() {
	cli.RunTUI = func(o cli.Options) error {
		return app.Run(app.Options{Server: o.Server, DB: o.DB, Project: o.Project})
	}
	cli.Actions = app.Registry
	os.Exit(cli.Main(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
