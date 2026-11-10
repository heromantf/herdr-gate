//go:build !windows && !darwin

package main

// systemLocale is what the environment says, as on any POSIX system.
func systemLocale() string {
	return localeFromEnvironment()
}
