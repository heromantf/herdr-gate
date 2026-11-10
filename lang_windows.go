package main

import "golang.org/x/sys/windows"

// systemLocale is the language Windows shows this person its own interface in.
func systemLocale() string {
	if languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME); err == nil && len(languages) > 0 {
		return languages[0]
	}
	return localeFromEnvironment()
}
