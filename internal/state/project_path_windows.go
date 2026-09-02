//go:build windows

package state

import "strings"

func projectPathKey(path string) string { return strings.ToLower(path) }

func sameProjectPath(left, right string) bool { return strings.EqualFold(left, right) }
