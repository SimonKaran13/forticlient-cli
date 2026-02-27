package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Tunnel struct {
	ConnectionName string `json:"connection_name"`
	Type           string `json:"type"`
	CloudVPN       int    `json:"cloud_vpn"`
	Corporate      int    `json:"corporate"`
	Default        bool   `json:"default,omitempty"`
}

type TunnelState struct {
	IPSecState     int    `json:"ipsec_state"`
	SSLState       int    `json:"ssl_state"`
	ConnectionName string `json:"connection_name"`
	SamlVPNName    string `json:"saml_vpn_name"`
}

type Status struct {
	State              string `json:"state"`
	Connected          bool   `json:"connected"`
	CurrentConnection  string `json:"current_connection"`
	SelectedConnection string `json:"selected_connection,omitempty"`
	CheckedAt          int64  `json:"checked_at"`
}

type bridgeResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func main() {
	code := run(os.Args[1:])
	os.Exit(code)
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch args[0] {
	case "connections", "services":
		return runConnections(args[1:])
	case "status":
		return runStatus(args[1:])
	case "connect":
		return runConnect(args[1:])
	case "disconnect":
		return runDisconnect(args[1:])
	case "watch":
		return runWatch(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", args[0])
		printUsage()
		return 2
	}
}

func printUsage() {
	fmt.Println(`fortivpn: FortiClient VPN helper CLI for macOS

Usage:
  fortivpn connections [--json]
  fortivpn status [--connection NAME] [--json]
  fortivpn connect [--connection NAME] [--timeout SEC] [--interval SEC] [--json]
  fortivpn disconnect [--timeout SEC] [--interval SEC] [--json]
  fortivpn watch [--connection NAME] [--timeout SEC] [--interval SEC]
`)
}

func runConnections(args []string) int {
	fs := flag.NewFlagSet("connections", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "Emit JSON output.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tunnels, err := getConnections()
	if err != nil {
		return fail(err)
	}
	if len(tunnels) == 0 {
		fmt.Println("No FortiClient VPN connections found.")
		return 1
	}

	if *asJSON {
		return printJSON(tunnels)
	}
	for _, tunnel := range tunnels {
		fmt.Printf("%s [type=%s]\n", tunnel.ConnectionName, tunnel.Type)
	}
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	connectionArg := fs.String("connection", "", "VPN connection name, e.g. prod/int.")
	asJSON := fs.Bool("json", false, "Emit JSON output.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tunnels, err := getConnections()
	if err != nil {
		return fail(err)
	}

	selectedName := ""
	if strings.TrimSpace(*connectionArg) != "" {
		tunnel, err := resolveTunnel(*connectionArg, tunnels)
		if err != nil {
			return fail(err)
		}
		selectedName = tunnel.ConnectionName
	}

	state, err := getTunnelState()
	if err != nil {
		return fail(err)
	}

	status := buildStatus(state, selectedName)
	if *asJSON {
		if code := printJSON(status); code != 0 {
			return code
		}
	} else {
		fmt.Printf("state: %s\n", status.State)
		fmt.Printf("current connection: %s\n", emptyAsUnknown(status.CurrentConnection))
		if status.SelectedConnection != "" {
			fmt.Printf("selected connection: %s\n", status.SelectedConnection)
		}
	}

	if status.Connected {
		return 0
	}
	return 1
}

func runConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	connectionArg := fs.String("connection", "", "VPN connection name, e.g. prod/int.")
	asJSON := fs.Bool("json", false, "Emit JSON output.")
	timeoutSec := fs.Float64("timeout", 20, "Wait timeout in seconds.")
	intervalSec := fs.Float64("interval", 1, "Polling interval in seconds.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := ensureFortiClientRunning(5 * time.Second); err != nil {
		return fail(err)
	}

	tunnels, err := getConnections()
	if err != nil {
		return fail(err)
	}
	target, err := resolveTunnel(*connectionArg, tunnels)
	if err != nil {
		return fail(err)
	}

	currentState, err := getTunnelState()
	if err != nil {
		return fail(err)
	}
	if currentState.Connected() && strings.EqualFold(currentState.CurrentConnection(), target.ConnectionName) {
		status := buildStatus(currentState, target.ConnectionName)
		return printConnectResult(status, *asJSON)
	}

	payload := map[string]string{
		"connection_name": target.ConnectionName,
		"connection_type": target.Type,
	}
	if _, err := runBridge("connect", payload); err != nil {
		return fail(err)
	}

	finalState, err := waitForTunnelState(target.ConnectionName, true, seconds(*timeoutSec), seconds(*intervalSec))
	if err != nil {
		return fail(err)
	}

	status := buildStatus(finalState, target.ConnectionName)
	return printConnectResult(status, *asJSON)
}

func runDisconnect(args []string) int {
	fs := flag.NewFlagSet("disconnect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "Emit JSON output.")
	timeoutSec := fs.Float64("timeout", 10, "Wait timeout in seconds.")
	intervalSec := fs.Float64("interval", 1, "Polling interval in seconds.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	state, err := getTunnelState()
	if err != nil {
		return fail(err)
	}
	if !state.Connected() {
		status := buildStatus(state, "")
		if *asJSON {
			if code := printJSON(status); code != 0 {
				return code
			}
		} else {
			fmt.Printf("state: %s\n", status.State)
			fmt.Printf("current connection: %s\n", emptyAsUnknown(status.CurrentConnection))
		}
		return 0
	}

	payload := map[string]string{
		"connection_name": state.CurrentConnection(),
		"connection_type": state.ConnectionType(),
	}
	if _, err := runBridge("disconnect", payload); err != nil {
		return fail(err)
	}

	finalState, err := waitForTunnelState("", false, seconds(*timeoutSec), seconds(*intervalSec))
	if err != nil {
		return fail(err)
	}
	status := buildStatus(finalState, "")

	if *asJSON {
		if code := printJSON(status); code != 0 {
			return code
		}
	} else {
		fmt.Printf("state: %s\n", status.State)
		fmt.Printf("current connection: %s\n", emptyAsUnknown(status.CurrentConnection))
	}

	if !status.Connected {
		return 0
	}
	return 2
}

func runWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	connectionArg := fs.String("connection", "", "VPN connection name, e.g. prod/int.")
	timeoutSec := fs.Float64("timeout", 20, "Reconnect wait timeout in seconds.")
	intervalSec := fs.Float64("interval", 5, "Polling interval in seconds.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tunnels, err := getConnections()
	if err != nil {
		return fail(err)
	}
	target, err := resolveTunnel(*connectionArg, tunnels)
	if err != nil {
		return fail(err)
	}

	interval := seconds(*intervalSec)
	timeout := seconds(*timeoutSec)
	fmt.Printf("Watching %q. interval=%s reconnect-timeout=%s\n", target.ConnectionName, interval, timeout)

	lastStatus := ""
	for {
		state, err := getTunnelState()
		if err != nil {
			return fail(err)
		}

		status := buildStatus(state, target.ConnectionName)
		label := fmt.Sprintf("%s (%s)", status.State, emptyAsUnknown(status.CurrentConnection))
		if label != lastStatus {
			fmt.Printf("%s state=%s connection=%s\n", now(), status.State, emptyAsUnknown(status.CurrentConnection))
			lastStatus = label
		}

		shouldReconnect := !state.Connected() || !strings.EqualFold(state.CurrentConnection(), target.ConnectionName)
		if shouldReconnect {
			fmt.Printf("%s reconnecting to %q...\n", now(), target.ConnectionName)
			payload := map[string]string{
				"connection_name": target.ConnectionName,
				"connection_type": target.Type,
			}
			if _, err := runBridge("connect", payload); err != nil {
				fmt.Printf("%s reconnect start failed: %v\n", now(), err)
			} else {
				outcome, err := waitForTunnelState(target.ConnectionName, true, timeout, interval)
				if err != nil {
					fmt.Printf("%s reconnect failed: %v\n", now(), err)
				} else {
					fmt.Printf("%s reconnect result=%s connection=%s\n", now(), connectedLabel(outcome.Connected()), emptyAsUnknown(outcome.CurrentConnection()))
					lastStatus = ""
				}
			}
		}

		time.Sleep(interval)
	}
}

func getConnections() ([]Tunnel, error) {
	result, err := runBridge("list-connections", nil)
	if err != nil {
		return nil, err
	}

	var tunnels []Tunnel
	if len(result) == 0 || string(result) == "null" {
		return tunnels, nil
	}
	if err := json.Unmarshal(result, &tunnels); err != nil {
		return nil, fmt.Errorf("failed to decode tunnel list: %w", err)
	}
	return tunnels, nil
}

func getTunnelState() (TunnelState, error) {
	result, err := runBridge("get-state", nil)
	if err != nil {
		return TunnelState{}, err
	}
	if len(result) == 0 || string(result) == "null" {
		return TunnelState{}, nil
	}

	var state TunnelState
	if err := json.Unmarshal(result, &state); err != nil {
		return TunnelState{}, fmt.Errorf("failed to decode tunnel state: %w", err)
	}
	return state, nil
}

func waitForTunnelState(expectedConnection string, shouldBeConnected bool, timeout, interval time.Duration) (TunnelState, error) {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	if timeout < 0 {
		timeout = 0
	}

	deadline := time.Now().Add(timeout)
	last, err := getTunnelState()
	if err != nil {
		return TunnelState{}, err
	}

	for !time.Now().After(deadline) {
		last, err = getTunnelState()
		if err != nil {
			return TunnelState{}, err
		}

		if shouldBeConnected {
			if last.Connected() {
				if expectedConnection == "" || strings.EqualFold(last.CurrentConnection(), expectedConnection) || last.CurrentConnection() == "" {
					return last, nil
				}
			}
		} else if !last.Connected() {
			return last, nil
		}

		time.Sleep(interval)
	}

	return last, nil
}

func resolveTunnel(target string, tunnels []Tunnel) (Tunnel, error) {
	if len(tunnels) == 0 {
		return Tunnel{}, errors.New("no FortiClient VPN connections found")
	}

	target = strings.TrimSpace(target)
	if target == "" {
		return tunnels[0], nil
	}

	for _, tunnel := range tunnels {
		if strings.EqualFold(target, tunnel.ConnectionName) {
			return tunnel, nil
		}
	}

	alias := strings.ToLower(target)
	candidates := make([]Tunnel, 0)
	for _, tunnel := range tunnels {
		name := strings.ToLower(tunnel.ConnectionName)
		if strings.Contains(name, alias) {
			candidates = append(candidates, tunnel)
			continue
		}
		if (alias == "prod" || alias == "production") && strings.Contains(name, "production") {
			candidates = append(candidates, tunnel)
			continue
		}
		if (alias == "int" || alias == "integration") && strings.Contains(name, "integration") {
			candidates = append(candidates, tunnel)
		}
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.ConnectionName)
		}
		return Tunnel{}, fmt.Errorf("connection %q is ambiguous; matches: %s", target, strings.Join(names, ", "))
	}

	available := make([]string, 0, len(tunnels))
	for _, tunnel := range tunnels {
		available = append(available, tunnel.ConnectionName)
	}
	return Tunnel{}, fmt.Errorf("connection %q not found; available: %s", target, strings.Join(available, ", "))
}

func runBridge(action string, payload any) (json.RawMessage, error) {
	bridge, err := findBridgeScript()
	if err != nil {
		return nil, err
	}

	args := []string{bridge, action}
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		args = append(args, string(body))
	}

	cmd := exec.Command("node", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}

	var resp bridgeResponse
	if err := decodeBridgeResponse(out, &resp); err != nil {
		return nil, fmt.Errorf("invalid bridge response: %s", strings.TrimSpace(string(out)))
	}
	if !resp.OK {
		if strings.TrimSpace(resp.Error) == "" {
			return nil, errors.New("bridge call failed")
		}
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

func decodeBridgeResponse(raw []byte, out *bridgeResponse) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return errors.New("empty output")
	}

	if err := json.Unmarshal([]byte(trimmed), out); err == nil {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(candidate, "{") {
			continue
		}
		if err := json.Unmarshal([]byte(candidate), out); err == nil {
			return nil
		}
	}

	lastObj := strings.LastIndex(trimmed, "{")
	if lastObj >= 0 {
		candidate := trimmed[lastObj:]
		if err := json.Unmarshal([]byte(candidate), out); err == nil {
			return nil
		}
	}

	return errors.New("no json response found")
}

func findBridgeScript() (string, error) {
	candidates := []string{}
	if fromEnv := strings.TrimSpace(os.Getenv("FORTIVPN_BRIDGE")); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}

	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "fortivpn-bridge.js"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "fortivpn-bridge.js"))
	}

	for _, candidate := range candidates {
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("could not find fortivpn-bridge.js")
}

func buildStatus(state TunnelState, selectedConnection string) Status {
	connected := state.Connected()
	if selectedConnection != "" {
		connected = connected && strings.EqualFold(state.CurrentConnection(), selectedConnection)
	}
	return Status{
		State:              connectedLabel(connected),
		Connected:          connected,
		CurrentConnection:  state.CurrentConnection(),
		SelectedConnection: selectedConnection,
		CheckedAt:          time.Now().Unix(),
	}
}

func (s TunnelState) Connected() bool {
	return s.SSLState != 0 || s.IPSecState != 0
}

func (s TunnelState) CurrentConnection() string {
	if strings.TrimSpace(s.ConnectionName) != "" {
		return strings.TrimSpace(s.ConnectionName)
	}
	if strings.TrimSpace(s.SamlVPNName) != "" {
		return strings.TrimSpace(s.SamlVPNName)
	}
	return ""
}

func (s TunnelState) ConnectionType() string {
	if s.IPSecState != 0 {
		return "ipsec"
	}
	return "ssl"
}

func connectedLabel(connected bool) string {
	if connected {
		return "Connected"
	}
	return "Disconnected"
}

func printConnectResult(status Status, asJSON bool) int {
	if asJSON {
		if code := printJSON(status); code != 0 {
			return code
		}
	} else {
		fmt.Printf("state: %s\n", status.State)
		fmt.Printf("current connection: %s\n", emptyAsUnknown(status.CurrentConnection))
		if status.SelectedConnection != "" {
			fmt.Printf("selected connection: %s\n", status.SelectedConnection)
		}
	}

	if status.Connected {
		return 0
	}
	return 2
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return 3
}

func seconds(v float64) time.Duration {
	if v <= 0 {
		return 0
	}
	return time.Duration(v * float64(time.Second))
}

func now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func emptyAsUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "<none>"
	}
	return v
}

func ensureFortiClientRunning(wait time.Duration) error {
	if fortiClientRunning() {
		return nil
	}

	if err := exec.Command("open", "-a", "FortiClient").Run(); err != nil {
		return fmt.Errorf("failed to start FortiClient app: %w", err)
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if fortiClientRunning() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return errors.New("FortiClient app did not start in time")
}

func fortiClientRunning() bool {
	return exec.Command("pgrep", "-x", "FortiClient").Run() == nil
}
