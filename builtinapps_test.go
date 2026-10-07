// CRC: crc-Server.md | Test: test-BuiltinApps.md
package ark

import (
	"reflect"
	"testing"
)

// Test: builtin app names are the top-level dirs under the bundle's apps/ (R3515)
func TestBuiltinAppNames(t *testing.T) {
	files := []string{
		"apps/mcp/app.lua",
		"apps/mcp/viewdefs/MCP.DEFAULT.html",
		"apps/ark/app.lua",
		"apps/README.md", // a file directly in apps/, not an app
		"html/mcp/x.css", // outside apps/
		"lua/main.lua",
	}
	if got, want := builtinAppNames(files), []string{"ark", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("builtinAppNames = %v, want %v", got, want)
	}
	if got := builtinAppNames(nil); got != nil {
		t.Errorf("builtinAppNames(nil) = %v, want nil", got)
	}
}
