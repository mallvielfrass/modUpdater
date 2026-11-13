package minecraft

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrRestart      = errors.New("restart_failed")
	ErrLogs         = errors.New("logs_failed")
	ErrBusy         = errors.New("restart_in_progress")
	ErrInvalidLimit = errors.New("invalid_limit")
)

type Service struct {
	Unit     string
	Warning  string
	Delay    time.Duration
	host     string
	port     int
	password string
	Say      func(command string) error
	Exec     func(command string) (string, error)
	Run      func(name string, args []string) error
	Output   func(name string, args []string) (string, error)
	mu       sync.Mutex
}

func New(unit, warning string, delaySec int, host string, port int, password string) *Service {
	s := &Service{
		Unit:     unit,
		Warning:  warning,
		Delay:    time.Duration(delaySec) * time.Second,
		host:     host,
		port:     port,
		password: password,
	}
	s.Say = s.sayRCON
	s.Exec = s.execRCON
	s.Run = execRun
	s.Output = execOutput
	return s
}

func (s *Service) Restart(message string) error {
	return s.RestartBefore(message, nil)
}

func (s *Service) RestartBefore(message string, before func() error) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if err := s.Say(SayCommand(message, s.Warning)); err != nil {
		return ErrRCON
	}
	if s.Delay > 0 {
		time.Sleep(s.Delay)
	}
	var beforeErr error
	if before != nil {
		beforeErr = before()
	}
	if err := s.Run("systemctl", []string{"restart", s.Unit}); err != nil {
		return ErrRestart
	}
	return beforeErr
}

func (s *Service) RestartPrepared(message string, prepare func() error) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if err := s.Say(SayCommand(message, s.Warning)); err != nil {
		return ErrRCON
	}
	if s.Delay > 0 {
		time.Sleep(s.Delay)
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
	}
	if err := s.Run("systemctl", []string{"restart", s.Unit}); err != nil {
		return ErrRestart
	}
	return nil
}

func (s *Service) ActiveEnter() (time.Time, bool) {
	if s == nil || s.Unit == "" || s.Output == nil {
		return time.Time{}, false
	}
	out, err := s.Output("systemctl", []string{"show", s.Unit, "-p", "ActiveEnterTimestamp", "--value", "--timestamp=unix"})
	if err == nil {
		if at, ok := parseUnixStamp(out); ok {
			return at, true
		}
	}
	out, err = s.Output("systemctl", []string{"show", s.Unit, "-p", "ActiveEnterTimestamp", "--value"})
	if err != nil {
		return time.Time{}, false
	}
	return parseLocalStamp(out)
}

func parseUnixStamp(raw string) (time.Time, bool) {
	line := firstLine(raw)
	line = strings.TrimPrefix(line, "@")
	if line == "" || line == "n/a" || line == "0" {
		return time.Time{}, false
	}
	whole, frac, _ := strings.Cut(line, ".")
	if whole == "" || !digitsOnly(whole) {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}, false
	}
	var nsec int64
	if sec > 1_000_000_000_000 {
		nsec = (sec % 1_000_000) * 1000
		sec /= 1_000_000
	} else if frac != "" {
		if len(frac) > 9 {
			frac = frac[:9]
		}
		if !digitsOnly(frac) {
			return time.Time{}, false
		}
		for len(frac) < 9 {
			frac += "0"
		}
		nsec, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
	}
	return time.Unix(sec, nsec).UTC(), true
}

func parseLocalStamp(raw string) (time.Time, bool) {
	line := firstLine(raw)
	if line == "" || line == "n/a" || line == "0" {
		return time.Time{}, false
	}
	fields := strings.Fields(line)
	if len(fields) >= 2 && !strings.Contains(fields[len(fields)-1], ":") {
		line = strings.TrimSpace(strings.TrimSuffix(line, fields[len(fields)-1]))
	}
	at, err := time.ParseInLocation("Mon 2006-01-02 15:04:05", line, time.Local)
	if err != nil {
		at, err = time.ParseInLocation("Mon 2006-01-02 15:04:05.000000", line, time.Local)
		if err != nil {
			return time.Time{}, false
		}
	}
	if at.Unix() <= 0 {
		return time.Time{}, false
	}
	return at.UTC(), true
}

func firstLine(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	for _, part := range strings.Split(raw, "\n") {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Service) Logs(lines int, since string) (string, []string, error) {
	if lines < 1 || lines > 5000 {
		return "", nil, ErrInvalidLimit
	}
	args := []string{
		"-u", s.Unit,
		"-n", strconv.Itoa(lines),
		"-o", "short-iso",
		"--no-pager",
	}
	switch since {
	case "", "all":
	case "hour":
		args = append(args, "--since", "1 hour ago")
	case "day":
		args = append(args, "--since", "1 day ago")
	default:
		return "", nil, ErrInvalidLimit
	}
	out, err := s.Output("journalctl", args)
	if err != nil {
		return "", nil, ErrLogs
	}
	return s.Unit, splitLines(out), nil
}

func execRun(name string, args []string) error {
	cmd := exec.Command(name, args...)
	_, err := cmd.Output()
	return err
}

func execOutput(name string, args []string) (string, error) {
	cmd := exec.Command(name, args...)
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}
