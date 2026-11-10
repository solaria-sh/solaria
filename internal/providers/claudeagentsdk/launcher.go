package claudeagentsdk

import _ "embed"

// The launchers, the same for every agent: everything that differs is in
// the bundle's options.json and isolation.json.
var (
	//go:embed launcher/launch.mjs
	launchJS []byte
	//go:embed launcher/launch.py
	launchPython []byte
)
