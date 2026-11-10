package main

import (
	"os"
	"regexp"
	"strings"
)

// The gate speaks English or Chinese: whatever it prints, logs, asks or
// reports to herdr. The language is the one given with -lang; without it
// HERDR_GATE_LANG (en, zh); without that the system's, English when the
// system's is neither or cannot be told. `-info` names it, so a control panel
// can speak the same one.
var language = detectLanguage()

// languageFlag finds the value of -lang among the arguments, "" when it is
// not there. The flags are described in the gate's language, so this one has
// to be read before they are parsed.
func languageFlag(args []string) string {
	for i, arg := range args {
		if arg == "--" {
			break
		}
		name, value, joined := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		// Without a dash it is another flag's value (-listen :7522).
		if !strings.HasPrefix(arg, "-") || name != "lang" {
			continue
		}
		if joined {
			return value
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// T picks a message in the gate's language. Both versions take the same
// arguments in the same order.
func T(en, zh string) string {
	if language == "zh" {
		return zh
	}
	return en
}

func detectLanguage() string {
	if forced := os.Getenv("HERDR_GATE_LANG"); forced != "" {
		return languageOf(forced)
	}
	return languageOf(systemLocale())
}

// languageOf reads a locale as systems write them (zh_CN.UTF-8, zh-Hans-CN,
// zh): "zh" for Chinese, "en" for everything else.
func languageOf(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if l == "zh" || strings.HasPrefix(l, "zh-") || strings.HasPrefix(l, "zh_") || strings.HasPrefix(l, "zh.") {
		return "zh"
	}
	return "en"
}

// localeFromEnvironment is the POSIX answer: the first of these that is set.
func localeFromEnvironment() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

var languageTag = regexp.MustCompile(`[A-Za-z]{2,3}(?:[-_][A-Za-z0-9]+)*`)

// firstLanguage finds the first language in a list printed the way macOS
// prints its preferred languages: `(\n    "zh-Hans-CN",\n    en\n)`.
func firstLanguage(list string) string {
	return languageTag.FindString(list)
}
