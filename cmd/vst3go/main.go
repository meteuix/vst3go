// Command vst3go creates and builds vst3go plugins.
//
//	vst3go new [flags] <dir> <Name>        scaffold a plugin module
//	vst3go build [flags] <package> <Name>  build VST3, CLAP, Audio Unit, LV2 and app bundles
//
// Add it to a plugin module as a tool (go get -tool
// github.com/meteuix/vst3go/v2/cmd/vst3go) and run it with go tool vst3go;
// vst3go new does that for the modules it creates.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const usage = `usage:
  vst3go new [flags] <dir> <Name>        scaffold a plugin module (vst3go new -h)
  vst3go build [flags] <package> <Name>  build plugin bundles (vst3go build -h)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "vst3go:", err)
		}
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return flag.ErrHelp
	}
	switch args[0] {
	case "new":
		return runNew(args[1:])
	case "build":
		return runBuild(args[1:])
	case "-h", "-help", "--help", "help":
		fmt.Fprint(os.Stderr, usage)
		return flag.ErrHelp
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}
