package originals

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestCommandArgv(t *testing.T) {
	for _, tc := range []struct {
		os, ext, action, executable string
		args                        []string
	}{
		{"linux", ".pdf", "file", "xdg-open", []string{"/tmp/-quoted name.pdf"}},
		{"linux", ".go", "file", "gedit", []string{"--", "/tmp/-quoted name.go"}},
		{"linux", "", "folder", "xdg-open", []string{"/tmp/folder"}},
		{"darwin", ".pdf", "file", "open", []string{"--", "/tmp/-quoted name.pdf"}},
		{"darwin", ".txt", "file", "open", []string{"-t", "--", "/tmp/-quoted name.txt"}},
		{"windows", ".pdf", "file", "rundll32.exe", []string{"url.dll,FileProtocolHandler", "/tmp/-quoted name.pdf"}},
		{"windows", ".txt", "file", "notepad.exe", []string{"/tmp/-quoted name.txt"}},
		{"windows", "", "folder", "explorer.exe", []string{"/tmp/folder"}},
	} {
		path := "/tmp/-quoted name" + tc.ext
		if tc.action == "folder" {
			path = "/tmp/folder"
		}
		exe, args, err := Command(tc.os, Action(tc.action), path)
		if err != nil || exe != tc.executable || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s %s: %q %#v %v", tc.os, tc.action, exe, args, err)
		}
	}
}

func TestIdentityBindsRegistrationSnapshotSourceAndDocument(t *testing.T) {
	base := Identity("registration", 1, "hash", "source", "doc-a")
	for _, identity := range []string{Identity("registration", 1, "hash", "source", "doc-b"), Identity("registration", 1, "hash", "other-source", "doc-a"), Identity("new-registration", 1, "hash", "source", "doc-a"), Identity("registration", 2, "hash", "source", "doc-a"), Identity("registration", 1, "new-hash", "source", "doc-a")} {
		if identity == base {
			t.Fatal("identity did not bind all expected provenance fields")
		}
	}
}

func TestResolveConfinement(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	name := `-a "quote".txt`
	if runtime.GOOS == "windows" {
		name = "-a spaced.txt"
	}
	file := filepath.Join(root, "sub", name)
	if err := os.WriteFile(file, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(root, "sub/"+name); err != nil || got != file {
		t.Fatalf("%q %v", got, err)
	}
	for _, rel := range []string{"../outside.txt", "/etc/passwd", `sub\..\outside`, "sub/file:stream.txt", "sub//file.txt"} {
		if _, err := Resolve(root, rel); err == nil {
			t.Errorf("accepted %q", rel)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err == nil {
		if _, err := Resolve(root, "escape/outside.txt"); err == nil {
			t.Fatal("accepted symlink escape")
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "sub/"+name); err == nil {
		t.Fatal("accepted missing file")
	}
}

func TestDisallowedTypesAndInjectedLauncher(t *testing.T) {
	for _, ext := range []string{".exe", ".com", ".msi", ".bat", ".cmd", ".ps1", ".lnk", ".desktop", ".url", ".webloc", ".app"} {
		if AllowedDocument("doc" + ext) {
			t.Errorf("allowed %s", ext)
		}
		if _, _, err := Command("linux", OpenFile, "/tmp/doc"+ext); err == nil {
			t.Errorf("command for %s", ext)
		}
	}
	for _, root := range []string{`\\server\share`, `\\?\C:\data`, `\\.\device`, `C:\Users\you\Notes`} {
		want := root == `C:\Users\you\Notes`
		if got := safeRootForOS(root, "windows"); got != want {
			t.Errorf("safeRootForOS(%q)=%v want %v", root, got, want)
		}
	}
	if name, args, err := Command("linux", OpenFile, "/tmp/script.sh"); err != nil || name != "gedit" || !reflect.DeepEqual(args, []string{"--", "/tmp/script.sh"}) {
		t.Fatalf("script was not routed to text editor: %q %#v %v", name, args, err)
	}
	var name string
	var args []string
	o := Opener{GOOS: "linux", Launch: func(n string, a ...string) error { name = n; args = a; return nil }}
	if err := o.Open(OpenFile, "/tmp/notes.txt"); err != nil {
		t.Fatal(err)
	}
	if name != "gedit" || !reflect.DeepEqual(args, []string{"--", "/tmp/notes.txt"}) {
		t.Fatalf("%q %#v", name, args)
	}
}
