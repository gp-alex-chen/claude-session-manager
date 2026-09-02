//go:build !windows

package state

func projectPathKey(path string) string { return path }

func sameProjectPath(left, right string) bool { return left == right }
