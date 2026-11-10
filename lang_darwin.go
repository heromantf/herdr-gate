package main

import "os/exec"

// systemLocale is the first of the person's preferred languages in System
// Settings. The environment is the fallback, not the first choice: a program
// started by the system has no LANG, one started from a terminal may have a
// LANG that says something else, and the gate and `-info` must agree.
func systemLocale() string {
	if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
		if first := firstLanguage(string(out)); first != "" {
			return first
		}
	}
	return localeFromEnvironment()
}
