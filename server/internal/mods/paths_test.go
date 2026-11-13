package mods

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeJarNameRejects(t *testing.T) {
	for _, raw := range []string{"../x.jar", "foo/../../x.jar", `foo\..\x.jar`, "a/../b.jar", "foo..bar.jar", "has\"q.jar", "bad\nname.jar"} {
		if _, err := SafeJarName(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	got, err := SafeJarName(`dir/Foo.jar`)
	if err != nil || got != "Foo.jar" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = SafeJarName(`C:\fakepath\Mod.jar`)
	if err != nil || got != "Mod.jar" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestOpenRegularRejectsEscapeSymlinkAndAbsolute(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenRegular(dir, "../../x"); err == nil {
		t.Fatal("accepted archive escape")
	}
	if _, err := OpenRegular(dir, "/abs.jar"); err == nil {
		t.Fatal("accepted absolute name")
	}
	real := filepath.Join(dir, "real.jar")
	if err := os.WriteFile(real, []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "link.jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRegular(dir, "link.jar"); err == nil {
		t.Fatal("opened symlink")
	}
	f, err := OpenRegular(dir, "real.jar")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestResolveModsDirJail(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "data")
	mods := filepath.Join(parent, "mods")
	nested := filepath.Join(data, "mods")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mods, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveModsDir(data, "/"); err == nil {
		t.Fatal("accepted /")
	}
	if _, err := ResolveModsDir(data, data); err == nil {
		t.Fatal("accepted data dir")
	}
	if _, err := ResolveModsDir(data, nested); err == nil {
		t.Fatal("accepted mods dir inside data")
	}
	if _, err := ResolveModsDir(data, parent); err == nil {
		t.Fatal("accepted parent of data")
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveModsDir(data, link); err == nil {
		t.Fatal("accepted symlink to data dir")
	}
	got, err := ResolveModsDir(data, mods)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(mods)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("%s != %s", got, want)
	}
}
