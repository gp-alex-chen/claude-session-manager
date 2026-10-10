//go:build !debuglog

package agent

// Ordinary builds, including release builds, do not write diagnostic logs.
const debugLoggingEnabled = false
