// Command agent stands in for a coding agent in hive's demo. Built as
// hive-demo-claude, hive-demo-opencode, hive-demo-codex or hive-demo-copilot,
// it reports to hive through `hive hook` as that tool would, plays a short
// script in its pane and answers what it is sent, so the demo is recorded
// without spending anything. A headless run (`codex exec`, `opencode run`)
// writes no screen; Codex's keeps a rollout file for hive's preview.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// step is one thing the agent does: an action with its result, or words.
type step struct {
	pause  float64  // seconds before it
	action string   // Read, Edit, Bash
	arg    string   // what it acts on, as shown
	out    string   // its result, shown under it
	say    string   // or words to the user
	run    []string // a command to run; its output becomes out
	child  []string // an agent to start alongside, not waited for
}

type script struct {
	title string
	steps []step
	reply []step // what it does with a message sent to it
}

// scripts are keyed by the prompt the agent starts with.
var scripts = map[string]script{
	"redesign checkout as one page and keep saved cards": {title: "Checkout redesign", steps: []step{
		{pause: .8, action: "Read", arg: "src/checkout/Checkout.tsx"},
		{pause: .8, action: "Read", arg: "src/checkout/steps/", out: "4 files"},
		{pause: 1.2, say: "One page it is. I'll hand the payment tests to opencode while I rework the page."},
		{pause: 1, action: "Bash", arg: `hive new opencode -p "port the payment tests to vitest" --wait`,
			run: []string{"hive", "new", "opencode", "-p", "port the payment tests to vitest", "--wait"}},
		{pause: 2, action: "Edit", arg: "src/checkout/Checkout.tsx", out: "+84 −121"},
		{pause: 4, action: "Edit", arg: "src/checkout/SavedCards.tsx", out: "+52 −8"},
		{pause: 4, action: "Bash", arg: "npm run typecheck", out: "✓ no type errors"},
		{pause: 6, action: "Edit", arg: "src/checkout/Summary.tsx", out: "+31 −44"},
		{pause: 6, action: "Edit", arg: "src/checkout/checkout.css", out: "+18 −62"},
		{pause: 7, action: "Bash", arg: "npm test -- checkout", out: "✓ 64 passed"},
		{pause: 7, action: "Read", arg: "src/api/payments.ts"},
		{pause: 7, action: "Edit", arg: "src/api/payments.ts", out: "+12 −3"},
		{pause: 8, action: "Bash", arg: "npm run lint", out: "✓ clean"},
		{pause: 8, action: "Edit", arg: "src/checkout/Checkout.test.tsx", out: "+40 −12"},
		{pause: 8, action: "Bash", arg: "npm test", out: "✓ 212 passed"},
		{pause: 4, say: "Checkout is one page now, saved cards on top. The payment tests are opencode's."},
	}},
	"port the payment tests to vitest": {title: "Port payment tests to Vitest", steps: []step{
		{pause: .8, action: "Read", arg: "jest.config.js"},
		{pause: 1, action: "Edit", arg: "vitest.config.ts", out: "new file, +24"},
		{pause: 1.2, action: "Bash", arg: `codex exec "run the e2e suite and fix what fails"`, out: "running in the background",
			child: []string{"hive-demo-codex", "exec", "run the e2e suite and fix what fails"}},
		{pause: 3, action: "Edit", arg: "src/payments/__tests__/charge.test.ts", out: "+9 −11"},
		{pause: 5, action: "Edit", arg: "src/payments/__tests__/refund.test.ts", out: "+7 −9"},
		{pause: 5, action: "Bash", arg: "npx vitest run src/payments", out: "✓ 18 passed ✗ 2 failed"},
		{pause: 7, action: "Edit", arg: "src/payments/__tests__/webhook.test.ts", out: "+14 −6"},
		{pause: 7, action: "Bash", arg: "npx vitest run src/payments", out: "✓ 20 passed"},
		{pause: 8, action: "Edit", arg: "package.json", out: "+2 −3"},
		{pause: 8, action: "Bash", arg: "npm test", out: "✓ 20 passed"},
		{pause: 8, action: "Edit", arg: ".github/workflows/test.yml", out: "+3 −3"},
		{pause: 4, say: "All 20 payment tests run under Vitest, and jest is gone from package.json."},
	}},
	"run the e2e suite and fix what fails": {steps: []step{
		{pause: 2, action: "Bash", arg: "npx playwright test", out: "38 passed, 2 failed (checkout.spec.ts)"},
		{pause: 4, action: "Read", arg: "e2e/checkout.spec.ts"},
		{pause: 3, say: "Both failures click the old Next step button; checkout is one page now."},
		{pause: 5, action: "Edit", arg: "e2e/checkout.spec.ts", out: "+11 −19"},
		{pause: 5, action: "Bash", arg: "npx playwright test e2e/checkout.spec.ts", out: "12 passed"},
		{pause: 7, action: "Bash", arg: "npx playwright test", out: "40 passed"},
		{pause: 3, say: "All 40 e2e tests pass; checkout.spec.ts follows the one-page flow."},
	}},
	"fix the flaky login test": {title: "Fix the flaky login test", steps: []step{
		{pause: .8, action: "Read", arg: "e2e/login.spec.ts"},
		{pause: 1.2, say: "It sleeps 2s before checking the redirect, which CI sometimes misses."},
		{pause: 1.2, action: "Edit", arg: "e2e/login.spec.ts", out: "+3 −2"},
		{pause: 2, action: "Bash", arg: "npx playwright test login --repeat-each 20", out: "20 passed"},
		{pause: 1, say: "Fixed: it waits for the dashboard URL instead of sleeping."},
	}, reply: []step{
		{pause: 1, action: "Read", arg: "playwright.config.ts"},
		{pause: 2, say: "CI runs with a 5s expect timeout and no retries; locally it's 10s."},
		{pause: 2, action: "Edit", arg: "playwright.config.ts", out: "+4 −1"},
		{pause: 2.5, action: "Bash", arg: "npx playwright test login --repeat-each 50", out: "50 passed"},
		{pause: 1, say: "CI now uses the 10s timeout and one retry."},
	}},
	"document the new checkout flow": {title: "Document the new checkout flow", steps: []step{
		{pause: .8, action: "Read", arg: "content/guides/checkout.md"},
		{pause: 1.2, action: "Edit", arg: "content/guides/checkout.md", out: "+46 −71"},
		{pause: 1, say: "The guide walks through the one-page checkout now."},
	}},
	"bump Go to 1.25 in the Dockerfiles": {title: "Bump Go to 1.25 in the Dockerfiles", steps: []step{
		{pause: .5, action: "Edit", arg: "images/api/Dockerfile", out: "+1 −1"},
		{pause: .5, action: "Edit", arg: "images/worker/Dockerfile", out: "+1 −1"},
	}},
	"add a skeleton loader to the cart": {title: "Cart skeleton loader", steps: []step{
		{pause: .8, action: "Read", arg: "src/cart/Cart.tsx"},
		{pause: 1.2, action: "Edit", arg: "src/cart/CartSkeleton.tsx", out: "new file, +38"},
		{pause: 1.5, action: "Edit", arg: "src/cart/Cart.tsx", out: "+6 −2"},
		{pause: 2, action: "Bash", arg: "npm test -- cart", out: "✓ 12 passed"},
		{pause: 1, say: "The cart shows a skeleton while it loads."},
	}},
}

// generic answers anything without a script.
var generic = []step{
	{pause: 1, action: "Read", arg: "README.md"},
	{pause: 1.5, say: "On it."},
}

var accents = map[string]string{"claude": "217;119;87", "opencode": "86;182;194", "copilot": "122;162;247", "codex": "16;163;127"}

const (
	bold  = "\x1b[1m"
	dim   = "\x1b[2m"
	green = "\x1b[32m"
	red   = "\x1b[31m"
	reset = "\x1b[0m"
)

func main() {
	tool := strings.TrimPrefix(filepath.Base(os.Args[0]), "hive-demo-")
	var id, prompt string
	headless := false
	for args := os.Args[1:]; len(args) > 0; args = args[1:] {
		switch a := args[0]; {
		case (a == "--session-id" || a == "--prompt") && len(args) > 1:
			if a == "--session-id" {
				id = args[1]
			} else {
				prompt = args[1]
			}
			args = args[1:]
		case a == "exec" || a == "run" || a == "-p":
			headless = true
		case a == "--interactive":
		default:
			prompt = a
		}
	}
	if id == "" {
		id = newID(tool)
	}
	cwd, _ := os.Getwd()
	sc, ok := scripts[prompt]
	if !ok {
		sc.steps, sc.reply = generic, generic
	}
	a := &agent{tool: tool, id: id, cwd: cwd, title: sc.title, headless: headless}
	a.hook("start")
	if headless {
		a.rollout(map[string]any{"type": "message", "role": "user",
			"content": []map[string]string{{"type": "input_text", "text": prompt}}})
		a.hook("busy")
		a.play(sc.steps)
		a.hook("end")
		return
	}
	fmt.Printf("%s%s%s %s%s%s\n\n", a.color(), bold+tool, reset, dim, shortPath(cwd), reset)
	if prompt != "" {
		a.take(prompt)
		a.play(sc.steps)
		a.hook("idle")
	}
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n" + a.color() + "› " + reset)
		if !in.Scan() {
			a.hook("end")
			return
		}
		if msg := strings.TrimSpace(in.Text()); msg != "" {
			a.hook("prompt")
			fmt.Println()
			a.play(cmpSteps(sc.reply, generic))
			a.hook("idle")
		}
	}
}

type agent struct {
	tool, id, cwd, title string
	headless             bool
}

func (a *agent) color() string { return "\x1b[38;2;" + accents[a.tool] + "m" }

// take shows the first prompt as if typed, and starts working on it.
func (a *agent) take(prompt string) {
	fmt.Printf("%s› %s%s%s\n\n", a.color(), reset+bold, prompt, reset)
	a.hook("prompt")
}

func (a *agent) play(steps []step) {
	for _, s := range steps {
		time.Sleep(time.Duration(s.pause * float64(time.Second)))
		if s.run != nil {
			out, _ := exec.Command(s.run[0], s.run[1:]...).CombinedOutput()
			s.out = strings.TrimSpace(string(out))
		}
		if s.child != nil {
			cmd := exec.Command(s.child[0], s.child[1:]...)
			if err := cmd.Start(); err == nil {
				go cmd.Wait()
			}
		}
		a.show(s)
	}
}

// show prints a step in the pane, or adds it to a headless run's rollout.
func (a *agent) show(s step) {
	if a.headless {
		switch {
		case s.say != "":
			a.rollout(map[string]any{"type": "message", "role": "assistant",
				"content": []map[string]string{{"type": "output_text", "text": s.say}}})
		case s.action == "Bash":
			a.rollout(map[string]any{"type": "local_shell_call", "action": map[string]any{"command": []string{"bash", "-lc", s.arg}}})
		default:
			a.rollout(map[string]any{"type": "custom_tool_call", "name": strings.ToLower(s.action), "input": s.arg})
		}
		return
	}
	if s.say != "" {
		fmt.Printf("%s\n\n", wrap(s.say, 48))
		return
	}
	fmt.Printf("%s●%s %s%s%s %s\n", a.color(), reset, bold, s.action, reset, s.arg)
	if s.out != "" {
		var out []string
		for w := range strings.FieldsSeq(s.out) {
			switch {
			case strings.HasPrefix(w, "+"), w == "✓":
				w = green + w + reset + dim
			case strings.HasPrefix(w, "−"), w == "✗":
				w = red + w + reset + dim
			}
			out = append(out, w)
		}
		fmt.Printf("  %s└ %s%s\n", dim, strings.Join(out, " "), reset)
	}
	fmt.Println()
}

// wrap breaks text into lines of at most width columns, so it reads the
// same in the pane and in hive's narrower preview of it.
func wrap(text string, width int) string {
	var lines []string
	line := ""
	for w := range strings.FieldsSeq(text) {
		if line != "" && len([]rune(line))+1+len([]rune(w)) > width {
			lines, line = append(lines, line), ""
		}
		line = strings.TrimSpace(line + " " + w)
	}
	return strings.Join(append(lines, line), "\n")
}

// hook reports an event to hive, the way the tool's own integration does.
func (a *agent) hook(event string) {
	args := []string{"hook", a.tool, "--event", event, "--session", a.id,
		"--pid", strconv.Itoa(os.Getpid()), "--cwd", a.cwd}
	if a.title != "" {
		args = append(args, "--title", a.title)
	}
	exec.Command("hive", args...).Run()
}

// rollout appends an item to a Codex run's session file, which is where
// hive's preview reads a headless run from.
func (a *agent) rollout(item map[string]any) {
	if a.tool != "codex" {
		return
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".codex")
	}
	now := time.Now()
	dir := filepath.Join(home, "sessions", now.Format("2006/01/02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "rollout-*-"+a.id+".jsonl"))
	path := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+a.id+".jsonl")
	var lines []any
	if len(matches) > 0 {
		path = matches[0]
	} else {
		lines = append(lines, map[string]any{"timestamp": now.Format(time.RFC3339), "type": "session_meta",
			"payload": map[string]any{"id": a.id, "cwd": a.cwd, "originator": "codex_exec", "source": "exec"}})
	}
	lines = append(lines, map[string]any{"timestamp": now.Format(time.RFC3339), "type": "response_item", "payload": item})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range lines {
		enc.Encode(l)
	}
}

func cmpSteps(steps, fallback []step) []step {
	if len(steps) > 0 {
		return steps
	}
	return fallback
}

func newID(tool string) string {
	b := make([]byte, 16)
	rand.Read(b)
	h := hex.EncodeToString(b)
	if tool == "opencode" {
		return "ses_" + h[:26]
	}
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func shortPath(p string) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
