package firewall

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ProtoTCP  = "tcp"
	ProtoUDP  = "udp"
	ProtoBoth = "both"
)

var (
	ErrUFWNotFound     = errors.New("ufw is not installed")
	ErrUnsupportedHost = errors.New("ufw management requires a Debian/Ubuntu host with apt-get")
	ErrProtectedPort   = errors.New("refusing to remove a rule that protects SSH or the panel port")
	ErrInvalidPort     = errors.New("port must be between 1 and 65535")
	ErrInvalidProto    = errors.New("protocol must be tcp, udp, or both")
	ErrInvalidRule     = errors.New("rule number must be positive")
)

var numberedRuleRE = regexp.MustCompile(`^\[\s*(\d+)\]\s+(\S+)\s+(ALLOW|DENY|REJECT|LIMIT)\s+(IN|OUT)\s+(\S.*)$`)

// Runner executes an external command and returns combined stdout/stderr text.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

// Service manages host UFW rules for the panel.
type Service struct {
	Run          Runner
	SSHConfig    string
	LookPath     func(file string) (string, error)
	PanelPortFn  func() (int, error)
	CommandTimeout time.Duration
}

type Rule struct {
	Number    int    `json:"number"`
	To        string `json:"to"`
	Action    string `json:"action"`
	Direction string `json:"direction"`
	From      string `json:"from"`
	Protected bool   `json:"protected"`
}

type ProtectedPort struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	Label string `json:"label"`
}

type Status struct {
	Installed       bool            `json:"installed"`
	Active          bool            `json:"active"`
	Available       bool            `json:"available"`
	Rules           []Rule          `json:"rules"`
	ProtectedPorts  []ProtectedPort `json:"protectedPorts"`
	Message         string          `json:"message,omitempty"`
}

type PortSpec struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Proto string `json:"proto"`
}

func NewService(panelPortFn func() (int, error)) *Service {
	return &Service{
		Run:            defaultRunner,
		SSHConfig:      "/etc/ssh/sshd_config",
		LookPath:       exec.LookPath,
		PanelPortFn:    panelPortFn,
		CommandTimeout: 60 * time.Second,
	}
}

func defaultRunner(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func (s *Service) runner() Runner {
	if s.Run != nil {
		return s.Run
	}
	return defaultRunner
}

func (s *Service) lookPath() func(string) (string, error) {
	if s.LookPath != nil {
		return s.LookPath
	}
	return exec.LookPath
}

func (s *Service) timeout() time.Duration {
	if s.CommandTimeout > 0 {
		return s.CommandTimeout
	}
	return 60 * time.Second
}

func (s *Service) run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout())
	defer cancel()
	return s.runner()(ctx, name, args...)
}

func (s *Service) Status() (Status, error) {
	protected, err := s.protectedPorts()
	if err != nil {
		return Status{}, err
	}

	st := Status{
		Rules:          []Rule{},
		ProtectedPorts: protected,
	}

	if _, err := s.lookPath()("ufw"); err != nil {
		st.Message = ErrUFWNotFound.Error()
		return st, nil
	}
	st.Installed = true
	st.Available = true

	out, err := s.run("ufw", "status", "numbered")
	if err != nil {
		st.Message = strings.TrimSpace(out)
		if st.Message == "" {
			st.Message = err.Error()
		}
		return st, nil
	}

	st.Active = strings.Contains(out, "Status: active")
	st.Rules = parseNumberedRules(out, protected)
	return st, nil
}

func (s *Service) Install() error {
	if _, err := s.lookPath()("ufw"); err == nil {
		return nil
	}
	if _, err := s.lookPath()("apt-get"); err != nil {
		return ErrUnsupportedHost
	}
	if out, err := s.run("apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update failed: %w (%s)", err, strings.TrimSpace(out))
	}
	if out, err := s.run("apt-get", "install", "-y", "ufw"); err != nil {
		return fmt.Errorf("apt-get install ufw failed: %w (%s)", err, strings.TrimSpace(out))
	}
	if _, err := s.lookPath()("ufw"); err != nil {
		return ErrUFWNotFound
	}
	return nil
}

func (s *Service) Enable() error {
	if err := s.requireUFW(); err != nil {
		return err
	}
	if err := s.ensureProtectedAllows(); err != nil {
		return err
	}
	out, err := s.run("ufw", "--force", "enable")
	if err != nil {
		return fmt.Errorf("ufw enable failed: %w (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

func (s *Service) Disable() error {
	if err := s.requireUFW(); err != nil {
		return err
	}
	out, err := s.run("ufw", "disable")
	if err != nil {
		return fmt.Errorf("ufw disable failed: %w (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

func (s *Service) Allow(spec PortSpec) error {
	if err := s.requireUFW(); err != nil {
		return err
	}
	normalized, err := normalizePortSpec(spec)
	if err != nil {
		return err
	}
	for _, args := range allowArgs(normalized) {
		out, err := s.run("ufw", args...)
		if err != nil {
			return fmt.Errorf("ufw allow failed: %w (%s)", err, strings.TrimSpace(out))
		}
	}
	return nil
}

func (s *Service) DeleteByNumber(number int) error {
	if err := s.requireUFW(); err != nil {
		return err
	}
	if number <= 0 {
		return ErrInvalidRule
	}
	st, err := s.Status()
	if err != nil {
		return err
	}
	for _, rule := range st.Rules {
		if rule.Number == number {
			if rule.Protected {
				return ErrProtectedPort
			}
			out, err := s.run("ufw", "--force", "delete", strconv.Itoa(number))
			if err != nil {
				return fmt.Errorf("ufw delete failed: %w (%s)", err, strings.TrimSpace(out))
			}
			return nil
		}
	}
	return fmt.Errorf("ufw rule %d not found", number)
}

func (s *Service) DeleteAllow(spec PortSpec) error {
	if err := s.requireUFW(); err != nil {
		return err
	}
	normalized, err := normalizePortSpec(spec)
	if err != nil {
		return err
	}
	protected, err := s.protectedPorts()
	if err != nil {
		return err
	}
	if coversProtected(normalized, protected) {
		return ErrProtectedPort
	}
	for _, args := range deleteAllowArgs(normalized) {
		out, err := s.run("ufw", args...)
		if err != nil {
			return fmt.Errorf("ufw delete allow failed: %w (%s)", err, strings.TrimSpace(out))
		}
	}
	return nil
}

func (s *Service) requireUFW() error {
	if _, err := s.lookPath()("ufw"); err != nil {
		return ErrUFWNotFound
	}
	return nil
}

func (s *Service) ensureProtectedAllows() error {
	protected, err := s.protectedPorts()
	if err != nil {
		return err
	}
	for _, p := range protected {
		spec := PortSpec{Start: p.Port, End: p.Port, Proto: ProtoTCP}
		for _, args := range allowArgs(spec) {
			out, err := s.run("ufw", args...)
			if err != nil {
				return fmt.Errorf("failed to allow protected port %d/%s: %w (%s)", p.Port, ProtoTCP, err, strings.TrimSpace(out))
			}
		}
	}
	return nil
}

func (s *Service) protectedPorts() ([]ProtectedPort, error) {
	sshPort := s.sshPort()
	panelPort := 2053
	if s.PanelPortFn != nil {
		if port, err := s.PanelPortFn(); err == nil && port >= 1 && port <= 65535 {
			panelPort = port
		}
	}

	out := []ProtectedPort{
		{Port: sshPort, Proto: ProtoTCP, Label: fmt.Sprintf("%d/tcp (SSH)", sshPort)},
		{Port: panelPort, Proto: ProtoTCP, Label: fmt.Sprintf("%d/tcp (panel)", panelPort)},
	}
	if sshPort == panelPort {
		out = out[:1]
		out[0].Label = fmt.Sprintf("%d/tcp (SSH, panel)", sshPort)
	}
	return out, nil
}

func (s *Service) sshPort() int {
	path := s.SSHConfig
	if path == "" {
		path = "/etc/ssh/sshd_config"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 22
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	port := 22
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "Port") {
			if n, err := strconv.Atoi(fields[1]); err == nil && n >= 1 && n <= 65535 {
				port = n
			}
		}
	}
	return port
}

func normalizePortSpec(spec PortSpec) (PortSpec, error) {
	start := spec.Start
	end := spec.End
	if end == 0 {
		end = start
	}
	if start < 1 || start > 65535 || end < 1 || end > 65535 || end < start {
		return PortSpec{}, ErrInvalidPort
	}
	proto := strings.ToLower(strings.TrimSpace(spec.Proto))
	if proto == "" {
		proto = ProtoBoth
	}
	switch proto {
	case ProtoTCP, ProtoUDP, ProtoBoth:
	default:
		return PortSpec{}, ErrInvalidProto
	}
	return PortSpec{Start: start, End: end, Proto: proto}, nil
}

func allowArgs(spec PortSpec) [][]string {
	return protoArgs("allow", spec)
}

func deleteAllowArgs(spec PortSpec) [][]string {
	return protoArgs("delete", spec)
}

func protoArgs(action string, spec PortSpec) [][]string {
	portToken := strconv.Itoa(spec.Start)
	if spec.End != spec.Start {
		portToken = fmt.Sprintf("%d:%d", spec.Start, spec.End)
	}
	switch spec.Proto {
	case ProtoTCP:
		if action == "delete" {
			return [][]string{{"delete", "allow", portToken + "/tcp"}}
		}
		return [][]string{{"allow", portToken + "/tcp"}}
	case ProtoUDP:
		if action == "delete" {
			return [][]string{{"delete", "allow", portToken + "/udp"}}
		}
		return [][]string{{"allow", portToken + "/udp"}}
	default:
		if action == "delete" {
			if spec.End != spec.Start {
				return [][]string{
					{"delete", "allow", portToken + "/tcp"},
					{"delete", "allow", portToken + "/udp"},
				}
			}
			return [][]string{{"delete", "allow", portToken}}
		}
		if spec.End != spec.Start {
			return [][]string{
				{"allow", portToken + "/tcp"},
				{"allow", portToken + "/udp"},
			}
		}
		return [][]string{{"allow", portToken}}
	}
}

func parseNumberedRules(out string, protected []ProtectedPort) []Rule {
	rules := make([]Rule, 0)
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "[") {
			continue
		}
		m := numberedRuleRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		rule := Rule{
			Number:    num,
			To:        m[2],
			Action:    m[3],
			Direction: m[4],
			From:      strings.TrimSpace(m[5]),
		}
		rule.Protected = ruleProtects(rule, protected)
		rules = append(rules, rule)
	}
	return rules
}

func ruleProtects(rule Rule, protected []ProtectedPort) bool {
	to := strings.TrimSpace(rule.To)
	if strings.EqualFold(to, "OpenSSH") {
		return true
	}
	start, end, proto, ok := parseToPorts(to)
	if !ok {
		return false
	}
	for _, p := range protected {
		if p.Port < start || p.Port > end {
			continue
		}
		if proto == "" || proto == ProtoBoth || proto == p.Proto || p.Proto == ProtoBoth {
			return true
		}
	}
	return false
}

func coversProtected(spec PortSpec, protected []ProtectedPort) bool {
	for _, p := range protected {
		if p.Port < spec.Start || p.Port > spec.End {
			continue
		}
		if spec.Proto == ProtoBoth || p.Proto == ProtoBoth || spec.Proto == p.Proto {
			return true
		}
	}
	return false
}

func parseToPorts(to string) (start, end int, proto string, ok bool) {
	to = strings.TrimSpace(to)
	if to == "" || strings.EqualFold(to, "Anywhere") {
		return 0, 0, "", false
	}
	proto = ""
	portPart := to
	if i := strings.LastIndex(to, "/"); i >= 0 {
		portPart = to[:i]
		proto = strings.ToLower(to[i+1:])
	}
	if strings.Contains(portPart, ":") {
		parts := strings.SplitN(portPart, ":", 2)
		if len(parts) != 2 {
			return 0, 0, "", false
		}
		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, "", false
		}
		return start, end, proto, true
	}
	n, err := strconv.Atoi(portPart)
	if err != nil {
		return 0, 0, "", false
	}
	return n, n, proto, true
}

// WriteSSHConfigForTest writes a minimal sshd_config used by unit tests.
func WriteSSHConfigForTest(dir string, port int) (string, error) {
	path := filepath.Join(dir, "sshd_config")
	content := fmt.Sprintf("# test\nPort %d\n", port)
	return path, os.WriteFile(path, []byte(content), 0o600)
}
