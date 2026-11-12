# Herdr Gate

[English](README.md)

一个给自己机器用的小型 SSH 服务。装在要被连的机器上，herdr 的桌面 app 和手机 app
就能把这台机器当普通 SSH 机器来连，不用开系统的 sshd。

它只认公钥，对方发什么命令就按本机系统的方式执行：Mac 和 Linux 用你的登录 shell，
Windows 用 cmd。不提供交互终端、文件传输和端口转发。

你批准的设备能以你的身份在这台机器上执行任意命令，只批准自己的设备。

## 运行

在要被连的机器上打开终端运行，窗口开着它就一直运行：

```sh
herdr-gate                # 监听所有网卡的 7522 端口
herdr-gate -listen :2200  # 换端口
```

启动后会显示本机地址和本机指纹。Windows 第一次运行时防火墙会询问一次，点允许。

Tailscale 分给本机的地址排在最前面，mod 的面板里会标出来。哪个是 Tailscale 的地址，
herdr-gate 是调用 `tailscale` 命令问来的；没装或没登录 Tailscale 时，不标任何地址。

这台机器上要先装好 hc，并且在这个终端里能直接运行 `hc`。

## 批准设备

新设备第一次连接时，窗口里会显示它的来源地址和密钥指纹并询问，输入 `y` 回车批准。
直接回车是拒绝；拒绝过的设备在重启 herdr-gate 之前不会再问。

### 自动批准自己的 Tailscale 设备

如果 Tailscale 确认新设备和本机登录的是同一个账号，herdr-gate 可以不询问直接批准。
默认关闭，在 mod 的面板里打开，或者用命令：

```sh
herdr-gate -auto-approve on    # 关闭用 off
```

正在运行的 herdr-gate 会立即跟着开关变。每来一台新设备，它会调用 `tailscale` 命令
查询连接的两端：连接必须到达本机自己的 Tailscale 地址，并且来自同一账号下的设备。
其余情况照常询问：tailnet 里别人的设备、打了标签的设备，以及经由其他设备的路由转发
过来的连接。你拒绝过的设备在重启 herdr-gate 之前仍然被拒绝。

这样批准的设备和其他设备一样在名单里，带着它在 Tailscale 里的名字，撤销方式也一样。

开关打开后，一台设备只要登录了你的 Tailscale 账号，就能在这台机器上执行命令。

## 从别的设备连它

- 桌面 app：Add machine 里填 `用户名@地址:7522`。电脑第一次连要先确认本机指纹：
  Mac 和 Linux 在终端运行 `hc machine add 用户名@地址:7522`；Windows 先运行一次
  `ssh -p 7522 用户名@地址`。
- 手机 app：添加机器，填地址、端口 7522 和用户名。

用户名必须是运行 herdr-gate 的这个系统账号，不分大小写；启动信息和 `-info` 里都会显示它。
用别的用户名登录会被直接拒绝，不会询问。

手机 app 等待登录的时间比 herdr-gate 的询问短（1.0.5 版约 30 秒）。手机先提示登录超时也没关系：
窗口里的询问还在，批准之后在手机上再连一次就行，不会再问。

手机提示要手动添加公钥时，不要用它给的那条命令：那条命令写的是 `~/.ssh/authorized_keys`，
herdr-gate 不读这个文件。在 herdr-gate 的窗口或面板里批准这台手机；它被拒绝过的话，
把手机显示的公钥作为一行加进 herdr-gate 自己的 `authorized_keys`（见"文件"一节）。

## 在 herdr 的终端里运行

也可以在这台机器的 herdr-app 里开一个终端标签，在里面运行 herdr-gate。关掉 app 窗口后
它继续运行；关掉那个标签页它就停了。

这种情况下，有新设备等待批准时 herdr-gate 会用 herdr 的状态协议（OSC 7501）上报，
回答之后撤回，herdr 会把它当成一个需要你处理的 agent 来提示。

用命令回答时要加 `--force`，因为 herdr 默认不往等待中的程序里打字：

```sh
hc send --force <终端> y
```

## herdr-app 的 mod

`mods/herdr-gate/` 是一个 herdr-app 的 mod，用来在 app 里启动、停止 herdr-gate 和批准设备。
它只调用 `hc` 和 herdr-gate 程序。

mod 启动的 herdr-gate 不占标签页，在后台运行：herdr-app 里关掉标签页会结束里面的终端，
所以不能让它住在标签页里。没有标签页时 herdr 自带的提示不会响，所以由状态栏按钮负责弹通知。

面板里有：

- 是否在运行，以及启动和停止；
- 别的设备该填的连接地址，每个地址一行，可以复制；
- 本机指纹；
- 端口，可以改；
- "随 Herdr 自动启动"开关；
- "自动批准我的 Tailscale 设备"开关；
- 语言；
- 正在使用的 herdr-gate 程序：版本和所在位置；
- 已批准的设备、每台是否在线，以及撤销；
- 日志，只读。

状态栏按钮在 herdr-gate 运行时是绿色，有设备连着时显示在线台数，有设备等待批准时显示提示。

安装：把 `mods/herdr-gate/` 复制到 `~/.config/herdr-app/mods/herdr-gate/`。herdr-gate 程序
放在 mod 的 `bin/` 里，三种方式任选：

- 打开面板。没有程序时面板会提示，并提供"下载"，从最新的发布版本里取本机系统的那一个。
  你不点下载，它不会联网。下载时按钮旁会显示进度和速度，下完之前不要关面板。在 macOS 和
  Windows 上，环境里没有代理变量时，下载会走系统设置里的代理。
- 自己从发布页下载程序，放进 `bin/`。
- 自己编译：`go run build.go` 会把所有系统的程序编译到 `bin/` 里，然后再复制 mod。

不管是哪种方式放进去的，用的都是 `bin/` 里的程序，面板里会显示它的版本和位置。

更新：先停止 herdr-gate，并先删掉旧的 `~/.config/herdr-app/mods/herdr-gate/`，再复制新的。
在 Apple 芯片的 Mac 上，直接覆盖正在运行的程序，新程序一启动就会被系统结束，
要删掉文件重新复制才能恢复。

- 面板：`herdr-app do mod.open --name herdr-gate`。
- 状态栏按钮：在自己的布局里加一个块。通知、自动启动和在线台数都依赖它。

```json
{ "id": "herdr-gate", "type": "block", "size": { "px": 34 },
  "content": { "mod": "herdr-gate", "props": { "role": "launcher", "block": "herdr-gate" } } }
```

## 语言

herdr-gate 和 mod 有英文和中文两种界面，默认跟随系统语言，系统语言两者都不是时用英文。

- 在 mod 的面板里用"语言"选择：跟随系统、English 或中文。面板和状态栏按钮立即切换；
  正在运行的 herdr-gate 自己的日志和批准提示要到下次启动才切换。
- 手动运行时：`herdr-gate -lang zh`（或 `en`）。没有给 `-lang` 时，环境变量
  `HERDR_GATE_LANG` 起同样的作用。

## 文件

都在 `~/.config/herdr-gate/`（可用 `-dir` 或环境变量 `HERDR_GATE_HOME` 改）：

- `authorized_keys`：已批准的设备，一行一台。删掉一行就是撤销那台设备：它不能再登录，
  正在用的连接也会在几秒内被断开。
- `host_key`：本机指纹对应的密钥。删掉它，下次启动会生成新的，所有客户端都要重新确认指纹。
- `connections.json`：当前有谁连着，运行中的 herdr-gate 在每次有设备连上或断开时重写它。
- `auto_approve_tailscale`：自动批准自己的 Tailscale 设备的开关，开着时这个文件存在，
  删掉它就是关闭。

## 查询和撤销

```sh
herdr-gate -info                 # 本机账号、地址、指纹、已批准的设备和当前连接（JSON）
herdr-gate -revoke SHA256:...    # 撤销一台设备，给指纹或 -info 里的 key 都可以
```

这两个命令不需要 herdr-gate 正在运行，mod 的面板用的就是它们。

## 构建和测试

需要 Go 1.26 以上。

```sh
go test ./...
go run build.go   # 编译所有系统的程序，放进 mods/herdr-gate/bin/
go build .        # 只编译本机的，放在当前文件夹
```

`build.go` 会生成 Windows、macOS 和 Linux 的 `herdr-gate-<系统>-<架构>`（Windows 加 `.exe`），
mod 会自动选用本机对应的那个。

发布：改 `main.go` 里的版本号，提交，再推送标签 `v<版本号>`。仓库的工作流会在 Linux、
macOS、Windows 上跑测试、编译程序，然后把它们发布到这个版本下。

## 许可证

[MIT](LICENSE)。程序里还包含 Go 的运行库和几个库，它们有自己的许可：见
[THIRD-PARTY-NOTICES](THIRD-PARTY-NOTICES)。
