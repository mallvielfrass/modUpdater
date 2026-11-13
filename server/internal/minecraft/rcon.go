package minecraft

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

var ErrRCON = errors.New("rcon_failed")

const saveAllTimeout = 45 * time.Second

func SayCommand(message, warning string) string {
	if message == "" {
		message = warning
	}
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == 0 {
			return -1
		}
		return r
	}, message)
	rs := []rune(message)
	if len(rs) > 200 {
		message = string(rs[:200])
	}
	return "say " + message
}

func (s *Service) sayRCON(command string) error {
	_, err := s.execRCON(command)
	return err
}

func (s *Service) Command(command string) (string, error) {
	if s.Exec == nil {
		return "", ErrRCON
	}
	return s.Exec(command)
}

func (s *Service) rconConfigured() bool {
	return s != nil && s.host != "" && s.port > 0 && s.password != ""
}

func (s *Service) PrepareWorldCopy(copy func() error) error {
	if copy == nil {
		return nil
	}
	if !s.rconConfigured() || s.Say == nil {
		return copy()
	}
	if err := s.Say(SayCommand("Началось сохранение мира", "")); err != nil {
		return copy()
	}
	if s.Exec == nil {
		err := copy()
		_ = s.Say(SayCommand("Сохранение мира закончилось", ""))
		return err
	}
	if _, err := s.Exec("save-off"); err != nil {
		err = copy()
		_ = s.Say(SayCommand("Сохранение мира закончилось", ""))
		return err
	}
	var copyErr error
	func() {
		defer func() {
			_, _ = s.Exec("save-on")
			_ = s.Say(SayCommand("Сохранение мира закончилось", ""))
		}()
		_, _ = s.Exec("save-all")
		copyErr = copy()
	}()
	return copyErr
}

func (s *Service) execRCON(command string) (string, error) {
	wait := 5 * time.Second
	if command == "save-all" {
		wait = saveAllTimeout
	}
	return s.execRCONWithin(command, wait)
}

func (s *Service) execRCONWithin(command string, wait time.Duration) (string, error) {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "", ErrRCON
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(wait))
	if err := writePacket(conn, 1, 3, s.password); err != nil {
		return "", ErrRCON
	}
	authed := false
	for i := 0; i < 4; i++ {
		id, typ, _, err := readPacket(conn)
		if err != nil {
			return "", ErrRCON
		}
		if id == -1 {
			return "", ErrRCON
		}
		if typ == 2 && id == 1 {
			authed = true
			break
		}
	}
	if !authed {
		return "", ErrRCON
	}
	const cmdID int32 = 2
	if err := writePacket(conn, cmdID, 2, command); err != nil {
		return "", ErrRCON
	}
	var out strings.Builder
	for {
		id, _, body, err := readPacket(conn)
		if err != nil || id == -1 {
			return "", ErrRCON
		}
		if id != cmdID {
			continue
		}
		out.WriteString(body)
		if len(body) < 4096 {
			return out.String(), nil
		}
	}
}

func writePacket(w io.Writer, id, typ int32, body string) error {
	if len(body) > 4000 {
		return ErrRCON
	}
	return writeFrame(w, id, typ, body)
}

func writeFrame(w io.Writer, id, typ int32, body string) error {
	if len(body) > 4096 {
		return ErrRCON
	}
	payload := make([]byte, 8+len(body)+2)
	binary.LittleEndian.PutUint32(payload[0:4], uint32(id))
	binary.LittleEndian.PutUint32(payload[4:8], uint32(typ))
	copy(payload[8:], body)
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readPacket(r io.Reader) (int32, int32, string, error) {
	var size int32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return 0, 0, "", err
	}
	if size < 10 || size > 4110 {
		return 0, 0, "", ErrRCON
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, 0, "", err
	}
	id := int32(binary.LittleEndian.Uint32(buf[0:4]))
	typ := int32(binary.LittleEndian.Uint32(buf[4:8]))
	body := buf[8:]
	if i := indexByte(body, 0); i >= 0 {
		body = body[:i]
	}
	return id, typ, string(body), nil
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
