package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// コマンド出力先
var cliOut io.Writer = os.Stdout

// 危険操作のdpCode — -forceなし拒否
var riskyCodes = map[string]bool{"empty": true, "factory_reset": true, "ball_reset": true, "filter_reset": true, "desiccant_reset": true}

// 位置によらず-name/-idフラグを抽出
func pullDeviceFlags(args []string) ([]string, map[string]string) {
	devFlags := map[string]string{}
	out := []string{}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "-name" || a == "--name" || a == "-id" || a == "--id" {
			key := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
			if i+1 < len(args) {
				devFlags[key] = args[i+1]
				i += 2
				continue
			}
		}
		out = append(out, a)
		i++
	}
	return out, devFlags
}

func usage() {
	fmt.Fprintln(cliOut, "usage: petsnowy-cli [flags] <command> [--name X | --id ID]")
	fmt.Fprintln(cliOut, "       petsnowy-cli                  (interactive mode)")
	fmt.Fprintln(cliOut, "       petsnowy-cli --help           (show this help)")
	fmt.Fprintln(cliOut, "commands:")
	fmt.Fprintln(cliOut, "  auth login [-email X -password Y]  login and save session (interactive if omitted)")
	fmt.Fprintln(cliOut, "  auth status                       show session status")
	fmt.Fprintln(cliOut, "  auth logout                       delete saved session")
	fmt.Fprintln(cliOut, "  time                              server time (no login)")
	fmt.Fprintln(cliOut, "  devices                           device list (dp names resolved)")
	fmt.Fprintln(cliOut, "  state --name X|--id ID            device dp state")
	fmt.Fprintln(cliOut, "  model --name X|--id ID            dpCode/dpID table")
	fmt.Fprintln(cliOut, "  op --name X <dpCode> <value>      operate by dpCode")
	fmt.Fprintln(cliOut, "  clean --name X|--id ID            manual clean")
	fmt.Fprintln(cliOut, "  feed --name X [n]                 manual feed (default 1)")
	fmt.Fprintln(cliOut, "  light --name X on|off             light switch")
	fmt.Fprintln(cliOut, "  sleep --name X on|off             sleep mode")
	fmt.Fprintln(cliOut, "  send --id ID <dpsJson>            raw dp send")
	fmt.Fprintln(cliOut, "flags:")
	fmt.Fprintln(cliOut, "  -email/-password/-countryCode     login credentials")
	fmt.Fprintln(cliOut, "  -force                            allow risky ops (empty/factory_reset/ball_reset)")
	fmt.Fprintln(cliOut, "interactive mode: help / quit")
}

// 1コマンド実行: argsはflags+位置引数, 戻り値は終了コード
func run(args []string, c *Client) int {
	fs := flag.NewFlagSet("petsnowy-cli", flag.ContinueOnError)
	email := fs.String("email", "", "Tuya account email")
	password := fs.String("password", "", "account password")
	countryCode := fs.String("countryCode", "1", "country code")
	force := fs.Bool("force", false, "allow risky ops")

	rest, devFlags := pullDeviceFlags(args)
	if err := fs.Parse(rest); err != nil {
		if err == flag.ErrHelp {
			usage()
			return 0
		}
		return 2
	}
	if fs.NArg() < 1 {
		usage()
		return 2
	}
	cmd := fs.Arg(0)

	fail := func(format string, a ...interface{}) int {
		fmt.Fprintf(cliOut, "error: "+format+"\n", a...)
		return 1
	}

	needLogin := func() bool {
		// 保存済みセッションを優先
		if s, err := loadSession(); err == nil {
			if sd, err := c.sessionValid(s.SID); err == nil {
				c.SID = sd.SID
				if err := c.loadModels(); err != nil {
					fmt.Fprintln(cliOut, "warn: model load failed:", err)
				}
				return true
			}
			fmt.Fprintln(cliOut, "warn: saved session expired, re-login required")
			deleteSession()
		}
		if *email == "" || *password == "" {
			fmt.Fprintln(cliOut, "error: not logged in. run: auth login")
			return false
		}
		info, err := c.login(*email, *password, *countryCode)
		if err != nil {
			fmt.Fprintln(cliOut, "error:", err)
			return false
		}
		saveSession(sessionData{SID: c.SID, UID: info.UID, Email: info.Email, Nickname: info.Nickname, SavedAt: time.Now().Unix()})
		if err := c.loadModels(); err != nil {
			fmt.Fprintln(cliOut, "warn: model load failed:", err)
		}
		return true
	}

	deviceKey := func() string {
		if v, ok := devFlags["id"]; ok {
			return v
		}
		return devFlags["name"]
	}
	requireDevice := func() (string, bool) {
		k := deviceKey()
		if k == "" {
			fmt.Fprintln(cliOut, "error: specify device with --name or --id")
			return "", false
		}
		return k, true
	}

	runOp := func(devKey, code string, value interface{}) int {
		if riskyCodes[code] && !*force {
			fmt.Fprintf(cliOut, "error: %q is a risky op; add -force to allow\n", code)
			return 1
		}
		raw, err := c.sendByCode(devKey, code, value)
		if err != nil {
			fmt.Fprintln(cliOut, "error:", err)
			return 1
		}
		fmt.Fprintf(cliOut, "op ok: %s=%v -> %s\n", code, value, string(raw))
		return 0
	}

	parseVal := func(s string) interface{} {
		switch s {
		case "true", "on":
			return true
		case "false", "off":
			return false
		}
		var f float64
		if err := json.Unmarshal([]byte(s), &f); err == nil {
			return f
		}
		return s
	}

	switch cmd {
	case "time":
		raw, err := c.call("smartlife.p.time.get", "1.0", nil)
		if err != nil {
			return fail("%s", err)
		}
		fmt.Fprintln(cliOut, "server time:", string(raw))
		return 0
	case "auth":
		return authCommand(c, fs.Args()[1:], email, password, countryCode)
	case "login":
		if !needLogin() {
			return 1
		}
		fmt.Fprintln(cliOut, "login ok")
		return 0
	case "devices":
		if !needLogin() {
			return 1
		}
		homes, _ := c.homes()
		for _, h := range homes {
			fmt.Fprintf(cliOut, "home: %s (gid=%d)\n", h.Name, h.GroupID)
			devs, _ := c.devices(h.GroupID)
			for _, d := range devs {
				if d.CloudOnline {
					printDevice(d, c.Models[d.ProductID])
				}
			}
		}
		return 0
	case "state":
		if !needLogin() {
			return 1
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		dev := c.findDevice(k)
		if dev == nil {
			return fail("device not found: %s", deviceKey())
		}
		printDevice(*dev, c.Models[dev.ProductID])
		return 0
	case "model":
		if !needLogin() {
			return 1
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		dev := c.findDevice(k)
		if dev == nil {
			return fail("device not found: %s", deviceKey())
		}
		raw, err := c.thingModel(dev.ProductID, dev.ProductVer)
		if err != nil {
			return fail("%s", err)
		}
		printModel(raw)
		return 0
	case "op":
		if !needLogin() {
			return 1
		}
		if fs.NArg() < 3 {
			fmt.Fprintln(cliOut, "usage: op --name X <dpCode> <value>")
			return 2
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		return runOp(k, fs.Arg(1), parseVal(fs.Arg(2)))
	case "clean":
		if !needLogin() {
			return 1
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		return runOp(k, "manual_clean", true)
	case "feed":
		if !needLogin() {
			return 1
		}
		n := 1.0
		if fs.NArg() > 1 {
			n = parseVal(fs.Arg(1)).(float64)
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		return runOp(k, "manual_feed", n)
	case "light":
		if !needLogin() {
			return 1
		}
		if fs.NArg() < 2 {
			fmt.Fprintln(cliOut, "usage: light --name X on|off")
			return 2
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		return runOp(k, "light", parseVal(fs.Arg(1)))
	case "sleep":
		if !needLogin() {
			return 1
		}
		if fs.NArg() < 2 {
			fmt.Fprintln(cliOut, "usage: sleep --name X on|off")
			return 2
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		return runOp(k, "sleep", parseVal(fs.Arg(1)))
	case "send":
		if !needLogin() {
			return 1
		}
		if fs.NArg() < 2 {
			fmt.Fprintln(cliOut, "usage: send --id ID <dpsJson>")
			return 2
		}
		var dps map[string]interface{}
		if err := json.Unmarshal([]byte(fs.Arg(1)), &dps); err != nil {
			return fail("dps json: %s", err)
		}
		k, ok := requireDevice()
		if !ok {
			return 2
		}
		raw, err := c.sendDps(k, dps)
		if err != nil {
			return fail("%s", err)
		}
		fmt.Fprintln(cliOut, "send ok:", string(raw))
		return 0
	default:
		fmt.Fprintln(cliOut, "unknown command:", cmd)
		return 2
	}
}

// authサブコマンド: login / status / logout
// restは"auth"以降の位置引数(loginの後置フラグ解釈に使用)
func authCommand(c *Client, rest []string, email, password, countryCode *string) int {
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	// auth loginは後置フラグも許可: auth login -email X -password Y
	if sub == "login" {
		fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
		em := fs.String("email", *email, "email")
		pw := fs.String("password", *password, "password")
		cc := fs.String("countryCode", *countryCode, "country code")
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		*email, *password, *countryCode = *em, *pw, *cc
	}
	switch sub {
	case "login":
		if tuiActive && (*email == "" || *password == "") {
			fmt.Fprintln(cliOut, "error: interactive input is unavailable in TUI mode. run: auth login -email EMAIL -password PASSWORD")
			return 2
		}
		// パラメータなければ対話型プロンプト
		reader := bufio.NewReader(os.Stdin)
		if *email == "" {
			fmt.Print("Email: ")
			line, _ := reader.ReadString('\n')
			*email = strings.TrimSpace(line)
		}
		if *password == "" {
			fmt.Print("Password: ")
			b, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Fprintln(cliOut)
			if err != nil {
				// 非TTY(パイプ)の場合は1行読み取り
				line, _ := reader.ReadString('\n')
				b = []byte(strings.TrimSpace(line))
			}
			*password = string(b)
		}
		if *email == "" || *password == "" {
			fmt.Fprintln(cliOut, "error: email and password are required")
			return 2
		}
		// 既存セッションが有効なら再利用
		if s, err := loadSession(); err == nil {
			if sd, err := c.sessionValid(s.SID); err == nil {
				fmt.Fprintf(cliOut, "already logged in: %s (nickname=%s)\n", sd.Email, sd.Nickname)
				return 0
			}
			deleteSession()
		}
		info, err := c.login(*email, *password, *countryCode)
		if err != nil {
			fmt.Fprintln(cliOut, "error:", err)
			return 1
		}
		if err := saveSession(sessionData{SID: c.SID, UID: info.UID, Email: info.Email, Nickname: info.Nickname, SavedAt: time.Now().Unix()}); err != nil {
			fmt.Fprintln(cliOut, "error: save session:", err)
			return 1
		}
		fmt.Fprintf(cliOut, "login ok: %s (nickname=%s), session saved to %s\n", info.Email, info.Nickname, sessionFile())
		return 0
	case "status":
		s, err := loadSession()
		if err != nil {
			fmt.Fprintln(cliOut, "not logged in")
			return 1
		}
		sd, err := c.sessionValid(s.SID)
		if err != nil {
			fmt.Fprintf(cliOut, "session expired or invalid (saved %s)\n", time.Unix(s.SavedAt, 0).Format(time.RFC3339))
			deleteSession()
			return 1
		}
		fmt.Fprintf(cliOut, "logged in: %s (nickname=%s, uid=%s, saved %s)\n", sd.Email, sd.Nickname, sd.UID, time.Unix(s.SavedAt, 0).Format(time.RFC3339))
		return 0
	case "logout":
		deleteSession()
		fmt.Fprintln(cliOut, "logged out (session file removed)")
		return 0
	default:
		fmt.Fprintln(cliOut, "usage: petsnowy-cli auth <login|status|logout>")
		return 2
	}
}

// 対話モード: 1行を1コマンドとして実行
func interactive() {
	c := NewClient(randomHex(8))
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintln(cliOut, "petsnowy-cli interactive mode — type 'help' for commands, 'quit' to exit")
	for {
		fmt.Print("petsnowy> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(cliOut)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line {
		case "quit", "exit", "q":
			return
		case "help", "?":
			usage()
			continue
		}
		fields := strings.Fields(line)
		run(fields, c)
	}
}

func main() {
	if len(os.Args) > 1 {
		first := os.Args[1]
		if first == "--help" || first == "-h" || first == "help" {
			usage()
			return
		}
		if first == "-v" || first == "--version" {
			fmt.Fprintln(cliOut, "petsnowy-cli 1.0.0")
			return
		}
		c := NewClient(randomHex(8))
		os.Exit(run(os.Args[1:], c))
	}
	runTUI()
}
