package i18n

import "fmt"

// chinese is the catalogue for 简体中文.
//
// The vocabulary is the one a Chinese terminal already uses: 主机列表 and
// 文件视图 are what the two screens call themselves at the top of their own
// frames, 栏位 is a pane, 光标 is the cursor. Width was the other half of the
// work — a Chinese character is two columns, so a row that was measured at 80
// columns in English is not the same row here, and TestHelpHintsSurviveTheUsualTerminalWidth
// and TestFrameFitsTheTerminal are run in this language as well as the other.
//
// Where a Chinese sentence wants its arguments in a different order from the
// English one, the index is written out: 已从 %[2]s 删除 %[1]s names the file
// before the host, which is how the sentence goes, and the caller passes its two
// arguments in the same order it always did.
var chinese = Messages{
	// The host list.
	HostsCount:        "%d/%d 台主机",
	NoHostsConfig:     "没有找到主机。请在 ~/.ssh/config 里加一个 Host 段。",
	NoHostsAdd:        "没有找到主机。按 A 添加一台。",
	NoHostsMatch:      "没有主机匹配过滤条件。按 esc 清空。",
	FilterPlaceholder: "按名称、主机、用户或标签过滤",
	MoreHosts:         "↓ 还有 %d 台",
	ColumnName:        "名称",
	ColumnUser:        "用户",
	ColumnHost:        "主机",
	ColumnPort:        "端口",
	ColumnTags:        "标签",
	FormTitle:         "新增主机",
	SourceSSHConfig:   "ssh 配置文件",

	DisconnectedFrom:    "已断开 %s",
	AddedTo:             "已添加 %s 到 %s",
	Added:               "已添加 %s",
	DeletedFrom:         "已从 %[2]s 删除 %[1]s",
	Deleted:             "已删除 %s",
	PasswordKeptSuffix:  "（已保存的密码保留）",
	NoTransferService:   "无法传输：没有接入文件服务",
	NoHostServiceAdd:    "无法添加主机：没有接入主机服务",
	NoHostServiceRemove: "无法删除主机：没有接入主机服务",
	HostNotWritable:     "%[1]s 在 %[2]s 里，ohmyssh 不写这个文件；请在那里修改",
	ConfirmDelete:       "删除 %s？ y/n",
	ConfirmDeleteFrom:   "从 %[2]s 删除 %[1]s？ y/n",

	// The key bar.
	HintConnect: "连接",
	HintMove:    "移动",
	HintClear:   "清空",
	HintQuit:    "退出",
	HintAdd:     "添加",
	HintDelete:  "删除",
	HintFiles:   "文件",
	HintHelp:    "帮助",
	HintFilter:  "过滤",
	HintPane:    "栏位",
	HintSend:    "发送",
	HintCopy:    "复制",
	HintFind:    "查找",
	HintReload:  "重读",
	HintBack:    "返回",
	HintKeep:    "保留",
	HintCancel:  "取消",
	HintSave:    "保存",
	HintField:   "字段",

	// The help.
	HelpKeys:         "按键 · %s",
	HelpHostList:     "主机列表",
	HelpFileView:     "文件视图",
	HelpConnect:      "连接到光标所在的主机",
	HelpMove:         "移动光标",
	HelpPageHosts:    "按页翻动主机",
	HelpEndsHosts:    "第一台和最后一台",
	HelpType:         "按名称、主机、用户或标签过滤",
	HelpEsc:          "清空过滤条件；已经为空时退出",
	HelpQuit:         "退出",
	HelpQuitAnywhere: "任何界面、任何位置都能退出",
	HelpAdd:          "添加主机",
	HelpAddWrittenTo: "添加主机，写入 %s",
	HelpDelete:       "删除光标所在的主机，会先询问",
	HelpFiles:        "打开文件视图：U 进本地栏，D 进远端栏",
	HelpTab:          "切换栏位",
	HelpMoveEntries:  "移动光标",
	HelpPageEntries:  "按页翻动条目",
	HelpEndsEntries:  "第一个和最后一个条目",
	HelpEnterEntry:   "进入目录；文件则发送到另一栏",
	HelpCopyEntry:    "整体发送该条目，目录也一样",
	HelpParent:       "返回上一级目录",
	HelpFind:         "过滤拿到键盘的那个栏位",
	HelpReload:       "重新读取该栏位的目录",
	HelpEscFiles:     "取消传输、清空过滤，然后离开视图",
	HelpTheseKeys:    "查看按键功能说明",
	HelpClose:        "关闭",

	// The file view.
	FilesTitle:            "文件 · %s",
	PaneLocal:             "[本地]",
	PaneRemote:            "[远端]",
	PaneConnecting:        "连接中…",
	PaneNoSession:         "无会话",
	PaneLoading:           "读取中…",
	PaneNoMatch:           "无匹配",
	PaneEmpty:             "空目录",
	PaneFilterPlaceholder: "过滤当前栏位",
	StatusCancelling:      "正在取消…",
	TransferRunning:       "已经有一个传输在跑了",
	NoSessionYet:          "还没有会话：仍在连接",
	TransferCancelled:     "已取消 %s %s → %s",

	// The add form.
	FormAlias:            "别名",
	FormHost:             "主机",
	FormUser:             "用户",
	FormPort:             "端口",
	FormTags:             "标签",
	FormAliasPlaceholder: "必填；你输入的那个名字",
	FormHostPlaceholder:  "地址；留空就用别名",
	FormUserPlaceholder:  "登录名；留空就用当前用户",
	FormPortPlaceholder:  "22",
	FormTagsPlaceholder:  "prod, web",

	// Counts and rates.
	Files: func(n int) string {
		// Chinese counts without changing the noun, so both branches say the same
		// thing. It is written out anyway rather than dropped, because the next
		// language to be added here may not be so lucky.
		return fmt.Sprintf("%d 个文件", n)
	},
	ETA: "预计 %s",

	// The command line.
	RootUsage: "带交互式主机浏览器的 SSH 连接管理器",
	RootDescription: `ohmyssh 从你的 OpenSSH 配置里读取主机，并通过 SSH 连接上去。

不带参数运行会以交互方式浏览主机，直接写出主机名则立刻连接：

  ohmyssh                 浏览主机，按 enter 连接
  ohmyssh web1            在 web1 上打开一个 shell
  ohmyssh web1 -- uptime  在 PTY 下执行一条命令
  ohmyssh exec web1 -- df -h   不分配 PTY，执行一条命令
  ohmyssh put web1 ./out/ /opt/app/   把文件复制到 web1

密码可以用管道传入，而不是当作参数写在命令行上，这样它就不会出现在进程
列表和 shell 历史里：

  echo "$PW" | ohmyssh --password-stdin web1 -- uptime

主机密钥校验采用首次使用即信任的方式，记在 ~/.ssh/known_hosts 里：
不认识的主机会被记下，密钥变了的会被拒绝。`,

	ListUsage:       "交互式浏览主机，按 enter 连接",
	ListFilterUsage: "预先填好主机过滤条件",

	ConnectUsage: "打开一个交互式 shell（或者在 PTY 下执行命令）",
	ConnectDescription: `连接到 ssh 配置里的某台主机，并打开一个登录 shell。

跟在后面的命令会在 PTY 下执行，和 ssh -t 一样，所以交互式程序也能正常工作：

  ohmyssh connect web1
  ohmyssh connect web1 -- htop

请在命令前面写上 --，这样它自己的参数不会被 ohmyssh 解析。

密码可以用管道传入，而不是敲进去，这样它就不会出现在本可看到它的进程列表里：

  echo "$PW" | ohmyssh connect --password-stdin web1

不带主机名运行 connect 会打开交互式主机浏览器。`,

	ExecUsage: "在远端主机上执行一条命令，不分配 PTY",
	ExecDescription: `在某台主机上执行一条命令，并把它自己的退出码返回。

输出直接送到 stdout 和 stderr，因此 exec 可以放进管道里：

  ohmyssh exec web1 -- df -h
  ohmyssh exec web1 -- cat /etc/hostname | tr -d '\n'

远端命令的退出码就是 ohmyssh 的退出码。

密码可以用管道传入，而不是敲进去，这样它就不会出现在本可看到它的进程列表里：

  echo "$PW" | ohmyssh exec --password-stdin web1 -- uptime`,

	PutUsage: "把文件或整个目录上传到主机",
	PutDescription: `把本机路径复制到远端，本机路径是目录时递归复制。

进度报告写在 stderr，所以 stdout 始终只有最后那一行摘要：

  ohmyssh put web1 ./deploy.sh /tmp/deploy.sh
  ohmyssh put web1 ./out/ /opt/app/

目录复制的是它的内容，而不是在远端建一个同名的目录：上面第二行会让文件直接
落在 /opt/app 下。远端路径以斜杠结尾表示目录，此时单个文件会保留自己的名字放
进去。已存在的文件会被覆盖。

密码可以用管道传入，而不是敲进去，这样它就不会出现在本可看到它的进程列表里：

  echo "$PW" | ohmyssh put --password-stdin web1 ./out/ /opt/app/`,

	GetUsage: "从主机下载文件或整个目录",
	GetDescription: `把远端路径复制到本机，远端路径是目录时递归复制。

它就是两端对调的 put，规则也一样：

  ohmyssh get web1 /var/log/app.log ./app.log
  ohmyssh get web1 /opt/app/ ./app/

密码可以用管道传入，而不是敲进去，这样它就不会出现在本可看到它的进程列表里：

  echo "$PW" | ohmyssh get --password-stdin web1 /var/log/app.log ./app.log

目录复制的是它的内容，落在本机路径下。本机路径以分隔符结尾表示目录，此时单个
文件会保留自己的名字放进去。已存在的文件会被覆盖。`,

	ScpUsage: "在本机与主机之间复制，两端的写法与 scp 一致",
	ScpDescription: `复制两个路径，其中恰好一个以 host:path 的形式指明主机。

  ohmyssh scp ./deploy.sh web1:/tmp/deploy.sh
  ohmyssh scp web1:/var/log/app.log ./app.log

方向由哪一端指明主机决定，所以 scp 就是 put 和 get，没有什么要判断的。没有冒号
的路径是本机的；冒号后面为空的，比如 web1:，表示主机的主目录。

密码可以用管道传入，而不是敲进去，这样它就不会出现在本可看到它的进程列表里：

  echo "$PW" | ohmyssh scp --password-stdin web1:/var/log/app.log ./app.log`,

	ForgetUsage:    "删除已保存的密码",
	ForgetAllUsage: "忘记所有已保存的密码",
	ForgetDescription: `删除 ohmyssh 为某台主机保存的密码。

连接成功后密码会被保存下来，之后连接就不再询问。forget 就是撤销这一步：

  ohmyssh forget web1        忘记 web1 的密码
  ohmyssh forget --all       忘记所有已保存的密码
  ohmyssh forget             列出有已保存密码的主机

已保存的密码放在一个加密文件里；想看它在哪儿，加上 --log-level debug 运行。`,

	ExecMissing: "exec 需要一个主机和一条命令：ohmyssh exec <host> -- <command>",
	PutMissing:  "put 需要一个主机、一个本机路径和一个远端路径：ohmyssh put <host> <local> <remote>",
	GetMissing:  "get 需要一个主机、一个远端路径和一个本机路径：ohmyssh get <host> <remote> <local>",
	ScpMissing:  "scp 需要一个来源和一个目的：ohmyssh scp <src> <dst>",
	ScpNoHost:   "scp 需要有一端指明主机：%s 和 %s 都是本机路径",
	ScpTwoHosts: "scp 只能在本机与主机之间复制，不能在两台主机之间：%s 和 %s 都指明了主机",

	FlagConfigUsage:         "ssh 配置文件的路径（默认 ~/.ssh/config）",
	FlagKnownHostsUsage:     "known_hosts 的路径（默认 ~/.ssh/known_hosts）",
	FlagLogLevelUsage:       "日志级别：trace、debug、info、warn、error、disabled",
	FlagLogFormatUsage:      "日志格式：console 或 json",
	FlagDebugUsage:          "--log-level debug 的简写",
	FlagInsecureUsage:       "跳过主机密钥校验（不安全：允许被中间人拦截）",
	FlagNoAgentUsage:        "不使用 ssh-agent 认证",
	FlagPasswordUsage:       "用于密码/键盘交互认证的密码（省略时会提示输入）",
	FlagPasswordStdinUsage:  "从标准输入读取一行密码",
	FlagNoPromptUsage:       "绝不提示输入密码，直接失败",
	FlagNoSavePasswordUsage: "不记住验证成功的密码（已保存的仍然照用）",
	FlagTimeoutUsage:        "连接与握手超时",
	FlagLangUsage:           "界面语言：en 或 zh，auto 表示跟随环境变量",
	PasswordBoth:            "--password 和 --password-stdin 只能用其中一个",
	PasswordStdinEmpty:      "--password-stdin 没有从标准输入读到密码",

	ForgetNone:  "没有已保存的密码",
	ForgotOne:   "已忘记 %s 的密码",
	ForgotAll:   "已忘记 %d 个已保存的密码",
	SavedIn:     "已保存的密码在 %s：",
	ForgetHowTo: "\n删除其中一个：ohmyssh forget <主机>",

	AliasTaken:      "别名 %q 已经存在于 %s",
	NotOhmysshsFile: "主机 %q 在 %s 里，ohmyssh 不写这个文件；请在那里删除",
	NoSavedPassword: "%s 没有已保存的密码",
	NoSavedPasswordTransfer: "%s 没有已保存的密码：先连它一次，" +
		"或者在 shell 里用 ohmyssh put 或 get",

	UnsupportedLanguage: "不支持的语言 %q；ohmyssh 有的是 %s",
}
