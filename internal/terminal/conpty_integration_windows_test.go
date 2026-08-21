//go:build windows && integration

package terminal

// Real ConPTY lifecycle tests belong to the explicit integration suite. They
// are intentionally excluded from ordinary go test because they start child
// processes and require a Windows console environment.
