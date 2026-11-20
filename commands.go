package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// 危険操作のdpCode — -forceなし拒否
var riskyCodes = map[string]bool{"empty": true, "factory_reset": true, "ball_reset": true, "filter_reset": true, "desiccant_reset": true}

// 位置によらず-name/-idフラグを抽出する
var devFlags = map[string]string{}

func pullDeviceFlags(args []string) []string {
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
	return out
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: petsnowy-cli [flags] <command> [--name X | --id ID]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  auth login [-email X -password Y]  login and save session (interactive if omitted)")
	fmt.Fprintln(os.Stderr, "  auth status                       show session status")
	fmt.Fprintln(os.Stderr, "  auth logout                       delete saved session")
	fmt.Fprintln(os.Stderr, "  time                              server time (no login)")
	fmt.Fprintln(os.Stderr, "  devices                           device list (dp names resolved)")
	fmt.Fprintln(os.Stderr, "  state --name X|--id ID            device dp state")
	fmt.Fprintln(os.Stderr, "  model --name X|--id ID            dpCode/dpID table")
	fmt.Fprintln(os.Stderr, "  op --name X <dpCode> <value>      operate by dpCode")
	fmt.Fprintln(os.Stderr, "  clean --name X|--id ID            manual clean")
	fmt.Fprintln(os.Stderr, "  feed --name X [n]                 manual feed (default 1)")
	fmt.Fprintln(os.Stderr, "  light --name X on|off             light switch")
	fmt.Fprintln(os.Stderr, "  sleep --name X on|off             sleep mode")
	fmt.Fprintln(os.Stderr, "  send --id ID <dpsJson>            raw dp send")
	fmt.Fprintln(os.Stderr, "flags:")
	fmt.Fprintln(os.Stderr, "  -email/-password/-countryCode     login credentials")
	fmt.Fprintln(os.Stderr, "  -force                            allow risky ops (empty/factory_reset/ball_reset)")
}

func main() {
	email := flag.String("email", "", "Tuya account email")
	password := flag.String("password", "", "account password")
	countryCode := flag.String("countryCode", "1", "country code")
	force := flag.Bool("force", false, "allow risky ops")
	flag.CommandLine.Parse(pullDeviceFlags(os.Args[1:]))

	if flag.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	cmd := flag.Arg(0)

	c := NewClient(randomHex(8))

	needLogin := func() {
		// 保存済みセッションを優先
		if s, err := loadSession(); err == nil {
			if sd, err := c.sessionValid(s.SID); err == nil {
				c.SID = sd.SID
				if err := c.loadModels(); err != nil {
					fmt.Fprintln(os.Stderr, "warn: model load failed:", err)
				}
				return
			}
			fmt.Fprintln(os.Stderr, "warn: saved session expired, re-login required")
			deleteSession()
		}
		if *email == "" || *password == "" {
			fmt.Fprintln(os.Stderr, "error: not logged in. run: petsnowy-cli auth login")
			os.Exit(1)
		}
		info, err := c.login(*email, *password, *countryCode)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		saveSession(sessionData{SID: c.SID, UID: info.UID, Email: info.Email, Nickname: info.Nickname, SavedAt: time.Now().Unix()})
		if err := c.loadModels(); err != nil {
			fmt.Fprintln(os.Stderr, "warn: model load failed:", err)
		}
	}

	deviceKey := func() string {
		if v, ok := devFlags["id"]; ok {
			return v
		}
		return devFlags["name"]
	}
	requireDevice := func() string {
		k := deviceKey()
		if k == "" {
			fmt.Fprintln(os.Stderr, "error: specify device with --name or --id")
			os.Exit(2)
		}
		return k
	}

	runOp := func(devKey, code string, value interface{}) {
		if riskyCodes[code] && !*force {
			fmt.Fprintf(os.Stderr, "error: %q is a risky op; add -force to allow\n", code)
			os.Exit(1)
		}
		raw, err := c.sendByCode(devKey, code, value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("op ok: %s=%v -> %s\n", code, value, string(raw))
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
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("server time:", string(raw))
	case "auth":
		authCommand(c, flag.Arg(1), email, password, countryCode)
	case "login":
		needLogin()
		fmt.Println("login ok")
	case "devices":
		needLogin()
		homes, _ := c.homes()
		for _, h := range homes {
			fmt.Printf("home: %s (gid=%d)\n", h.Name, h.GroupID)
			devs, _ := c.devices(h.GroupID)
			for _, d := range devs {
				if d.CloudOnline {
					printDevice(d, c.Models[d.ProductID])
				}
			}
		}
	case "state":
		needLogin()
		dev := c.findDevice(requireDevice())
		if dev == nil {
			fmt.Fprintln(os.Stderr, "error: device not found:", deviceKey())
			os.Exit(1)
		}
		printDevice(*dev, c.Models[dev.ProductID])
	case "model":
		needLogin()
		dev := c.findDevice(requireDevice())
		if dev == nil {
			fmt.Fprintln(os.Stderr, "error: device not found:", deviceKey())
			os.Exit(1)
		}
		raw, err := c.thingModel(dev.ProductID, dev.ProductVer)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printModel(raw)
	case "op":
		needLogin()
		if flag.NArg() < 3 {
			fmt.Fprintln(os.Stderr, "usage: op --name X <dpCode> <value>")
			os.Exit(2)
		}
		runOp(requireDevice(), flag.Arg(1), parseVal(flag.Arg(2)))
	case "clean":
		needLogin()
		runOp(requireDevice(), "manual_clean", true)
	case "feed":
		needLogin()
		n := 1.0
		if flag.NArg() > 1 {
			n = parseVal(flag.Arg(1)).(float64)
		}
		runOp(requireDevice(), "manual_feed", n)
	case "light":
		needLogin()
		if flag.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "usage: light --name X on|off")
			os.Exit(2)
		}
		runOp(requireDevice(), "light", parseVal(flag.Arg(1)))
	case "sleep":
		needLogin()
		if flag.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "usage: sleep --name X on|off")
			os.Exit(2)
		}
		runOp(requireDevice(), "sleep", parseVal(flag.Arg(1)))
	case "send":
		needLogin()
		if flag.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "usage: send --id ID <dpsJson>")
			os.Exit(2)
		}
		var dps map[string]interface{}
		if err := json.Unmarshal([]byte(flag.Arg(1)), &dps); err != nil {
			fmt.Fprintln(os.Stderr, "error: dps json:", err)
			os.Exit(1)
		}
		raw, err := c.sendDps(requireDevice(), dps)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("send ok:", string(raw))
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		os.Exit(2)
	}
}

// authサブコマンド: login / status / logout
func authCommand(c *Client, sub string, email, password, countryCode *string) {
	// auth loginは後置フラグも許可: auth login -email X -password Y
	if sub == "login" {
		fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
		em := fs.String("email", *email, "email")
		pw := fs.String("password", *password, "password")
		cc := fs.String("countryCode", *countryCode, "country code")
		if err := fs.Parse(os.Args[3:]); err != nil {
			os.Exit(2)
		}
		*email, *password, *countryCode = *em, *pw, *cc
	}
	switch sub {
	case "login":
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
			fmt.Println()
			if err != nil {
				// 非TTY(パイプ)の場合は1行読み取り
				line, _ := reader.ReadString('\n')
				b = []byte(strings.TrimSpace(line))
			}
			*password = string(b)
		}
		if *email == "" || *password == "" {
			fmt.Fprintln(os.Stderr, "error: email and password are required")
			os.Exit(2)
		}
		// 既存セッションが有効なら再利用
		if s, err := loadSession(); err == nil {
			if sd, err := c.sessionValid(s.SID); err == nil {
				fmt.Printf("already logged in: %s (nickname=%s)\n", sd.Email, sd.Nickname)
				return
			}
			deleteSession()
		}
		info, err := c.login(*email, *password, *countryCode)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if err := saveSession(sessionData{SID: c.SID, UID: info.UID, Email: info.Email, Nickname: info.Nickname, SavedAt: time.Now().Unix()}); err != nil {
			fmt.Fprintln(os.Stderr, "error: save session:", err)
			os.Exit(1)
		}
		fmt.Printf("login ok: %s (nickname=%s), session saved to %s\n", info.Email, info.Nickname, sessionFile())
	case "status":
		s, err := loadSession()
		if err != nil {
			fmt.Println("not logged in")
			os.Exit(1)
		}
		sd, err := c.sessionValid(s.SID)
		if err != nil {
			fmt.Printf("session expired or invalid (saved %s)\n", time.Unix(s.SavedAt, 0).Format(time.RFC3339))
			deleteSession()
			os.Exit(1)
		}
		fmt.Printf("logged in: %s (nickname=%s, uid=%s, saved %s)\n", sd.Email, sd.Nickname, sd.UID, time.Unix(s.SavedAt, 0).Format(time.RFC3339))
	case "logout":
		deleteSession()
		fmt.Println("logged out (session file removed)")
	default:
		fmt.Fprintln(os.Stderr, "usage: petsnowy-cli auth <login|status|logout>")
		os.Exit(2)
	}
}
