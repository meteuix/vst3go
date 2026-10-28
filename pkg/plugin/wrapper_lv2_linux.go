package plugin

// LV2 plugins: lv2_linux.c wraps the plugin's CLAP entry as an LV2 plugin
// and lv2ui_linux.c its editor as an X11 UI; the Turtle files come from
// VST3GoLV2WriteBundle (vst3go build -format lv2).

/*
#cgo linux LDFLAGS: -lm
*/
import "C"

// GoLV2Email is the factory e-mail (foaf:mbox of the plugins' maintainer),
// or NULL. The Turtle writer frees it.
//
//export GoLV2Email
func GoLV2Email() *C.char {
	if globalFactoryInfo.Email == "" {
		return nil
	}
	return C.CString(globalFactoryInfo.Email)
}
