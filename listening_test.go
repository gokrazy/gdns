package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/netlink"
)

func TestListenaddrs(t *testing.T) {
	const ns = "gdns0"
	if os.Getenv("LISTENING_PROCESS") == "1" {
		if _, err := net.Listen("tcp", "[::]:1313"); err != nil {
			log.Fatal(err)
		}
		f := os.NewFile(3, "/proc/self/fd/3")
		fmt.Fprintf(f, "%d", os.Getpid())
		f.Close()                 // signal readiness
		time.Sleep(1 * time.Hour) // wait until killed
		return
	}
	add := exec.Command("ip", "netns", "add", ns)
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		t.Fatalf("%v: %v", add.Args, err)
	}
	defer exec.Command("ip", "netns", "delete", ns).Run()

	commands := strings.Join([]string{
		"link add lan0 type dummy",
		"link set lan0 address 02:73:53:00:b0:0c",
		"addr add dev lan0 2001:db8::1", // RFC3849 documentation
	}, "\n")
	setup := exec.Command("ip", "-netns", ns, "-batch", "-")
	setup.Stdin = strings.NewReader(commands)
	setup.Stderr = os.Stderr
	if err := setup.Run(); err != nil {
		t.Fatalf("%v: %v", commands, err)
	}

	// ip(8) places network namespaces in C.NETNS_RUN_DIR, but offers no way to
	// obtain that directory path without cgo. Hence, the listening process,
	// which is placed in network namespace ns via ip(8) sends us its pid, with
	// which we can open the namespace via /proc.
	cmd := exec.Command("ip", "netns", "exec", ns, os.Args[0], "-test.run=^TestListenaddrs$")
	cmd.Env = append(os.Environ(), "LISTENING_PROCESS=1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{w}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	// Close the write end of the pipe in the parent process.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Block until listening.
	b, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join("/proc", string(b), "ns", "net"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := listenaddrs(&netlink.Config{NetNS: int(f.Fd())})
	if err != nil {
		t.Fatal(err)
	}
	want := []listenAddr{
		{
			Addr:    net.ParseIP("::"),
			Port:    1313,
			Cmdline: os.Args[0],
		},
	}
	opts := []cmp.Option{
		cmp.FilterPath(func(p cmp.Path) bool {
			return p.Last().String() == ".Inode"
		}, cmp.Ignore()),
	}
	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Errorf("listenaddrs: unexpected diff (-want +got):\n%s", diff)
	}
}
