//go:build !windows

package notify

func findBadgeTarget() badgeTarget             { return badgeTarget{} }
func applyTaskbarBadge(badgeTarget, int) error { return nil }
