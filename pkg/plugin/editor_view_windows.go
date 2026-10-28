//go:build windows && cgo && amd64

package plugin

/*
#cgo windows,amd64 CFLAGS: -I../../include
#cgo windows,amd64 LDFLAGS: -lWebView2Loader
#include "../../include/vst3/vst3_c_api.h"
#include "editor_view.h"
*/
import "C"
