package i18n

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// env is an environment to resolve against, written the way os.Getenv would
// answer for it: a variable that is not there is the empty string.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// The locale a machine is set to is the whole point of this package: a user who
// has never heard of ohmyssh and never passes a flag should still get their own
// language, and the variables that said so are read in gettext's order.
func TestResolveFollowsTheEnvironment(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		flag string
		want Language
	}{
		{name: "nothing is set", want: English},
		{name: "LANG alone", vars: map[string]string{"LANG": "zh_CN.UTF-8"}, want: Chinese},
		{name: "a bare language", vars: map[string]string{"LANG": "zh"}, want: Chinese},
		{name: "a script subtag", vars: map[string]string{"LANG": "zh-Hans"}, want: Chinese},
		{name: "a country and a modifier", vars: map[string]string{"LANG": "zh_CN.UTF-8@mod"}, want: Chinese},
		{name: "uppercase", vars: map[string]string{"LANG": "ZH_cn"}, want: Chinese},
		// LC_ALL is the one the user set to override everything else on this
		// machine, so it outranks the per-category variable below it.
		{name: "LC_ALL over LC_MESSAGES", vars: map[string]string{
			"LC_ALL": "zh_CN.UTF-8", "LC_MESSAGES": "en_US.UTF-8", "LANG": "en_US.UTF-8",
		}, want: Chinese},
		{name: "LC_MESSAGES over LANG", vars: map[string]string{
			"LC_MESSAGES": "zh_CN.UTF-8", "LANG": "en_US.UTF-8",
		}, want: Chinese},
		{name: "LC_ALL empty falls through", vars: map[string]string{
			"LC_ALL": "", "LANG": "zh_CN.UTF-8",
		}, want: Chinese},
		{name: "OHMYSSH_LANG outranks every locale", vars: map[string]string{
			"OHMYSSH_LANG": "zh", "LC_ALL": "en_US.UTF-8", "LANG": "en_US.UTF-8",
		}, want: Chinese},
		// LANG=fr_FR.UTF-8 is the locale of the machine, not a mistake by the
		// user, and a program that refused to run in it would be a worse
		// program. English is what there is.
		{name: "a language ohmyssh does not have", vars: map[string]string{"LANG": "fr_FR.UTF-8"}, want: English},
		{name: "a locale that is not a language at all", vars: map[string]string{"LANG": "C"}, want: English},
		// The flag ignores the environment when it names a language...
		{name: "the flag outranks a locale", vars: map[string]string{"LANG": "zh_CN.UTF-8"},
			flag: "en", want: English},
		// ...and abstains when it says auto. The environment then decides in the
		// same order it would have decided in with no flag at all, OHMYSSH_LANG
		// included: auto is for escaping a --lang somebody else's wrapper script
		// passed, not for escaping the environment.
		{name: "auto means the locale decides", vars: map[string]string{"LANG": "zh_CN.UTF-8"},
			flag: "auto", want: Chinese},
		{name: "auto lets OHMYSSH_LANG decide, as it would have anyway", vars: map[string]string{
			"OHMYSSH_LANG": "en", "LANG": "zh_CN.UTF-8",
		}, flag: "auto", want: English},
		{name: "an empty flag is the same as no flag", vars: map[string]string{"LANG": "zh_CN.UTF-8"},
			flag: "  ", want: Chinese},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(env(tt.vars), tt.flag)
			if err != nil {
				t.Fatalf("Resolve(_, %q) = %v, want no error", tt.flag, err)
			}
			if got != tt.want {
				t.Errorf("Resolve(_, %q) = %q, want %q", tt.flag, got, tt.want)
			}
		})
	}
}

// The flag is what the user typed, so it wins — and when it names a language
// ohmyssh does not have, saying so beats silently doing something else.
func TestResolvePrefersTheFlag(t *testing.T) {
	vars := map[string]string{"OHMYSSH_LANG": "zh", "LANG": "zh_CN.UTF-8"}

	for _, value := range []string{"en", "EN", "en_US.UTF-8"} {
		got, err := Resolve(env(vars), value)
		if err != nil {
			t.Fatalf("Resolve(_, %q) = %v, want no error", value, err)
		}
		if got != English {
			t.Errorf("Resolve(_, %q) = %q, want %q despite the environment asking for Chinese",
				value, got, English)
		}
	}

	got, err := Resolve(env(vars), "fr")
	if err == nil {
		t.Fatalf("Resolve(_, \"fr\") = %q, want a complaint about a language ohmyssh does not have", got)
	}
	if got != Chinese {
		// The complaint is written by the caller, and it is worth being able to
		// write it in a language the person asking for French can read.
		t.Errorf("Resolve(_, \"fr\") came back with %q beside the error, want the environment's %q",
			got, Chinese)
	}
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Resolve(_, \"fr\") = %v, want an *UnsupportedError", err)
	}
	if unsupported.Value != "fr" {
		t.Errorf("the complaint says %q, want the value as the user wrote it, %q", unsupported.Value, "fr")
	}
	if !strings.Contains(Names(), "zh") || !strings.Contains(Names(), "en") {
		t.Errorf("Names() = %q, want it to list both languages", Names())
	}
}

// A field left out of a catalogue is not a compile error — the struct is the
// same shape either way, and the zero value is a valid string. This is the only
// place that would notice, so it reads every field of every language.
func TestNoMessageIsMissing(t *testing.T) {
	for _, lang := range Languages {
		messages, ok := catalogues[lang]
		if !ok {
			t.Fatalf("%q is offered to the user but has no catalogue", lang)
		}
		value := reflect.ValueOf(messages).Elem()
		for i := range value.NumField() {
			field := value.Type().Field(i)
			got := value.Field(i)

			switch field.Name {
			case "Files":
				// The one field that is a function rather than a sentence. It
				// has its own test below, since a nil func is what a missing
				// one looks like and every call site would panic on it.
				if got.IsNil() {
					t.Errorf("%s.%s is not implemented", lang, field.Name)
				}
			default:
				if strings.TrimSpace(got.String()) == "" {
					t.Errorf("%s.%s says nothing", lang, field.Name)
				}
			}
		}
	}
}

// A verb missing from a translation does not fail to compile either; it prints
// "%!s(MISSING)" to whoever is reading the screen. So both languages have to
// take the same arguments for the same message — the same numbers, since
// "deleted %s from %s" and "已从 %[2]s 删除 %[1]s" are one message written two
// ways and not two different ones.
func TestEveryLanguageAgreesOnArguments(t *testing.T) {
	want := argumentsIn(t, &english)
	for _, lang := range Languages {
		if lang == English {
			continue
		}
		for name, got := range argumentsIn(t, catalogues[lang]) {
			if !slices.Equal(got, want[name]) {
				t.Errorf("%s.%s takes arguments %v, English takes %v; one of the two will print a formatting error",
					lang, name, got, want[name])
			}
		}
	}
}

// argumentsIn reads the argument numbers out of every message in a catalogue,
// the way fmt would: an implicit verb takes the next argument, and an explicit
// %[2]s names its own and sets where the next implicit one carries on from.
func argumentsIn(t *testing.T, messages *Messages) map[string][]int {
	t.Helper()
	value := reflect.ValueOf(messages).Elem()
	got := make(map[string][]int, value.NumField())
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.Name == "Files" {
			continue // a function, not a sentence; it has its own test
		}
		used := argumentsInMessage(value.Field(i).String())
		// Sorted, because which arguments a message takes is the question and
		// the order it names them in is the translation's own business: "deleted
		// %s from %s" and "已从 %[2]s 删除 %[1]s" are one message in two
		// languages, not two messages.
		slices.Sort(used)
		got[field.Name] = used
	}
	return got
}

func argumentsInMessage(message string) []int {
	var used []int
	next := 1
	for i := 0; i < len(message); i++ {
		if message[i] != '%' {
			continue
		}
		i++
		if i < len(message) && message[i] == '%' {
			continue // a literal percent, which takes no argument
		}
		if index, width, ok := argumentIndex(message[i:]); ok {
			next = index
			i += width
		}
		used = append(used, next)
		next++
	}
	return used
}

// argumentIndex reads the [2] of an explicit %[2]s, and says how many bytes it
// took. Anything else — a plain %s, or the verb of a %[2]s — is not an index.
func argumentIndex(rest string) (index, width int, ok bool) {
	if len(rest) == 0 || rest[0] != '[' {
		return 0, 0, false
	}
	for i := 1; i < len(rest); i++ {
		switch {
		case rest[i] == ']':
			if i == 1 {
				return 0, 0, false // [] is not an index, and fmt would say so
			}
			return index, i + 1, true
		case rest[i] >= '0' && rest[i] <= '9':
			index = index*10 + int(rest[i]-'0')
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// The plural rule belongs to the language, which is why Files is a function at
// all.
func TestFilesCountsInEachLanguage(t *testing.T) {
	for _, tt := range []struct {
		lang       Language
		n          int
		wantSuffix string
	}{
		{English, 1, "1 file"},
		{English, 3, "3 files"},
		{Chinese, 1, "1 个文件"},
		{Chinese, 3, "3 个文件"},
	} {
		if got := catalogues[tt.lang].Files(tt.n); got != tt.wantSuffix {
			t.Errorf("%s: Files(%d) = %q, want %q", tt.lang, tt.n, got, tt.wantSuffix)
		}
	}
}

// Setup is what Run calls, and M is what every renderer reads, so a language
// that was installed has to be the one that comes back.
func TestSetupInstallsTheCatalogue(t *testing.T) {
	t.Cleanup(func() { Setup(English) })

	Setup(Chinese)
	if M() != &chinese {
		t.Error("Setup(Chinese) left M() on another language")
	}
	Setup(English)
	if M() != &english {
		t.Error("Setup(English) did not put the catalogue back")
	}
	// A language with no catalogue is not a reason to end up with nothing to
	// say.
	Setup(Language("fr"))
	if M() != &english {
		t.Error("Setup of a language ohmyssh does not have moved off the installed one")
	}
}
