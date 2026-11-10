package process

import _ "embed"

// PreludeJS is prelude/isolate.mjs, which Node launchers import.
//
//go:embed prelude/isolate.mjs
var PreludeJS []byte

// PreludePython is prelude/isolate.py, which Python launchers import.
//
//go:embed prelude/isolate.py
var PreludePython []byte
