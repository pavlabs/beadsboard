package ui

import "runtime"

// browserOpener is the platform's "open this in the default handler" command.
// The project ships darwin and linux binaries, so `open` alone would make the
// inbox's o key a silent no-op on linux.
var browserOpener = openerFor(runtime.GOOS)

func openerFor(goos string) string {
	if goos == "darwin" {
		return "open"
	}
	return "xdg-open"
}
