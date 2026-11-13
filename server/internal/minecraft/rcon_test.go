package minecraft

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseList(t *testing.T) {
	online, max, names, ok := ParseList("There are 2 of a max of 20 players online: Ann, Bob")
	if !ok || online != 2 || max != 20 || strings.Join(names, ",") != "Ann,Bob" {
		t.Fatalf("%d %d %v %v", online, max, names, ok)
	}
	online, max, names, ok = ParseList("В игре 0 из 20 игроков: ")
	if !ok || online != 0 || max != 20 || len(names) != 0 {
		t.Fatalf("%d %d %v %v", online, max, names, ok)
	}
	if _, _, _, ok = ParseList("nope"); ok {
		t.Fatal("parsed")
	}
}

func TestParseMods(t *testing.T) {
	text := "Mod List: \n• JAVA_FML forge.jar : forge (47.4.18) - 1\n• JAVA_FML jei.jar : jei (15.2.0.20) - 1\n"
	mods := ParseMods(text)
	if len(mods) != 2 || mods[0].ID != "forge" || mods[0].Version != "47.4.18" || mods[0].File != "JAVA_FML forge.jar" || mods[1].ID != "jei" {
		t.Fatalf("%+v", mods)
	}
}

func TestCommandJoinsRCONPackets(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		id, typ, body, err := readPacket(conn)
		if err != nil || typ != 3 || body != "secret" {
			errCh <- errString("auth")
			return
		}
		if err := writePacket(conn, id, 2, ""); err != nil {
			errCh <- err
			return
		}
		id, typ, body, err = readPacket(conn)
		if err != nil || typ != 2 || body != "list" {
			errCh <- errString("cmd")
			return
		}
		chunk := strings.Repeat("x", 4096)
		if err := writeFrame(conn, id, 0, chunk); err != nil {
			errCh <- err
			return
		}
		if err := writeFrame(conn, id, 0, "END"); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	s := New("u", "w", 0, host, port, "secret")
	got, err := s.Command("list")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("x", 4096)+"END" {
		t.Fatalf("len %d", len(got))
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestSaveAllWaitsPastShortDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var cmds []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
				id, typ, _, err := readPacket(conn)
				if err != nil || typ != 3 {
					return
				}
				if err := writePacket(conn, id, 2, ""); err != nil {
					return
				}
				id, typ, body, err := readPacket(conn)
				if err != nil || typ != 2 {
					return
				}
				mu.Lock()
				cmds = append(cmds, body)
				mu.Unlock()
				if body == "save-all" {
					time.Sleep(6 * time.Second)
				}
				_ = writePacket(conn, id, 0, "ok")
			}()
		}
	}()
	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	s := New("u", "w", 0, host, port, "secret")
	start := time.Now()
	copied := false
	if err := s.PrepareWorldCopy(func() error {
		mu.Lock()
		cmds = append(cmds, "copy")
		mu.Unlock()
		copied = true
		return nil
	}); err != nil || !copied {
		t.Fatal(err, copied)
	}
	if time.Since(start) < 6*time.Second {
		t.Fatal(time.Since(start))
	}
	mu.Lock()
	got := strings.Join(cmds, ",")
	mu.Unlock()
	want := "say Началось сохранение мира,save-off,save-all,copy,save-on,say Сохранение мира закончилось"
	if got != want {
		t.Fatal(got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
