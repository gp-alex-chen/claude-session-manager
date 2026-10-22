//go:build !windows

package notify

func findBadgeTarget() badgeTarget                       { return badgeTarget{} }
func applyTaskbarBadge(badgeTarget, taskbarStatus) error { return nil }
