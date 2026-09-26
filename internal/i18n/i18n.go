// Package i18n holds every sentence ohmyssh says to whoever is running it, and
// which language those sentences come out in.
//
// The catalogue is a struct rather than a map so that two languages cannot drift:
// english and chinese are values of one type, so a message one of them has and
// the other does not is a field that is still there, empty — which is what
// TestNoMessageIsMissing reads for.
//
// The language is installed once, at the process boundary, and read from the
// renderers. It follows the same shape as the logger in internal/pkg/zlog: a
// process-wide service set up before anything is drawn, because a run has one
// language and the renderers are methods scattered across the package.
package i18n

import (
	"fmt"
	"strings"
)

// Language is one of the languages ohmyssh speaks. The value is the primary
// language subtag, which is also what --lang takes.
type Language string

const (
	English Language = "en"
	Chinese Language = "zh"
)

// Languages are the languages ohmyssh has, in the order they are offered to a
// user who asked for one it does not have.
var Languages = []Language{English, Chinese}

// catalogues is what each language says. They are package-level values rather
// than functions because a catalogue is written once and read many times: M
// hands out the pointer, so a frame does not copy it.
var catalogues = map[Language]*Messages{
	English: &english,
	Chinese: &chinese,
}

// current is the installed catalogue. It starts English so that anything drawn
// before Setup runs is in the language the source is written in.
var current = &english

// M returns the catalogue for the installed language.
func M() *Messages { return current }

// Setup installs a language. A language with no catalogue leaves the installed
// one alone rather than leaving the process with nothing to say.
func Setup(lang Language) {
	if messages, ok := catalogues[lang]; ok {
		current = messages
	}
}

// UnsupportedError reports a language ohmyssh does not have. The value is what
// the user asked for, as they wrote it, because the complaint is written by the
// caller — in whichever language the environment asks for, since the one that
// was named is not one this program has.
type UnsupportedError struct {
	Value string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf(M().UnsupportedLanguage, e.Value, Names())
}

// Resolve decides which language to use.
//
// The flag wins when it names one. "auto" means the flag abstains: the
// environment decides, in the same order it would have decided in with no flag
// at all. That is the way out of a --lang hardcoded in somebody else's wrapper
// script, which is the only way one can reach a run the user did not ask for
// and cannot remove.
//
// Otherwise the first of OHMYSSH_LANG, LC_ALL, LC_MESSAGES and LANG that is set
// decides, which is gettext's order and the reason a user who has not heard of
// ohmyssh still gets their own language.
//
// A value the environment carries but ohmyssh does not have — LANG=fr_FR.UTF-8,
// which is nobody's mistake — is English, quietly: a program that refused to
// start over the locale of the machine it was installed on would be a worse
// program. A value given to the flag is a different thing, and comes back as an
// error for the caller to report. The Language returned beside that error is the
// one the environment asks for, so that the report itself can be read.
//
// getenv is passed in rather than reached for, which is what lets the whole of
// this be tested without setting a variable on the test process.
func Resolve(getenv func(string) string, flagValue string) (Language, error) {
	fromEnvironment := environment(getenv)

	flagValue = strings.TrimSpace(flagValue)
	if flagValue == "" || strings.EqualFold(flagValue, "auto") {
		return fromEnvironment, nil
	}

	lang, ok := parse(flagValue)
	if !ok {
		return fromEnvironment, &UnsupportedError{Value: flagValue}
	}
	return lang, nil
}

// environment is the language the environment asks for: the first variable that
// is set decides, and a value ohmyssh does not have is English.
func environment(getenv func(string) string) Language {
	for _, name := range []string{"OHMYSSH_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			continue
		}
		lang, ok := parse(value)
		if !ok {
			return English
		}
		return lang
	}
	return English
}

// parse reads a language out of a flag or a locale, in any of the shapes those
// come in: "zh", "zh-CN", "zh_CN.UTF-8", "zh-Hans-CN". Only the primary subtag
// is looked at, so every Chinese locale there is reaches the one Chinese
// catalogue this program has — including zh-TW, whose characters are not the
// ones it writes. That is worth knowing but not worth refusing over: a reader of
// Traditional is far better served by Simplified than by English.
func parse(value string) (Language, bool) {
	primary, _, _ := strings.Cut(strings.ToLower(value), ".")
	primary, _, _ = strings.Cut(primary, "@")
	primary, _, _ = strings.Cut(primary, "-")
	primary, _, _ = strings.Cut(primary, "_")

	switch Language(primary) {
	case English:
		return English, true
	case Chinese:
		return Chinese, true
	default:
		return "", false
	}
}

// Names lists the languages as a user would write them on the command line,
// which is what a complaint about an unsupported one is worth ending with.
func Names() string {
	names := make([]string, len(Languages))
	for i, lang := range Languages {
		names[i] = string(lang)
	}
	return strings.Join(names, ", ")
}
