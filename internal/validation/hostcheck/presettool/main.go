// Command presettool is the pkg/preset side of the preset compatibility
// check in scripts/validate_vst3.sh (see hostcheck/presetcheck.cpp):
//
//	presettool write <out.vstpreset> <class-id> <state-file>
//	presettool read <in.vstpreset> <class-id>
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/th-release/vst3go/pkg/preset"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "presettool:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: presettool write <out> <class-id> <state> | read <in> <class-id>")
	}
	classID, err := preset.ParseClassID(args[2])
	if err != nil {
		return err
	}
	switch args[0] {
	case "write":
		if len(args) != 4 {
			return fmt.Errorf("write needs <out> <class-id> <state>")
		}
		state, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		meta := preset.MetaInfo{preset.AttrPlugInName: "presettool"}
		return preset.WriteFile(args[1], &preset.File{ClassID: classID, Component: state, MetaInfo: meta.Encode()})
	case "read":
		f, err := preset.ReadFile(args[1])
		if err != nil {
			return err
		}
		if f.ClassID != classID {
			return fmt.Errorf("class ID %s, want %s", preset.ClassIDString(f.ClassID), args[2])
		}
		if !bytes.HasPrefix(f.Component, []byte("VST3GO")) {
			return fmt.Errorf("component chunk is not a vst3go state (%d bytes)", len(f.Component))
		}
		fmt.Println("read ok")
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}
