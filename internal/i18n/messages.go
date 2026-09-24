package i18n

// Messages is every sentence ohmyssh says, in one language.
//
// The fields are plain strings and the formatting is fmt's, so a message that
// takes arguments carries the verbs and the call site passes the arguments:
//
//	fmt.Sprintf(i18n.M().DisconnectedFrom, host.Name)
//
// A message whose arguments a language would want in another order uses an
// explicit index — "已从 %[2]s 删除 %[1]s" — which is how a translation keeps its
// own word order without the caller knowing anything about it.
//
// Adding a message means adding a field here and a value to each language. A
// value left out is an empty string rather than a compile error, which is what
// TestNoMessageIsMissing reads for; a verb left out of a translation is what
// TestEveryLanguageAgreesOnVerbs reads for.
//
// What is deliberately not in here: the names of the commands and their flags,
// which the user types; the verbs put and get, which are two of those names; the
// keys in the key bar and the symbols beside them; the syntax in a usage line;
// and the log lines, which are read in a bug report rather than by the person
// running the program and are written to a file while a view owns the terminal.
type Messages struct {
	// -----------------------------------------------------------------------
	// The host list
	// -----------------------------------------------------------------------

	// HostsCount is the counter in the header: how many of the hosts on file are
	// showing.
	HostsCount string
	// NoHostsConfig, NoHostsAdd and NoHostsMatch are the empty list, told apart
	// by why it is empty: no hosts at all, none that this browser can add to, and
	// none left by the filter.
	NoHostsConfig string
	NoHostsAdd    string
	NoHostsMatch  string
	// FilterPlaceholder is the grey text in the filter box, which is also the
	// only place the filter binding is spelled out in full.
	FilterPlaceholder string
	// MoreHosts is the counter under a window of hosts that is cutting the list
	// off.
	MoreHosts string
	// The table's column labels. They are measured to lay the columns out, so a
	// language whose words are a different width lays them out differently and
	// needs nothing else changed.
	ColumnName string
	ColumnUser string
	ColumnHost string
	ColumnPort string
	ColumnTags string
	// FormTitle stands where the count stands, while the form is up.
	FormTitle string
	// SourceSSHConfig names the ssh config in a message about a file the user has
	// to go and edit themselves.
	SourceSSHConfig string

	// The status line.
	DisconnectedFrom string
	AddedTo          string
	Added            string
	DeletedFrom      string
	Deleted          string
	// PasswordKeptSuffix is added to a delete that left a saved password behind.
	// It is a suffix rather than a sentence of its own, so it carries its own
	// punctuation and spacing: a language that brackets with full-width marks
	// writes "（已保存的密码保留）" with nothing in front of it.
	PasswordKeptSuffix  string
	NoTransferService   string
	NoHostServiceAdd    string
	NoHostServiceRemove string
	HostNotWritable     string
	// ConfirmDelete and ConfirmDeleteFrom are the delete question, without and
	// with the file the host is in: two whole sentences rather than one and a
	// suffix, because where the file goes in the sentence is the language's
	// business.
	ConfirmDelete     string
	ConfirmDeleteFrom string

	// -----------------------------------------------------------------------
	// The key bar and the help, which are one set of bindings said twice
	// -----------------------------------------------------------------------

	// The bar's words, short enough to keep the whole bar on one line at 80
	// columns, which is what the help exists to make up for.
	HintConnect string
	HintMove    string
	HintClear   string
	HintQuit    string
	HintAdd     string
	HintDelete  string
	HintFiles   string
	HintHelp    string
	HintFilter  string
	HintPane    string
	HintSend    string
	HintCopy    string
	HintFind    string
	HintReload  string
	HintBack    string
	HintKeep    string
	HintCancel  string
	HintSave    string
	HintField   string

	// The help: a row per binding, saying what the bar has no room for.
	//
	// HelpKeys is the header's name for the screen and takes the view being
	// described, which HelpHostList and HelpFileView name in the words the
	// view's own header uses for itself.
	HelpKeys         string
	HelpHostList     string
	HelpFileView     string
	HelpConnect      string
	HelpMove         string
	HelpPageHosts    string
	HelpEndsHosts    string
	HelpType         string
	HelpEsc          string
	HelpQuit         string
	HelpQuitAnywhere string
	HelpAdd          string
	HelpAddWrittenTo string
	HelpDelete       string
	HelpFiles        string
	HelpTab          string
	HelpMoveEntries  string
	HelpPageEntries  string
	HelpEndsEntries  string
	HelpEnterEntry   string
	HelpCopyEntry    string
	HelpParent       string
	HelpFind         string
	HelpReload       string
	HelpEscFiles     string
	HelpTheseKeys    string
	HelpClose        string

	// -----------------------------------------------------------------------
	// The file view
	// -----------------------------------------------------------------------

	// FilesTitle is the header of the file view, and takes the host name.
	FilesTitle string
	// The two panes, named the way the paths above their contents are labelled.
	PaneLocal  string
	PaneRemote string
	// What a pane says when it has no row to draw, told apart by why.
	PaneConnecting        string
	PaneNoSession         string
	PaneLoading           string
	PaneNoMatch           string
	PaneEmpty             string
	PaneFilterPlaceholder string
	// The status line.
	StatusCancelling  string
	TransferRunning   string
	NoSessionYet      string
	TransferCancelled string

	// -----------------------------------------------------------------------
	// The add form
	// -----------------------------------------------------------------------

	FormAlias string
	FormHost  string
	FormUser  string
	FormPort  string
	FormTags  string
	// The placeholders say what a field is for when it is left empty. The port
	// and tags ones are examples rather than prose — the port a host dials when
	// it names none, and the shape of a tag list — so they read the same in
	// every language.
	FormAliasPlaceholder string
	FormHostPlaceholder  string
	FormUserPlaceholder  string
	FormPortPlaceholder  string
	FormTagsPlaceholder  string

	// -----------------------------------------------------------------------
	// Counts and rates
	// -----------------------------------------------------------------------

	// Files counts files, and takes the count. English needs the noun to change
	// with it and Chinese does not, so the plural rule lives in the language
	// rather than at the call site.
	Files func(n int) string
	// ETA prefixes what is left of a transfer, and takes the duration. The
	// duration itself is written the same way in every language: it reads as a
	// number, and both ends of it are digits.
	ETA string

	// -----------------------------------------------------------------------
	// The command line
	// -----------------------------------------------------------------------

	// The command tree: what a command is for, and the long help under it. The
	// examples in the long help keep the syntax the user types, which is the
	// same in every language.
	RootUsage          string
	RootDescription    string
	ListUsage          string
	ListFilterUsage    string
	ConnectUsage       string
	ConnectDescription string
	ExecUsage          string
	ExecDescription    string
	PutUsage           string
	PutDescription     string
	GetUsage           string
	GetDescription     string
	ScpUsage           string
	ScpDescription     string
	ForgetUsage        string
	ForgetAllUsage     string
	ForgetDescription  string
	// What a command says when it was handed the wrong number of arguments.
	ExecMissing string
	PutMissing  string
	GetMissing  string
	ScpMissing  string
	// ...and when scp was handed two arguments that name no host between them,
	// or that name one at each end. Both take the two arguments as typed.
	ScpNoHost   string
	ScpTwoHosts string

	// The flags: what each one is for.
	FlagConfigUsage         string
	FlagKnownHostsUsage     string
	FlagLogLevelUsage       string
	FlagLogFormatUsage      string
	FlagDebugUsage          string
	FlagInsecureUsage       string
	FlagNoAgentUsage        string
	FlagPasswordUsage       string
	FlagPasswordStdinUsage  string
	FlagNoPromptUsage       string
	FlagNoSavePasswordUsage string
	FlagTimeoutUsage        string
	FlagLangUsage           string
	// PasswordBoth and PasswordStdinEmpty are the two ways a piped password can
	// be asked for wrongly.
	PasswordBoth       string
	PasswordStdinEmpty string

	// What forget prints.
	ForgetNone  string
	ForgotOne   string
	ForgotAll   string
	SavedIn     string
	ForgetHowTo string

	// What the service layer says about a host it will not add or remove, and
	// about a password it does not have. Both front ends quote these: the
	// browser puts them in its status line and the command line prints them
	// after "ohmyssh: ".
	AliasTaken      string
	NotOhmysshsFile string
	NoSavedPassword string
	// NoSavedPasswordTransfer is the same news in the file view, where the way
	// out is different: the browser has no password prompt, so it says to
	// connect once instead of offering to ask.
	NoSavedPasswordTransfer string

	// UnsupportedLanguage is the complaint about a --lang ohmyssh does not have.
	// It takes the value as the user wrote it and the list of the ones there are.
	UnsupportedLanguage string
}
