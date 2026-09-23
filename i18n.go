package main

// Interface strings, as a struct rather than a map keyed by string.
//
// A map would let a typo compile and then silently render the key to a user. A
// struct makes every language a complete instance that the compiler checks: add a
// field and every translation fails to build until it is filled in, which is
// exactly the moment to notice. The cost is that this file is the only place
// strings live — which is also the point.
//
// Three languages, because those are the ones this tool is actually used in.
// Traditional Chinese would be a fourth instance of the same struct and nothing
// else. English is the fallback for everything else.

type msgs struct {
	// Notification-area menu
	OpenConsole    string
	SettingsItem   string
	ChangePassword string
	DataFolder     string
	Selftest       string
	RestartService string
	StopService    string
	StartService   string
	CheckUpdates   string
	AboutItem      string
	ExitItem       string
	LanguageItem   string
	LangAuto       string

	// Status, shown greyed at the top of the menu and in the tooltip
	Starting      string
	NotResponding string
	NeedsSetup    string
	NoTargets     string
	AllUpFmt      string // one %d: target count
	SomeDownFmt   string // two %d: target count, down count

	// Balloon notifications
	StoppedTitle   string
	StoppedBodyFmt string // one %s: console URL
	BackTitle      string
	DownTitle      string
	DownBodyFmt    string // two %d: down count, target count
	UpTitle        string
	UpBodyFmt      string // one %d: target count

	// About box
	AboutTitle   string
	AboutTagline string
	AboutConsole string
	AboutStatus  string
	AboutData    string
	AboutLicense string
}

var msgEN = msgs{
	OpenConsole:    "Open console",
	SettingsItem:   "Settings, accounts and port...",
	ChangePassword: "Change my password...",
	DataFolder:     "Open data folder",
	Selftest:       "Run clock selftest...",
	RestartService: "Restart service (applies a new port)",
	StopService:    "Stop service",
	StartService:   "Start service",
	CheckUpdates:   "Check for updates",
	AboutItem:      "About SmokeTrail",
	ExitItem:       "Exit SmokeTrail",
	LanguageItem:   "Language",
	LangAuto:       "Automatic (match Windows)",

	Starting:      "starting",
	NotResponding: "service not responding",
	NeedsSetup:    "set the admin password",
	NoTargets:     "no targets yet",
	AllUpFmt:      "%d targets, all up",
	SomeDownFmt:   "%d targets, %d DOWN",

	StoppedTitle:   "SmokeTrail stopped responding",
	StoppedBodyFmt: "The service is not answering on %s.",
	BackTitle:      "SmokeTrail is back",
	DownTitle:      "Link down",
	DownBodyFmt:    "%d of %d targets are not responding.",
	UpTitle:        "Links recovered",
	UpBodyFmt:      "All %d targets are responding again.",

	AboutTitle:   "About SmokeTrail",
	AboutTagline: "Latency distribution and packet loss, kept as a\ndistribution rather than an average.",
	AboutConsole: "Console",
	AboutStatus:  "Status",
	AboutData:    "Data",
	AboutLicense: "Apache License 2.0",
}

var msgJA = msgs{
	OpenConsole:    "コンソールを開く",
	SettingsItem:   "設定・アカウント・ポート...",
	ChangePassword: "パスワードを変更...",
	DataFolder:     "データフォルダーを開く",
	Selftest:       "クロック自己診断を実行...",
	RestartService: "サービスを再起動（ポート変更を適用）",
	StopService:    "サービスを停止",
	StartService:   "サービスを開始",
	CheckUpdates:   "更新を確認",
	AboutItem:      "SmokeTrail について",
	ExitItem:       "SmokeTrail を終了",
	LanguageItem:   "言語",
	LangAuto:       "自動（Windows に合わせる）",

	Starting:      "起動中",
	NotResponding: "サービスが応答していません",
	NeedsSetup:    "管理者パスワードを設定してください",
	NoTargets:     "監視対象がまだありません",
	AllUpFmt:      "%d 件すべて正常",
	SomeDownFmt:   "%d 件中 %d 件ダウン",

	StoppedTitle:   "SmokeTrail が応答しなくなりました",
	StoppedBodyFmt: "サービスが %s で応答していません。",
	BackTitle:      "SmokeTrail が復帰しました",
	DownTitle:      "回線ダウン",
	DownBodyFmt:    "監視対象 %d/%d 件が応答していません。",
	UpTitle:        "回線が復旧しました",
	UpBodyFmt:      "%d 件すべてが復旧しました。",

	AboutTitle:   "SmokeTrail について",
	AboutTagline: "遅延の分布とパケットロスを、平均ではなく\n分布のまま記録します。",
	AboutConsole: "コンソール",
	AboutStatus:  "状態",
	AboutData:    "データ",
	AboutLicense: "Apache License 2.0",
}

var msgZH = msgs{
	OpenConsole:    "打开控制台",
	SettingsItem:   "设置、账户和端口...",
	ChangePassword: "修改我的密码...",
	DataFolder:     "打开数据目录",
	Selftest:       "运行时钟自检...",
	RestartService: "重启服务（应用新端口）",
	StopService:    "停止服务",
	StartService:   "启动服务",
	CheckUpdates:   "检查更新",
	AboutItem:      "关于 SmokeTrail",
	ExitItem:       "退出 SmokeTrail",
	LanguageItem:   "语言",
	LangAuto:       "自动（跟随 Windows）",

	Starting:      "启动中",
	NotResponding: "服务无响应",
	NeedsSetup:    "请设置管理员密码",
	NoTargets:     "还没有监控目标",
	AllUpFmt:      "%d 个目标，全部正常",
	SomeDownFmt:   "%d 个目标，%d 个掉线",

	StoppedTitle:   "SmokeTrail 停止响应",
	StoppedBodyFmt: "服务在 %s 上没有响应。",
	BackTitle:      "SmokeTrail 已恢复",
	DownTitle:      "链路掉线",
	DownBodyFmt:    "%d/%d 个目标无响应。",
	UpTitle:        "链路已恢复",
	UpBodyFmt:      "全部 %d 个目标已恢复正常。",

	AboutTitle:   "关于 SmokeTrail",
	AboutTagline: "延迟分布与丢包 —— 保留分布本身，\n而不是压成一个平均值。",
	AboutConsole: "控制台",
	AboutStatus:  "状态",
	AboutData:    "数据",
	AboutLicense: "Apache License 2.0",
}

// langTag is a BCP-47-ish tag, kept short because only three values exist.
const (
	langAuto = ""
	langEN   = "en"
	langJA   = "ja"
	langZH   = "zh"
)

var langOrder = []string{langEN, langJA, langZH}

var langNames = map[string]string{
	langEN: "English",
	langJA: "日本語",
	langZH: "简体中文",
}

func messages(tag string) msgs {
	switch tag {
	case langJA:
		return msgJA
	case langZH:
		return msgZH
	default:
		return msgEN
	}
}
