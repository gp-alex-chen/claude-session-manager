package provider

import (
	"os"
	"path/filepath"
	"strings"
)

// Supply the resolved native binary's directory to this terminal only. Do
// not mutate the application's environment or discard inherited tool paths.
func codexExecutableEnvironment(bin string, inherited []string) []string {
	if inherited == nil {
		inherited = os.Environ()
	}
	path := ""
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "PATH") {
			path = value
		}
	}
	value := filepath.Dir(bin)
	if path != "" {
		value += string(os.PathListSeparator) + path
	}
	return []string{"PATH=" + value}
}
