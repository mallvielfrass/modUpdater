package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	dialTimeout   = 10 * time.Second
	authTimeout   = 30 * time.Second
	uploadTimeout = 10 * time.Minute

	versionFile = "client/src-tauri/tauri.conf.json"
	linuxFile   = "client/src-tauri/target/release/modupdater-client"
	windowsFile = "client/src-tauri/target/x86_64-pc-windows-msvc/release/modupdater-client.exe"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	server := fs.String("server", defaultServerURL(), "адрес сервера")
	versionFlag := fs.String("version", "", "версия релиза")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	version := strings.TrimSpace(*versionFlag)
	if version == "" {
		version, err = readVersion(filepath.Join(root, versionFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "не удалось прочитать версию из %s\n", filepath.Join(root, versionFile))
			return 1
		}
	}
	linuxPath := filepath.Join(root, linuxFile)
	windowsPath := filepath.Join(root, windowsFile)
	if err := requireFile(linuxPath); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if err := requireFile(windowsPath); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	base := strings.TrimRight(*server, "/")
	authClient := newClient(authTimeout)
	token, err := acquireToken(authClient, base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	if err := uploadRelease(newClient(uploadTimeout), base, token, version, linuxPath, windowsPath); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	return 0
}

func defaultServerURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("MODUPDATER_SERVER_URL")), "/"); v != "" {
		return v
	}
	if v := strings.TrimRight(strings.TrimSpace(dotenvValue("MODUPDATER_SERVER_URL")), "/"); v != "" {
		return v
	}
	return "http://127.0.0.1:8080"
}

func dotenvValue(key string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if fileExists(filepath.Join(dir, "Makefile")) {
			return readDotenvKey(filepath.Join(dir, ".env"), key)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func readDotenvKey(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	prefix := key + "="
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, prefix) {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		val = strings.Trim(val, `"'`)
		return val
	}
	return ""
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout: dialTimeout,
			}).DialContext,
		},
	}
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, versionFile)) || fileExists(filepath.Join(dir, linuxFile)) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("не найден корень репозитория")
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func readVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.Version) == "" {
		return "", errors.New("empty version")
	}
	return strings.TrimSpace(cfg.Version), nil
}

func requireFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("нет файла %s", path)
		}
		return fmt.Errorf("недоступен файл %s", path)
	}
	if st.IsDir() {
		return fmt.Errorf("нет файла %s", path)
	}
	return nil
}

func acquireToken(client *http.Client, server string) (string, error) {
	if tok := strings.TrimSpace(os.Getenv("MODUPDATER_TOKEN")); tok != "" {
		return tok, nil
	}
	path, err := refreshPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		refresh := strings.TrimSpace(string(data))
		if refresh != "" {
			tok, next, status, apiErr, reqErr := postAuth(client, server+"/api/auth/refresh", map[string]string{
				"refresh_token": refresh,
			})
			if reqErr != nil {
				return "", reqErr
			}
			if status != http.StatusUnauthorized {
				if status/100 != 2 {
					return "", statusErr(status, apiErr)
				}
				if err := saveRefresh(next); err != nil {
					return "", err
				}
				return tok, nil
			}
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("не удалось прочитать %s", path)
	}

	user, pass, err := promptCreds()
	if err != nil {
		return "", err
	}
	tok, next, status, apiErr, reqErr := postAuth(client, server+"/api/auth/login", map[string]string{
		"username": user,
		"password": pass,
	})
	if reqErr != nil {
		return "", reqErr
	}
	if status/100 != 2 {
		return "", statusErr(status, apiErr)
	}
	if err := saveRefresh(next); err != nil {
		return "", err
	}
	return tok, nil
}

func postAuth(client *http.Client, url string, payload map[string]string) (token, refresh string, status int, apiErr string, err error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", 0, "", errors.New("вход не удался")
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", "", 0, "", errors.New("вход не удался")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("ошибка запроса: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", resp.StatusCode, "", fmt.Errorf("ошибка запроса: %v", err)
	}
	var parsed struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(data, &parsed)
	if resp.StatusCode/100 != 2 {
		return "", "", resp.StatusCode, parsed.Error, nil
	}
	if parsed.Token == "" || parsed.RefreshToken == "" {
		return "", "", resp.StatusCode, "", errors.New("вход не удался")
	}
	return parsed.Token, parsed.RefreshToken, resp.StatusCode, "", nil
}

func statusErr(code int, api string) error {
	if api != "" {
		return fmt.Errorf("HTTP %d %s", code, api)
	}
	return fmt.Errorf("HTTP %d", code)
}

func uploadErr(version string, status int, api string) error {
	api = strings.TrimSpace(api)
	if status == http.StatusConflict && (api == "" || api == "conflict") {
		line := fmt.Sprintf("версия %s уже выпущена, укажите новую через -version", version)
		if api == "" {
			return fmt.Errorf("%s\nHTTP %d", line, status)
		}
		return fmt.Errorf("%s\nHTTP %d эта версия уже выпущена", line, status)
	}
	return statusErr(status, api)
}

func refreshPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("не найден домашний каталог")
	}
	return filepath.Join(home, ".config", "modupdater", "upload-refresh"), nil
}

func saveRefresh(refresh string) error {
	path, err := refreshPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("не удалось сохранить refresh")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("не удалось сохранить refresh")
	}
	tmp, err := os.CreateTemp(dir, ".upload-refresh-*")
	if err != nil {
		return fmt.Errorf("не удалось сохранить refresh")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("не удалось сохранить refresh")
	}
	if _, err := io.WriteString(tmp, refresh+"\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("не удалось сохранить refresh")
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("не удалось сохранить refresh")
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("не удалось сохранить refresh")
	}
	return os.Chmod(path, 0o600)
}

func promptCreds() (string, string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", "", errors.New("нет терминала")
	}
	defer tty.Close()
	if _, err := io.WriteString(tty, "username: "); err != nil {
		return "", "", err
	}
	user, err := readLine(tty)
	if err != nil {
		return "", "", err
	}
	if _, err := io.WriteString(tty, "password: "); err != nil {
		return "", "", err
	}
	pass, err := readPassword(tty)
	if err != nil {
		return "", "", err
	}
	_, _ = io.WriteString(tty, "\n")
	return user, pass, nil
}

func readLine(f *os.File) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for {
		n, err := f.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			if one[0] != '\r' {
				b = append(b, one[0])
			}
		}
		if err != nil {
			if err == io.EOF && len(b) > 0 {
				break
			}
			return "", err
		}
	}
	return string(b), nil
}

func readPassword(f *os.File) (string, error) {
	fd := int(f.Fd())
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return "", err
	}
	noecho := *state
	noecho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &noecho); err != nil {
		return "", err
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, state)
	return readLine(f)
}

func uploadRelease(client *http.Client, server, token, version, linuxPath, windowsPath string) error {
	body, size, contentType, err := buildBody(version, linuxPath, windowsPath)
	if err != nil {
		return err
	}
	defer body.Close()
	defer os.Remove(body.Name())

	out, closeOut := progressOut()
	defer closeOut()
	fmt.Fprintln(out, "загрузка...")

	req, err := http.NewRequest(http.MethodPost, server+"/api/admin/releases", &progressReader{
		r:     body,
		total: size,
		out:   out,
	})
	if err != nil {
		return errors.New("ошибка запроса")
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	fmt.Fprint(out, formatBar(0, size))
	resp, err := client.Do(req)
	fmt.Fprintln(out)
	if err != nil {
		return fmt.Errorf("ошибка запроса: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("ошибка запроса: %v", err)
	}
	if resp.StatusCode/100 == 2 {
		return nil
	}
	var parsed struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &parsed)
	return uploadErr(version, resp.StatusCode, parsed.Error)
}

func buildBody(version, linuxPath, windowsPath string) (*os.File, int64, string, error) {
	f, err := os.CreateTemp("", "modupdater-upload-*")
	if err != nil {
		return nil, 0, "", errors.New("не удалось подготовить загрузку")
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	mw := multipart.NewWriter(f)
	if err := mw.WriteField("version", version); err != nil {
		return nil, 0, "", errors.New("не удалось подготовить загрузку")
	}
	if err := copyPart(mw, "linux_file", "modupdater-linux", linuxPath); err != nil {
		return nil, 0, "", err
	}
	if err := copyPart(mw, "windows_file", "modupdater-windows.exe", windowsPath); err != nil {
		return nil, 0, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, 0, "", errors.New("не удалось подготовить загрузку")
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, 0, "", errors.New("не удалось подготовить загрузку")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", errors.New("не удалось подготовить загрузку")
	}
	ok = true
	return f, size, mw.FormDataContentType(), nil
}

func copyPart(mw *multipart.Writer, field, filename, path string) error {
	src, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("нет файла %s", path)
		}
		return fmt.Errorf("недоступен файл %s", path)
	}
	defer src.Close()
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return errors.New("не удалось подготовить загрузку")
	}
	if _, err := io.Copy(part, src); err != nil {
		return errors.New("не удалось подготовить загрузку")
	}
	return nil
}

func progressOut() (io.Writer, func()) {
	if isTTY(os.Stderr) {
		return os.Stderr, func() {}
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return os.Stderr, func() {}
	}
	return io.MultiWriter(os.Stderr, tty), func() { tty.Close() }
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

type progressReader struct {
	r     io.Reader
	sent  int64
	total int64
	out   io.Writer
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.sent += int64(n)
	}
	if p.draw(err) {
		fmt.Fprint(p.out, formatBar(p.sent, p.total))
		p.last = time.Now()
	}
	return n, err
}

func (p *progressReader) draw(err error) bool {
	if err != nil || p.sent >= p.total {
		return true
	}
	return p.last.IsZero() || time.Since(p.last) >= 100*time.Millisecond
}

func formatBar(sent, total int64) string {
	const width = 32
	pct := int64(0)
	filled := int64(0)
	if total > 0 {
		pct = sent * 100 / total
		if pct > 100 {
			pct = 100
		}
		filled = sent * width / total
		if filled > width {
			filled = width
		}
	}
	bar := strings.Repeat("#", int(filled)) + strings.Repeat("-", int(width-filled))
	line := fmt.Sprintf("[%s] %3d%% %d/%d", bar, pct, sent, total)
	const cols = 72
	if len(line) < cols {
		line += strings.Repeat(" ", cols-len(line))
	}
	return "\r" + line
}
