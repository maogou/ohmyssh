package command

import (
	"strings"

	"github.com/maogou/ohmyssh/internal/i18n"
)

// prescanLanguage decides the language before the command tree exists.
//
// It is a prescan because of a deadlock in urfave/cli: Usage, Description and
// every flag's Usage are strings fixed when the tree is built, and --lang is a
// flag that tree would have to parse. So the flag is read here, by hand, off the
// same argv the framework is about to be given, and the tree is then built in
// the language it named.
//
// The environment is reached through the injected getenv rather than through
// os.Getenv, so that what this decides can be tested without setting a variable
// on the test process.
func prescanLanguage(args []string, getenv func(string) string) (i18n.Language, error) {
	return i18n.Resolve(getenv, langFlagValue(args))
}

// langFlagValue finds the last --lang (or --language) in args and returns what
// it was given, or the empty string when there is none — which Resolve reads as
// "ask the environment".
//
// It stops at a bare --: everything after one belongs to the remote command, so
// the --lang in `ohmyssh exec web1 -- --lang zh` is an argument to something on
// the other end of a connection and none of this program's business.
//
// Last and not first, because urfave/cli keeps the last value too. The two have
// to agree about which --lang won, or a wrapper script's `--lang en` followed by
// the user's `--lang auto` would build the tree in one language and parse the
// flags in the other.
func langFlagValue(args []string) string {
	value := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		if name != "--lang" && name != "--language" {
			continue
		}
		if hasInline {
			value = inline
			continue
		}
		// The value is the next argument. A --lang at the very end has none,
		// which is a command line the framework is about to refuse anyway; there
		// is nothing to read out of it and nothing to add to it.
		if i+1 < len(args) {
			value = args[i+1]
			i++
		}
	}
	return value
}
