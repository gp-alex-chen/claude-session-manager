package session

import (
	"errors"
	"strings"
)

// ValidateID enforces the identifier contract used by both session discovery
// and terminal launch. IDs are embedded in the current Windows shell command,
// so only ASCII alphanumerics plus a small set of inert separators are valid.
func ValidateID(id string) error {
	if id == "" || len(id) > 256 || strings.HasPrefix(id, "new-") {
		return errors.New("invalid session ID")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return errors.New("invalid session ID")
	}
	return nil
}
