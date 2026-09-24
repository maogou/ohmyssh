package command

import (
	"context"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/urfave/cli/v3"
)

// The language has to be decided before the command tree is built, so it is
// read off argv by hand — and the hand has to agree with the framework about
// where a flag's value is and which of several won. These are the cases the
// reading can be wrong about.
func TestLangFlagValueFindsTheFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"a value in the next argument", []string{"ohmyssh", "--lang", "zh"}, "zh"},
		{"a value after an equals sign", []string{"ohmyssh", "--lang=zh"}, "zh"},
		{"the long name", []string{"ohmyssh", "--language", "zh"}, "zh"},
		{"the long name with an equals sign", []string{"ohmyssh", "--language=zh_CN.UTF-8"}, "zh_CN.UTF-8"},
		{"among the other flags", []string{"ohmyssh", "--debug", "--lang", "zh", "--insecure"}, "zh"},
		{"after the subcommand", []string{"ohmyssh", "list", "--lang", "zh"}, "zh"},
		{"after the host", []string{"ohmyssh", "exec", "web1", "--lang", "zh"}, "zh"},
		// The program name is the first argument of what Run is handed, and the
		// framework skips it. It is not a flag and there is nothing to read in
		// it, which is the whole of what this case has to say.
		{"nothing but the program name", []string{"ohmyssh"}, ""},
		// A different flag's value is not this one's, however it is spelled.
		{"no flag at all", []string{"ohmyssh", "list", "--filter", "web"}, ""},
		{"a flag that only starts the same", []string{"ohmyssh", "--language-files", "x"}, ""},
		{"a flag at the very end has no value to read", []string{"ohmyssh", "list", "--lang"}, ""},
		// The bare -- is where this program's arguments end. What follows
		// belongs to the remote command, and a --lang there is not ours to
		// obey: `ohmyssh exec web1 -- --lang zh` runs something on web1.
		{"a --lang belonging to the remote command", []string{"ohmyssh", "exec", "web1", "--", "--lang", "zh"}, ""},
		{"a --lang after a remote command's own flags", []string{"ohmyssh", "exec", "web1", "--", "uptime", "--lang", "zh"}, ""},
		// The framework keeps the last value, so this does too — otherwise a
		// wrapper script's --lang and the user's would be read as different
		// flags, and the tree would be built in one language and parsed in
		// another.
		{"the last one wins", []string{"ohmyssh", "--lang", "en", "--lang", "zh"}, "zh"},
		{"the last one wins across spellings", []string{"ohmyssh", "--language=en", "--lang", "zh"}, "zh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := langFlagValue(tt.args); got != tt.want {
				t.Errorf("langFlagValue(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// The agreement above is an assumption about somebody else's parsing, and it is
// the assumption the whole prescan rests on: if the framework were to keep the
// first value instead, the tree would answer to one --lang while the language
// came from the other.
func TestThePrescanReadsWhatTheFrameworkWould(t *testing.T) {
	var seen string
	cmd := &cli.Command{
		Name: "ohmyssh",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "lang", Aliases: []string{"language"}},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			seen = cmd.String("lang")
			return nil
		},
	}

	// The program name is part of what Run is handed, so it is part of every
	// case here: a prescan that only agrees with the framework on a command
	// line nobody types is no agreement at all.
	for _, args := range [][]string{
		{"ohmyssh", "--lang", "zh"},
		{"ohmyssh", "--lang=zh"},
		{"ohmyssh", "--language", "zh"},
		{"ohmyssh", "--lang", "en", "--lang", "zh"},
		{"ohmyssh", "--language=en", "--lang", "zh"},
	} {
		seen = ""
		if err := cmd.Run(context.Background(), args); err != nil {
			t.Fatalf("running %v: %v", args, err)
		}
		if got := langFlagValue(args); got != seen {
			t.Errorf("for %v the prescan reads %q and the command tree reads %q; the two have to agree",
				args, got, seen)
		}
	}
}

// The language is installed before the tree is built, so that what --help
// prints is what the user asked for. A Usage string is fixed the moment it is
// set, which is the whole reason the flag is prescanned in the first place.
func TestTheCommandTreeIsBuiltInTheInstalledLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.Setup(i18n.English) })
	t.Setenv("HOME", t.TempDir())

	i18n.Setup(i18n.English)
	english := New()

	i18n.Setup(i18n.Chinese)
	chinese := New()

	if english.Usage == chinese.Usage {
		t.Errorf("the tree reads %q in both languages, want the installed one's own words", english.Usage)
	}
	if want := i18n.M().RootUsage; chinese.Usage != want {
		t.Errorf("root Usage = %q, want %q", chinese.Usage, want)
	}
	if chinese.Description == english.Description {
		t.Error("the root description did not follow the language")
	}

	// Every command and every flag, not just the root: --help prints the whole
	// tree, and one English sentence in it is as wrong as all of them.
	names := map[string]bool{}
	for _, sub := range chinese.Commands {
		if sub.Usage == "" || isASCII(sub.Usage) {
			t.Errorf("the %s command still says %q", sub.Name, sub.Usage)
		}
		names[sub.Name] = true
	}
	for _, name := range []string{"list", "connect", "exec", "put", "get", "scp", "forget"} {
		if !names[name] {
			t.Errorf("the tree has no %s command", name)
		}
	}
	for _, flag := range chinese.Flags {
		usage := flagUsage(t, flag)
		if usage == "" || isASCII(usage) {
			t.Errorf("the %s flag still says %q", flag.Names()[0], usage)
		}
	}
}

// isASCII reports whether a string has no non-ASCII rune in it, which for a
// Chinese string means it was never translated. It is a blunt instrument on
// purpose: this test only has to notice a sentence left behind.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// flagUsage reads a flag's Usage without caring which kind of flag it is.
func flagUsage(t *testing.T, flag cli.Flag) string {
	t.Helper()
	if doc, ok := flag.(interface{ GetUsage() string }); ok {
		return doc.GetUsage()
	}
	t.Fatalf("%T does not report its usage", flag)
	return ""
}

// A language this program does not have is a command line that cannot be acted
// on, so it is refused with the status a usage error gets rather than falling
// back to something the user did not ask for. It is also refused before
// anything runs: --lang fr must not connect to anything on its way out.
func TestRunRefusesALanguageItDoesNotHave(t *testing.T) {
	t.Cleanup(func() { i18n.Setup(i18n.English) })

	for _, args := range [][]string{
		{"ohmyssh", "--lang", "fr"},
		{"ohmyssh", "--lang=fr", "--help"},
		{"ohmyssh", "list", "--lang", "fr"},
	} {
		err := run(context.Background(), args)
		if err == nil {
			t.Fatalf("run(%v) = nil, want a complaint", args)
		}

		coder, ok := err.(cli.ExitCoder)
		if !ok {
			t.Fatalf("run(%v) = %v, want an ExitCoder", args, err)
		}
		if got := coder.ExitCode(); got != errno.CodeUsage {
			t.Errorf("run(%v) exited %d, want the usage status %d", args, got, errno.CodeUsage)
		}
		if msg := coder.Error(); !strings.Contains(msg, "fr") {
			t.Errorf("run(%v) complains %q, want it to name the language asked for", args, msg)
		}
	}
}

// The complaint is written in the language the environment asks for rather than
// in the one that was refused — a French LANG is a reason to answer in English,
// not a reason to answer in French, which this program does not have.
func TestTheComplaintIsWrittenInALanguageOhmysshHas(t *testing.T) {
	t.Cleanup(func() { i18n.Setup(i18n.English) })
	t.Setenv("LANG", "zh_CN.UTF-8")
	t.Setenv("OHMYSSH_LANG", "")

	err := run(context.Background(), []string{"ohmyssh", "--lang", "fr"})
	if err == nil {
		t.Fatal("run(--lang fr) = nil, want a complaint")
	}
	if msg := err.Error(); !strings.Contains(msg, "不支持") {
		t.Errorf("run(--lang fr) with LANG=zh_CN.UTF-8 complains %q, want it in Chinese", msg)
	}
}
