//go:build !standalone

package standalone

import (
	"fmt"
	"os"
)

// Main runs the standalone application. This binary was built without the
// standalone tag, so it only reports how to build one.
func Main() {
	fmt.Fprintln(os.Stderr, "this binary is a plugin; build the standalone application with -tags standalone (scripts/build_standalone.sh)")
	os.Exit(2)
}
