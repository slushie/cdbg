package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

var (
	exitCode  int
	container string
	tty       = true
	address   = "/var/run/containerd/containerd.sock"
	image     = "docker.io/library/ubuntu:bionic"
	command   = []string{"/bin/bash", "-l"}
	id        = "cdbg"
)

func fail(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprintln(os.Stderr)
	_, file, line, _ := runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "\t%s:%d\n", file, line)
	if exitCode == 0 {
		exitCode = 1
	}
	runtime.Goexit()
}

func main() {
	defer os.Exit(exitCode)

	flag.StringVar(&image, "image", image, "Debug image name")
	flag.StringVar(&address, "address", address, "Address of containerd")
	flag.StringVar(&id, "id", id, "Unique ID for debug container")
	flag.BoolVar(&tty, "tty", tty, "Allocate a TTY for the debug container")

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fail("no container specified")
	}
	container = args[0]
	if len(args) > 1 {
		command = args[1:]
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "moby")
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, os.Kill)
	go func() {
		sig := <-signals
		fmt.Println(sig)
		cancel()
		ctx, cancel = context.WithCancel(context.Background())
		ctx = namespaces.WithNamespace(ctx, "moby")
	}()

	// create client
	client, err := containerd.New(address)
	if err != nil {
		fail("connect: %v", err)
	}

	// fetch target container data
	c, err := client.LoadContainer(ctx, container)
	if err != nil {
		fail("load container: %s: %v", container, err)
	}
	spec, err := c.Spec(ctx)
	if err != nil {
		fail("spec: %v", err)
	}
	t, err := c.Task(ctx, nil)
	if err != nil {
		fail("target task: %v", err)
	}

	// pull debug container image
	i, err := client.Pull(ctx, image, containerd.WithPullUnpack)
	if err != nil {
		fail("pull: %s: %v", image, err)
	}
	diffs, err := i.RootFS(ctx)
	if err != nil {
		fail("rootFS: %v", err)
	}
	digest := identity.ChainID(diffs)

	// create debug image snapshot path
	ss := client.SnapshotService(containerd.DefaultSnapshotter)
	snap, err := ss.Stat(ctx, digest.String())
	if err != nil {
		fail("stat: %s: %v", digest.String(), err)
	}
	mounts, err := ss.View(ctx, id, snap.Name)
	if err != nil {
		fail("view: %s: %v", snap.Name, err)
	}
	defer func() {
		err := ss.Remove(ctx, id)
		if err != nil {
			fail("remove: %v", err)
		}
	}()

	// create scratch workspace
	workDir, err := ioutil.TempDir("", "cdbg")
	if err != nil {
		fail("temp dir: %v", err)
	}
	defer os.RemoveAll(workDir)
	dbgRoot := filepath.Join(workDir, "dbg")
	err = os.MkdirAll(dbgRoot, 0777)
	if err != nil {
		fail("dbg dir: %v", err)
	}

	// mount debug image snapshot into workspace
	err = mount.All(mounts, dbgRoot)
	if err != nil {
		fail("mount all: %+v: %v", mounts, err)
	}
	defer mount.UnmountAll(dbgRoot, 0)

	// overlay of workspace snapshot over target container fs
	overlay := mount.Mount{
		Type:   "overlay",
		Source: "overlay",
		Options: []string{
			fmt.Sprintf("lowerdir=%s:%s", dbgRoot, spec.Root.Path),
		},
	}
	root := filepath.Join(workDir, "root")
	err = os.MkdirAll(root, 0777)
	if err != nil {
		fail("root: %v", err)
	}
	err = overlay.Mount(root)
	if err != nil {
		fail("mount: overlay %+v: %v", overlay, err)
	}
	defer func() {
		err = mount.UnmountAll(root, 0)
		if err != nil {
			fail("unmount: %s: %v", root, err)
		}
	}()

	// create debug container in target pid space
	dbgSpec := oci.Compose(
		oci.WithDefaultSpec(),
		oci.WithRootFSPath(root),
		oci.WithTTY,
		oci.WithImageConfigArgs(i, command),
		// TODO add CAP_SYS_PTRACE
		oci.WithLinuxNamespace(specs.LinuxNamespace{
			Type: "pid",
			Path: fmt.Sprintf("/proc/%d/ns/pid", t.Pid()),
		}),
	)
	dbg, err := client.NewContainer(ctx, id,
		containerd.WithNewSpec(dbgSpec))
	if err != nil {
		fail("create: %v", err)
	}
	defer func() {
		err := dbg.Delete(ctx)
		if err != nil {
			fail("delete dbg: %v", err)
		}
	}()

	// create task for debug container with tty
	var con console.Console
	opt := []cio.Opt{cio.WithStdio}
	if tty {
		con = console.Current()
		defer con.Reset()
		err = con.SetRaw()
		if err != nil {
			fail("console: %v", err)
		}
		fifos := filepath.Join(workDir, "fifos")
		err = os.MkdirAll(fifos, 0777)
		if err != nil {
			fail("fifos: %v", err)
		}
		opt = []cio.Opt{
			cio.WithTerminal,
			cio.WithStreams(con, con, nil),
			cio.WithFIFODir(fifos),
		}
	}
	t, err = dbg.NewTask(ctx, cio.NewCreator(opt...))
	if err != nil {
		fail("task: %v", err)
	}
	defer func() {
		_, err := t.Delete(ctx, containerd.WithProcessKill)
		if err != nil {
			fail("delete task: %v", err)
		}
	}()
	if tty {
		err := HandleConsoleResize(ctx, t, con)
		if err != nil {
			fail("resize: %v", err)
		}
	}

	// run the process and wait for termination
	exit, err := t.Wait(ctx)
	if err != nil {
		fail("wait: %v", err)
	}
	err = t.Start(ctx)
	if err != nil {
		fail("start: %v", err)
	}
	status := <-exit
	exitCode = int(status.ExitCode())
	fmt.Println("done")
}

// HandleConsoleResize resizes the console
func HandleConsoleResize(ctx context.Context, task containerd.Task, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
		log.G(ctx).WithError(err).Error("resize pty")
	}
	s := make(chan os.Signal, 16)
	signal.Notify(s, unix.SIGWINCH)
	go func() {
		for range s {
			size, err := con.Size()
			if err != nil {
				log.G(ctx).WithError(err).Error("get pty size")
				continue
			}
			if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
				log.G(ctx).WithError(err).Error("resize pty")
			}
		}
	}()
	return nil
}
