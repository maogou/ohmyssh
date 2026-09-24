<!-- 两个文件是同一个标记：深色那份把字换成了浏览器在深色主题下用的紫色。
     GitHub 按读者自己的设置二选一，所以这里不是一张图。 -->
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-wordmark-dark.svg">
    <img src="assets/logo-wordmark.svg" alt="ohmyssh" width="400">
  </picture>
</p>

# ohmyssh

<!-- CI 和 Go 版本是从仓库里读的，不是写在这里的，所以它们跟着 workflow 和 go.mod
     走，不会各自漂移。两者都要等仓库推到那个地址之后才会显示。 -->
<p align="center">
  <a href="https://github.com/maogou/ohmyssh/actions/workflows/ci.yml"><img src="https://github.com/maogou/ohmyssh/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/maogou/ohmyssh"><img src="https://pkg.go.dev/badge/github.com/maogou/ohmyssh.svg" alt="Go 文档"></a>
  <a href="https://github.com/maogou/ohmyssh/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/maogou/ohmyssh?logo=go" alt="Go 版本"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="许可证：MIT"></a>
</p>

[English](README.md) | [中文](README_CN.md)

一个带交互式主机浏览器的 SSH 连接管理器。它直接读取你 `~/.ssh/config` 里已有的主机 —— 无需另行配置，也没有第二份需要同步维护的清单 —— 而你在浏览器里添加的主机会写入 `~/.ohmyssh/hosts`，与它一并读取。

## 安装

```sh
go install github.com/maogou/ohmyssh/cmd/ohmyssh@latest
```

需要 Go 1.27.1 或更新，也就是 `go.mod` 里写明的那个版本。更旧的工具链会去取模块要求的那一个，而不是直接失败 —— `GOTOOLCHAIN=auto` 是默认值 —— 代价就是那次下载。

## 用法

```
ohmyssh                      浏览主机，回车连接
ohmyssh web1                 在 web1 上打开一个 shell
ohmyssh web1 -- uptime       在 PTY 下运行一条命令
ohmyssh exec web1 -- df -h   不分配 PTY 运行一条命令
ohmyssh scp ./a.sh web1:/tmp 拷贝文件，主机名写法与 scp 一致
ohmyssh list                 浏览主机
ohmyssh forget web1          删除 web1 保存的密码
```

### 浏览

不带参数运行 `ohmyssh` 会打开主机列表。输入即可按名称、主机地址、用户或标签过滤，用 `↑`/`↓` 移动，按 `enter` 连接。会话会接管整个终端；结束后你回到列表，状态栏里写着本次的结果，可以直接接着连下一台。

屏幕最下面那行按键提示是有额度的：终端一窄，它就从右往左把提示让出去，而先让掉的正是屏幕上已经写着的那几个。按 `?` 可以看到完整的按键表 —— 当前是什么视图，看到的就是那个视图的按键，在主机列表里看到的是列表的，在文件视图里看到的是文件视图的；提示行放不下的绑定都写在这里，`A` 添加的主机写到哪个文件也写在这里。

`U` 和 `D` 会在光标所在主机上打开一个双栏文件视图：左边是本地文件，右边是该主机的主目录。`U` 打开时光标在左栏，`D` 则在右栏，但两者打开的是同一个视图 —— 文件往哪个方向走，取决于光标当时在哪一栏，而不是打开它的那个键。

```
tab          切换栏位        enter   目录：进入 —— 文件：发送
↑ ↓ k j      移动            c       整棵树发送，不论是不是目录
← h          返回上一级      /       过滤当前栏位
pgup pgdown  翻页            r       重新读取当前栏位
g G          首行、末行      esc     停止传输，然后退出
?            完整的按键表
```

光标所在条目会被送到*另一*栏当前所在的目录，并沿用其自身名称，所以一次传输是在两个目录之间行走，而不是凭记忆敲出来的两条路径：文件落进另一栏的目录里，目录也一样，作为它自己的一个目录落下，而不会把内容物摊进目标目录。`c` 是 `enter` 做不到的那一半：它把目录整体发出去，而不是走进它。进度条绘制在列表原来的位置上，`esc` 停止传输，传到哪儿了会像其他信息一样在状态栏里报告。

终端窄于 80 列时会一次只画一栏，而不是把两栏挤到连文件名都读不清；`tab` 把另一栏调到前面。以点开头的名称在两栏中都不显示 —— 两栏是并排对比着看的，两边对一个文件是什么给出不同判断，读起来就像个 bug。要带上 `.env` 和 `.ssh`，就用 `put` 和 `get` 吧，它们没有这种顾虑。

`A` 添加主机，省得为了手写一台主机而离开浏览器去编辑配置文件：五个字段，`tab` 在字段间移动，`enter` 保存，`esc` 取消。只有别名是必填的 —— 空字段不会写进配置块，而是沿用 ssh 自己的默认值 —— 写出的块会追加到 `~/.ohmyssh/hosts`，浏览器会把它和你的 ssh config 一起读取。[添加与删除主机](#添加与删除主机) 一节有详细说明。

`X` 删除光标所在的主机，删除前先询问。询问绘制在状态栏上，列表仍然留在屏幕上 —— 一个确认要做的事，就是让它在问哪台主机一目了然 —— 按 `y` 才会执行，而*任何*其他键（`n`、`esc`、`enter`，随便什么）都不会动这台主机：

```
? delete web2 from ~/.ohmyssh/hosts?  y/n
```

只有 `~/.ohmyssh/hosts` 里的主机才能被删除：也就是你用 `A` 添加的那些。来自 ssh config 的主机会被拒绝，提示信息会点名那个文件，因为 ohmyssh 只读它，从不写它。删除主机会保留它已保存的密码 —— 密码属于某个登录身份（`user@host:port`），另一个别名完全可能共用同一个 —— 所以如果你真正想清除的是密码，那是 `ohmyssh forget <别名>` 的事。[添加与删除主机](#添加与删除主机) 一节有详细说明。

`A`、`U`、`D`、`X` 是大写字母，自有其道理，它们遵循 `q` 的规则：只在过滤器为空时生效。否则，你搜索的第一个以 `a`、`u`、`d` 或 `x` 开头的主机名，开头的那个字母就会被吞掉。`?` 不需要这条规则，过滤器里写着什么它都是绑定：名称、主机地址、用户和标签里都不会有问号，这个字符对搜索毫无用处。文件视图从不询问密码 —— 它独占着终端 —— 所以没有保存密码的主机会被告知先从 shell 连一次。

未在配置里的目标也可以直接用，写法与 ssh 接受的形状一致：

```sh
ohmyssh deploy@10.0.0.5
ohmyssh deploy@10.0.0.5:2222
```

### 命令

| 命令 | 别名 | 用途 |
| --- | --- | --- |
| `list` | `ls`, `browse` | 交互式浏览主机（`--filter` 预填搜索词） |
| `connect` | `ssh` | 打开一个 shell，或在 PTY 下运行命令 |
| `exec` | | 不分配 PTY 运行命令，流式输出 stdout/stderr |
| `put` | | 上传文件或目录树：`put <主机> <本地> <远端>` |
| `get` | | 下载：`get <主机> <远端> <本地>` |
| `scp` | | 双向拷贝，主机写成 `host:path` 的形式 |
| `forget` | | 删除已保存的密码（`--all`，或不带参数时列出） |

`connect` 与 `exec` 的区别在于是否申请伪终端。交互式程序用 `connect`，输出要管道传给别处时用 `exec`：`exec` 不改动 stdout 和 stderr，因此可以参与组合。

远端命令前请加 `--`，这样它的参数就不会被 ohmyssh 解析。

### 传输

`put`、`get` 和 `scp` 通过 SFTP 传输文件，复用的是 ohmyssh 本来就会建立的连接：一台在 `ProxyJump` 后面的主机、一台有已保存密码的主机，或者一台主机密钥你已经信任过的主机，都能直接传输，无需再把这些配置一遍。

```sh
ohmyssh put web1 ./deploy.sh /tmp/deploy.sh   # 主机、本地、远端
ohmyssh get web1 /var/log/app.log ./app.log   # 主机、远端、本地
ohmyssh scp ./deploy.sh web1:/tmp/deploy.sh   # host:path，两个方向都行
```

`scp` 从哪一侧写了主机来判断方向，所以它就是 `put` 或 `get`，没有什么需要你决定。不带冒号的路径是本地的，光秃秃的 `web1:` 是该主机的主目录，而两端都写了主机名会报错而不是随便猜 —— 没有哪一份凭据能用来在两台机器之间传文件。

目录会递归传输，落点规则与 `cp` 和 `rsync` 一致：

| 命令 | 结果 |
| --- | --- |
| `put web1 ./dist/ /opt/app/` | `dist` 的内容进入 `/opt/app` |
| `put web1 ./dist /opt/app` | 同上：进入目标的是目录的*内容*，而不是一个以它命名的目录 |
| `put web1 ./a.txt /opt/app/` | `/opt/app/a.txt` |
| `put web1 ./a.txt /opt/app` | 同上：`/opt/app` 是目录，而文件不能写到目录上 |
| `put web1 ./a.txt /opt/app/renamed.txt` | `/opt/app/renamed.txt`，覆盖写入 |

当目标是目录时，文件沿用自身名称 —— 无论是指明这一点的末尾斜杠，还是目录本来就在那里，`cp` 和 `scp` 也是这么理解的。目标不存在时，它就是正在创建的文件的名称：`put web1 ./a.txt /opt/new.txt` 会写出一个叫 `new.txt` 的文件。

已存在的文件会被覆盖，与 `scp` 一致。中途被停下的传输 —— 浏览器里按 `esc`，命令行上按 ctrl+c —— 已经写出去的那部分会留在远端，因为发出去的字节收不回来。文件在那儿，但不完整；`scp` 的行为也一样，所以续传之前先检查文件是否被截断。

进度绘制在 stderr 上，这样 stdout 保持干净，结果可以管道传到别处。在终端上进度条原地重绘；在其他任何地方 —— 管道、CI 日志 —— 它们会让位给每个文件一行，因为光标上移、重写行这种画面一旦被记录下来就没法读了。

```
  deploy.sh  ████████████████████████░░░░░░░░  83%  12 MB/s  ETA 1s
  3 files    ████████████████░░░░░░░░░░░░░░░░  61%  2/3  4.0 MB / 6.6 MB
✓ put ./dist → /opt/app  3 files  6.6 MB
```

符号链接永远不会在远端被重建。上传时，指向普通文件的链接按它背后的字节拷贝，指向目录的链接则被略过 —— 跟随它会让一个指回上层的链接无限递归下去。下载时，所有链接都被略过：传输量是从列表里统计出来的，而列表里每一项都带着自己的类型，解析它们将付出每个链接一次往返的代价。任何不是普通文件的东西 —— socket、设备、命名管道 —— 两个方向都会被略过，它们没有字节可发。

传输两端都会在任何数据被拷贝之前先统计一遍，这正是总量进度条能有分母的原因，也是路径敲错时能在不移动任何东西的情况下就失败的原因。这也意味着一个非常大的目录会先花一会儿走一遍；下载时这一遍是隔着连接去遍历远端目录树。

### 退出状态

远端的退出状态会成为 ohmyssh 的退出状态，所以下面两种写法都符合预期：

```sh
ohmyssh exec web1 -- systemctl is-active nginx
ohmyssh exec web1 -- failing-command && echo ok
```

### 全局参数

| 参数 | 默认值 | 用途 |
| --- | --- | --- |
| `--config`, `-F` | `~/.ssh/config` | 要读取的配置文件（`OHMYSSH_CONFIG`） |
| `--known-hosts` | `~/.ssh/known_hosts` | known_hosts 文件（`OHMYSSH_KNOWN_HOSTS`） |
| `--password`, `-p` | | 用于密码/键盘交互认证的密码（`OHMYSSH_PASSWORD`） |
| `--password-stdin` | | 从标准输入读取一行密码 |
| `--no-prompt` | | 绝不提示输入密码，直接失败 |
| `--no-save-password` | | 不记住验证成功的密码 |
| `--no-agent` | | 不使用 ssh-agent 认证 |
| `--insecure` | | 跳过主机密钥校验（不安全） |
| `--timeout` | `15s` | 连接与握手超时 |
| `--log-level` | `info` | `trace`、`debug`、`info`、`warn`、`error`、`disabled` |
| `--log-format` | `console` | `console` 或 `json` |
| `--debug`, `-d` | | `--log-level debug` 的简写 |

参数是全局的，所以放在子命令前面或后面都可以。日志输出到 stderr，因此 `exec` 的输出可以安全地管道传递。浏览器是个例外：它开着的时候日志写进 `~/.ohmyssh/ohmyssh.log`，因为这时终端归画面所有，写进去的一行会落在画面中间。不是日志行的那些文字也一样跟着走 —— 服务端的登录 banner、`ProxyCommand` 自己的 stderr —— 否则同一个问题只是换了个来源。

## 它会从你的配置里读什么

解析器遵循 OpenSSH 自己的规则，包括那些容易搞错的部分：

- 同一参数**先出现的值生效**，因此一个具体的 `Host` 块会胜过文件中后面出现的兜底 `Host *`。
- **文件作用域的指令**（出现在任何 `Host` 或 `Match` 之前）相当于全局默认值。
- `Include` 通配、`Match` 块、`~` 展开、用 `=` 作分隔符，以及引号，行为都与 ssh 一致。
- `*` 和 `?` 通配、`Host` 列表中的 `!` 取反，以及 `Host web1 web2` 这样的多别名行，都会按目标逐个解析。

只含通配符的块不会被列为可浏览的主机，因为它们命名的是一个模式，而不是一台机器。

### 标签

`# Tags:` 注释会给紧随其后的块打上标签，标签在浏览器里可以搜索：

```sshconfig
# Tags: prod, web
Host web1
    HostName 10.0.0.1
    User deploy
```

## 添加与删除主机

在浏览器里按 `A` —— 过滤器为空时，与 `U`、`D`、`X` 一样 —— 会打开一个表单，用来记下一台值得留存的主机：

```
alias  必填；你输入的名字
host   地址；为空时用别名
user   登录名；为空时用你的
port   22
tags   prod, web
```

`tab` 或 `↑`/`↓` 在字段间移动，`enter` 保存，`esc` 取消。写入的是一个普通的 `Host` 块，上面有一行 `# Tags:`，语法与你的 ssh config 相同，所以这个文件可以像你保存的任何其他配置一样被读取、编辑和纳入版本控制。添加被拒绝时，表单会带着里面的一切保持打开：一台主机有问题，通常只出在一个字段上，为了改它而把另外四个字段重打一遍，是表单最不该提的要求。

**写到哪儿。** `~/.ohmyssh/hosts`（Windows 上是 `%USERPROFILE%\.ohmyssh\hosts`），文件权限 0600，目录 0700 —— 与保存的密码同一个位置、同一套权限。你的 ssh config 永远不会被写入，文件中已有的内容也原样保留：新块以临时文件的方式追加到末尾，因此写入被中断也不会留下半个块，让文件后面的内容都按着错误的样子被解析。

**怎么读。** 两个文件被当作一个来解析，就好像你的 ssh config 末尾写了一行 `Include ~/.ohmyssh/hosts`。因此你 ssh config 里文件作用域的 `User`、`IdentityFile` 或 `ProxyJump`，会同样作用于这里添加的主机，和作用于你手写的主机完全一样 —— 这正是这类主机开箱即可连接的原因。顺序只在一个方向上重要：两个文件之间先出现的值生效，而你的 ssh config 先被读取，所以 `~/.ohmyssh/hosts` 里的任何内容都无法遮蔽你原本就有的主机。别名已被占用会被拒绝，提示信息会点名它已经在哪个文件里。`--config` 替换的是被读取的 ssh config；这里添加的主机无论如何都会被读取，因为它是你自己的状态，而不属于某一个配置文件。

**它不是什么。** 这些主机是 ohmyssh 的，`ssh web2` 并不认识它们 —— ssh 只读 `~/.ssh/config`，别的什么都不读。想让两边都有的主机应该放进 `~/.ssh/config`，反正你本来也会写在那里；ohmyssh 优先读那个文件，并一如既往地浏览它。

**删除。** `X` 收回一台主机。删的是添加时写的同一个文件、同一个块：命名别名的 `Host` 行、它的指令，以及上方那行 `# Tags:` —— 那行属于它下面的块，否则最后会给下面的主机打上标签。文件中其他内容原样保留 —— 删除是逐行进行的，而不是按解析结果重写整个文件，所以你自己的注释、缩进和空行都会毫发无伤；写入也走与添加相同的临时文件加改名流程，因此中断的删除不会截断文件。

来自 ssh config 的主机会被拒绝而不是删除，提示信息会点名那个文件：ohmyssh 只读它、从不写它，所以在那里按 `y` 唯一能做的只是失败。主机已保存的密码会被保留 —— 它是按 `user@host:port` 保存的，另一个别名可能共用 —— 所以删除主机是一个决定，遗忘它的密码是另一个决定。`ohmyssh forget <别名>` 做的是后一件事，主机被删除时状态栏也会这么说。

## 认证

认证方式按 ssh 的顺序依次尝试：

1. ssh-agent
2. `IdentityFile` 指定的密钥
3. 默认密钥：`id_ed25519`、`id_ecdsa`、`id_rsa`、`id_dsa`
4. 密码与键盘交互：先试已保存的密码，否则提示输入

加密的私钥用 `--password` 提供的密码解锁，或者用认证失败后提示输入的那个，所以 `-p` 也充当密钥口令。无法解锁的密钥会被跳过，而不是中止这次尝试，这样剩下的方式还能轮到自己。

`ProxyJump` 链条和 `ProxyCommand` 都支持，包括跳板机本身也是配置里另一个别名的情况。

### 已保存的密码

密码只需输入一次。当它验证成功就会被保存，此后一直使用，因此第二次连一台主机时什么都不用输：

```
$ ohmyssh web1                # 询问密码，然后保存
$ ohmyssh web1                # 直接连上
```

密码按登录身份（`deploy@10.0.0.1:22`）保存，而不是按别名，因此同一台机器的两个别名共用一条记录；而改动 `User` 或 `Port` 会要求输入新密码，而不是悄悄复用旧的那个。失效的已保存密码会被提示输入所取代 —— 一条过期的记录永远不会把你锁在门外。

管理已保存的内容：

```sh
ohmyssh forget                # 列出有保存密码的主机
ohmyssh forget web1           # 遗忘一个
ohmyssh forget --all          # 全部遗忘
```

`--no-save-password` 关闭本次运行的保存功能，但依然会使用已经保存的。`--password` 仍然有效且优先级更高，这正是你在脚本里想要的。

**在脚本里，请用管道传密码，而不是当作参数。** 参数是公开的：任何能列出机器上进程的东西都能读到命令行，shell 也会把它留在历史记录里。从标准输入进来的则两者皆非。`--password-stdin` 读取一行，并保留其余的输入：

```sh
echo "$PW" | ohmyssh --password-stdin web1 -- uptime
ohmyssh --password-stdin web1 < ~/.secrets/web1
```

这两个参数不能在同一次运行中组合使用，空行会被拒绝而不是拿去尝试。

**存放在哪里。** 密码保存在 `~/.ohmyssh/credentials.json`（Windows 上是 `%USERPROFILE%\.ohmyssh\credentials.json`），文件权限 0600，目录权限 0700 —— 后者在 Windows 上体现为只读属性，仅此而已，所以那里加密就是全部的保护。留在旧的 `~/.config/ohmyssh/` 位置的存储，会在首次被读取时搬到新位置，密码完好无损。

**加密值多少。** 文件用 AES-256-GCM 密封，密钥由你的用户、主机名和操作系统派生。这能让密码避开那些只是读读文件的东西 —— 一份备份、一个同步客户端、一次随手 `cat` —— 但同一台机器、同一个账户可以派生出同样的密钥，所以它不是针对账户失陷的防御。它是带干净接口的混淆，不是密钥保管库。文件复制到另一台机器上解不开；那些密码会显示为不存在，你会被重新询问。

## 主机密钥

未知主机首次使用时被信任，并追加到 `known_hosts`（权限 0600）。密钥*发生变化*的主机会被拒绝 —— 这正是首次使用即信任不再保护你的情形，所以它停下来，而不是询问。`--insecure` 完全关闭校验，并在日志中说明这一点。

## 开发

```sh
make build      # 构建 ./ohmyssh
make test       # go test ./...
make lint       # golangci-lint run ./...
make coverage   # 生成 coverage.html 并打开
```

不用 make 的话：

```sh
go build ./...
go vet ./...
go test ./...
```

测试套件会起一个真正的进程内 SSH 服务器，所以拨号、主机密钥校验、认证、PTY 处理、退出状态传递和 SFTP 传输全都是对着真实协议跑的，而不是 mock。服务器把一个临时目录作为它的 SFTP 根目录，这正是让上传能够通过从文件系统读回文件来断言的原因。密码策略 —— 试哪个密码、何时询问、保存什么 —— 单独放在 `internal/service` 里测试，对着一个只应答拨号、不真正开 socket 的传输层，因此每条规则都被独立断言。

`make lint` 需要 golangci-lint v2：

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

### 目录结构

```
cmd/ohmyssh/           入口
internal/command/      命令树，以及参数 → 选项
internal/service/      编排与密码策略
internal/repository/   拨号、主机查找和存储之上的接口
internal/sshclient/    SSH 本身：会话、PTY、认证、known_hosts、SFTP
```

`command` 只负责把命令行变成选项。`service` 从来看不到 `*cli.Command`。`repository` 把 `sshclient`、`config` 和 `credential` 放到接口后面，这正是密码策略能在没有连接的情况下被测试的原因。

## 许可证

[MIT](LICENSE)。拿去用、拿去改、拿去发布 —— 这份许可证只要求一件事，版权声明跟着一起走；只不给一件事，任何担保。`assets/` 下的文件属于本项目自身，采用同一份许可证。
