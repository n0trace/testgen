package cmds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/n0trace/testgen/recoder"

	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/go-delve/delve/service/rpccommon"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	// log is whether to log debug statements.
	log bool
	// logOutput is a comma separated list of components that should produce debug output.
	logOutput string
	// logDest is the file path or file descriptor where logs should go.
	logDest string
	// headless is whether to run without terminal.
	headless bool
	// continueOnStart is whether to continue the process on startup
	continueOnStart bool
	// apiVersion is the requested API version while running headless
	apiVersion int
	// acceptMulti allows multiple clients to connect to the same server
	acceptMulti bool
	// addr is the debugging server listen address.
	addr string
	// initFile is the path to initialization file.
	initFile string
	// buildFlags is the flags passed during compiler invocation.
	buildFlags string
	// workingDir is the working directory for running the program.
	workingDir string
	// checkLocalConnUser is true if the debugger should check that local
	// connections come from the same user that started the headless server
	checkLocalConnUser bool
	// tty is used to provide an alternate TTY for the program you wish to debug.
	tty string
	// disableASLR is used to disable ASLR
	disableASLR bool

	// dapClientAddr is dap subcommand's flag that specifies the address of a DAP client.
	// If it is specified, the dap server starts a debug session by dialing to the client.
	// The dap server will serve only for the debug session.
	dapClientAddr string

	// backend selection
	backend string

	// checkGoVersion is true if the debugger should check the version of Go
	// used to compile the executable and refuse to work on incompatible
	// versions.
	checkGoVersion bool

	// rootCommand is the root of the command tree.
	rootCommand *cobra.Command

	traceAttachPid  int
	traceExecFile   string
	traceTestBinary bool
	traceStackDepth int
	traceUseEBPF    bool

	// redirect specifications for target process
	redirects []string

	allowNonTerminalInteractive bool

	loadConfErr error
	breakpoint  string
)

const dlvCommandLongDesc = `Delve is a source level debugger for Go programs.

Delve enables you to interact with your program by controlling the execution of the process,
evaluating variables, and providing information of thread / goroutine state, CPU register state and more.

The goal of this tool is to provide a simple yet powerful interface for debugging Go programs.

Pass flags to the program you are debugging using ` + "`--`" + `, for example:

` + "`dlv exec ./hello -- server --config conf/config.toml`"

// New returns an initialized command tree.
func New(docCall bool) *cobra.Command {
	buildFlagsDefault := ""
	if runtime.GOOS == "windows" {
		ver, _ := goversion.Installed()
		if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
			// Work-around for https://github.com/golang/go/issues/13154
			buildFlagsDefault = "-ldflags='-linkmode internal'"
		}
	}
	// Main dlv root command.
	rootCommand = &cobra.Command{
		Use:   "dlv",
		Short: "Delve is a debugger for the Go programming language.",
		Long:  dlvCommandLongDesc,
	}
	rootCommand.PersistentFlags().StringVarP(&addr, "listen", "l", "127.0.0.1:0", "Debugging server listen address.")

	rootCommand.PersistentFlags().BoolVarP(&log, "log", "", false, "Enable debugging server logging.")
	rootCommand.PersistentFlags().StringVarP(&logOutput, "log-output", "", "", `Comma separated list of components that should produce debug output (see 'dlv help log')`)
	rootCommand.PersistentFlags().StringVarP(&logDest, "log-dest", "", "", "Writes logs to the specified file or file descriptor (see 'dlv help log').")

	rootCommand.PersistentFlags().BoolVarP(&headless, "headless", "", false, "Run debug server only, in headless mode.")
	rootCommand.PersistentFlags().BoolVarP(&acceptMulti, "accept-multiclient", "", false, "Allows a headless server to accept multiple client connections.")
	rootCommand.PersistentFlags().IntVar(&apiVersion, "api-version", 1, "Selects API version when headless. New clients should use v2. Can be reset via RPCServer.SetApiVersion. See Documentation/api/json-rpc/README.md.")
	rootCommand.PersistentFlags().StringVar(&initFile, "init", "", "Init file, executed by the terminal client.")
	rootCommand.PersistentFlags().StringVar(&buildFlags, "build-flags", buildFlagsDefault, "Build flags, to be passed to the compiler. For example: --build-flags=\"-tags=integration -mod=vendor -cover -v\"")
	rootCommand.PersistentFlags().StringVar(&workingDir, "wd", "", "Working directory for running the program.")
	rootCommand.PersistentFlags().BoolVarP(&checkGoVersion, "check-go-version", "", true, "Exits if the version of Go in use is not compatible (too old or too new) with the version of Delve.")
	rootCommand.PersistentFlags().BoolVarP(&checkLocalConnUser, "only-same-user", "", true, "Only connections from the same user that started this instance of Delve are allowed to connect.")
	rootCommand.PersistentFlags().StringVar(&backend, "backend", "default", `Backend selection (see 'dlv help backend').`)
	rootCommand.PersistentFlags().StringArrayVarP(&redirects, "redirect", "r", []string{}, "Specifies redirect rules for target process (see 'dlv help redirect')")
	rootCommand.PersistentFlags().BoolVar(&allowNonTerminalInteractive, "allow-non-terminal-interactive", false, "Allows interactive sessions of Delve that don't have a terminal as stdin, stdout and stderr")
	rootCommand.PersistentFlags().BoolVar(&disableASLR, "disable-aslr", false, "Disables address space randomization")
	rootCommand.PersistentFlags().StringVar(&breakpoint, "breakpoint", "main.main", "breakpoint")
	// 'trace' subcommand.
	traceCommand := &cobra.Command{
		Use:   "trace [package] regexp",
		Short: "Compile and begin tracing program.",
		Long: `Trace program execution.
	
	The trace sub command will set a tracepoint on every function matching the
	provided regular expression and output information when tracepoint is hit.  This
	is useful if you do not want to begin an entire debug session, but merely want
	to know what functions your process is executing.
	
	The output of the trace sub command is printed to stderr, so if you would like to
	only see the output of the trace operations you can redirect stdout.`,
		Run: traceCmd,
	}
	traceCommand.Flags().IntVarP(&traceAttachPid, "pid", "p", 0, "Pid to attach to.")
	traceCommand.Flags().StringVarP(&traceExecFile, "exec", "e", "", "Binary file to exec and trace.")
	rootCommand.AddCommand(traceCommand)

	// 'exec' subcommand.
	execCommand := &cobra.Command{
		Use:   "exec <path/to/binary>",
		Short: "Execute a precompiled binary, and begin a debug session.",
		Long: `Execute a precompiled binary and begin a debug session.

This command will cause Delve to exec the binary and immediately attach to it to
begin a new debug session. Please note that if the binary was not compiled with
optimizations disabled, it may be difficult to properly debug it. Please
consider compiling debugging binaries with -gcflags="all=-N -l" on Go 1.10
or later, -gcflags="-N -l" on earlier versions of Go.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide a path to a binary")
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(execute(0, args, "", debugger.ExecutingExistingFile, args, buildFlags))
		},
	}
	execCommand.Flags().StringVar(&tty, "tty", "", "TTY to use for the target program")
	execCommand.Flags().BoolVar(&continueOnStart, "continue", false, "Continue the debugged process on start.")
	rootCommand.AddCommand(execCommand)

	// 'debug' subcommand.
	debugCommand := &cobra.Command{
		Use:   "debug [package]",
		Short: "Compile and begin debugging main package in current directory, or the package specified.",
		Long: `Compiles your program with optimizations disabled, starts and attaches to it.

By default, with no arguments, Delve will compile the 'main' package in the
current directory, and begin to debug it. Alternatively you can specify a
package name and Delve will compile that package instead, and begin a new debug
session.`,
		Run: debugCmd,
	}
	debugCommand.Flags().String("output", "./__debug_bin", "Output path for the binary.")
	debugCommand.Flags().BoolVar(&continueOnStart, "continue", false, "Continue the debugged process on start.")
	debugCommand.Flags().StringVar(&tty, "tty", "", "TTY to use for the target program")
	rootCommand.AddCommand(debugCommand)
	return rootCommand
}

func buildBinary(cmd *cobra.Command, args []string, isTest bool) (string, bool) {
	debugname, err := filepath.Abs(cmd.Flag("output").Value.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return "", false
	}

	if isTest {
		err = gobuild.GoTestBuild(debugname, args, buildFlags)
	} else {
		err = gobuild.GoBuild(debugname, args, buildFlags)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return "", false
	}
	return debugname, true
}

func debugCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		dlvArgs, targetArgs := splitArgs(cmd, args)
		debugname, ok := buildBinary(cmd, dlvArgs, false)
		if !ok {
			return 1
		}
		defer gobuild.Remove(debugname)
		processArgs := append([]string{debugname}, targetArgs...)
		return execute(0, processArgs, "", debugger.ExecutingGeneratedFile, dlvArgs, buildFlags)
	}()
	os.Exit(status)
}

func execute(attachPid int, processArgs []string, coreFile string, kind debugger.ExecuteKind, dlvArgs []string, buildFlags string) int {
	if err := logflags.Setup(log, logOutput, logDest); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer logflags.Close()
	if loadConfErr != nil {
		logflags.DebuggerLogger().Errorf("%v", loadConfErr)
	}

	if headless && (initFile != "") {
		fmt.Fprint(os.Stderr, "Warning: init file ignored with --headless\n")
	}
	if continueOnStart {
		if !headless {
			fmt.Fprint(os.Stderr, "Error: --continue only works with --headless; use an init file\n")
			return 1
		}
		if !acceptMulti {
			fmt.Fprint(os.Stderr, "Error: --continue requires --accept-multiclient\n")
			return 1
		}
	}

	if !headless && acceptMulti {
		fmt.Fprint(os.Stderr, "Warning accept-multi: ignored\n")
		// acceptMulti won't work in normal (non-headless) mode because we always
		// call server.Stop after the terminal client exits.
		acceptMulti = false
	}

	if !headless && !allowNonTerminalInteractive {
		for _, f := range []struct {
			name string
			file *os.File
		}{{"Stdin", os.Stdin}, {"Stdout", os.Stdout}, {"Stderr", os.Stderr}} {
			if f.file == nil {
				continue
			}
			if !isatty.IsTerminal(f.file.Fd()) {
				fmt.Fprintf(os.Stderr, "%s is not a terminal, use '-r' to specify redirects for the target process or --allow-non-terminal-interactive=true if you really want to specify a redirect for Delve\n", f.name)
				return 1
			}
		}
	}

	if len(redirects) > 0 && tty != "" {
		fmt.Fprintf(os.Stderr, "Can not use -r and --tty together\n")
		return 1
	}

	redirects, err := parseRedirects(redirects)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	var listener net.Listener
	var clientConn net.Conn

	// Make a TCP listener
	if headless {
		listener, err = net.Listen("tcp", addr)
	} else {
		listener, clientConn = service.ListenerPipe()
	}
	if err != nil {
		fmt.Printf("couldn't start listener: %s\n", err)
		return 1
	}
	defer listener.Close()

	var server service.Server

	disconnectChan := make(chan struct{})

	if workingDir == "" {
		workingDir = "."
	}

	// Create and start a debugger server
	switch apiVersion {
	case 1, 2:
		server = rpccommon.NewServer(&service.Config{
			Listener:           listener,
			ProcessArgs:        processArgs,
			AcceptMulti:        acceptMulti,
			APIVersion:         apiVersion,
			CheckLocalConnUser: checkLocalConnUser,
			DisconnectChan:     disconnectChan,
			Debugger: debugger.Config{
				AttachPid:      attachPid,
				WorkingDir:     workingDir,
				Backend:        backend,
				CoreFile:       coreFile,
				Foreground:     headless && tty == "",
				Packages:       dlvArgs,
				BuildFlags:     buildFlags,
				ExecuteKind:    kind,
				CheckGoVersion: checkGoVersion,
				TTY:            tty,
				Redirects:      redirects,
				DisableASLR:    disableASLR,
			},
		})
	default:
		fmt.Printf("Unknown API version: %d\n", apiVersion)
		return 1
	}

	if err = server.Run(); err != nil {
		if err == api.ErrNotExecutable {
			switch kind {
			case debugger.ExecutingGeneratedFile:
				fmt.Fprintln(os.Stderr, "Can not debug non-main package")
				return 1
			case debugger.ExecutingExistingFile:
				fmt.Fprintf(os.Stderr, "%s is not executable\n", processArgs[0])
				return 1
			default:
				// fallthrough
			}
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var status int
	if headless {
		if continueOnStart {
			client := rpc2.NewClient(listener.Addr().String())
			client.Disconnect(true) // true = continue after disconnect
		}
		waitForDisconnectSignal(disconnectChan)
		err = server.Stop()
		if err != nil {
			fmt.Println(err)
		}

		return status
	}

	return connect(listener.Addr().String(), clientConn, kind)
}

// waitForDisconnectSignal is a blocking function that waits for either
// a SIGINT (Ctrl-C) or SIGTERM (kill -15) OS signal or for disconnectChan
// to be closed by the server when the client disconnects.
// Note that in headless mode, the debugged process is foregrounded
// (to have control of the tty for debugging interactive programs),
// so SIGINT gets sent to the debuggee and not to delve.
func waitForDisconnectSignal(disconnectChan chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	if runtime.GOOS == "windows" {
		// On windows Ctrl-C sent to inferior process is delivered
		// as SIGINT to delve. Ignore it instead of stopping the server
		// in order to be able to debug signal handlers.
		go func() {
			for range ch {
			}
		}()
		<-disconnectChan
	} else {
		select {
		case <-ch:
		case <-disconnectChan:
		}
	}
}

func splitArgs(cmd *cobra.Command, args []string) ([]string, []string) {
	if cmd.ArgsLenAtDash() >= 0 {
		return args[:cmd.ArgsLenAtDash()], args[cmd.ArgsLenAtDash():]
	}
	return args, []string{}
}

func traceCmd(cmd *cobra.Command, args []string) {
	var regexp string
	var processArgs []string
	var debugname string
	dlvArgs, targetArgs := splitArgs(cmd, args)
	dlvArgsLen := len(dlvArgs)
	if dlvArgsLen == 1 {
		regexp = args[0]
		dlvArgs = dlvArgs[0:0]
	} else if dlvArgsLen >= 2 {
		regexp = dlvArgs[dlvArgsLen-1]
		dlvArgs = dlvArgs[:dlvArgsLen-1]
	}
	debugname = traceExecFile
	processArgs = append([]string{debugname}, targetArgs...)

	// Make a local in-memory connection that client and server use to communicate
	listener, clientConn := service.ListenerPipe()
	defer listener.Close()

	if workingDir == "" {
		workingDir = "."
	}

	// Create and start a debug server
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: processArgs,
		APIVersion:  2,
		Debugger: debugger.Config{
			AttachPid:      traceAttachPid,
			WorkingDir:     workingDir,
			Backend:        backend,
			CheckGoVersion: checkGoVersion,
		},
	})
	if err := server.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	client := rpc2.NewClientFromConn(clientConn)
	_ = client
	_ = regexp
}

func parseRedirects(redirects []string) ([3]string, error) {
	r := [3]string{}
	names := [3]string{"stdin", "stdout", "stderr"}
	for _, redirect := range redirects {
		idx := 0
		for i, name := range names {
			pfx := name + ":"
			if strings.HasPrefix(redirect, pfx) {
				idx = i
				redirect = redirect[len(pfx):]
				break
			}
		}
		if r[idx] != "" {
			return r, fmt.Errorf("redirect error: %s redirected twice", names[idx])
		}
		r[idx] = redirect
	}
	return r, nil
}

func connect(addr string, clientConn net.Conn, kind debugger.ExecuteKind) int {
	// Create and start a terminal - attach to running instance
	var client *rpc2.RPCClient
	if clientConn != nil {
		client = rpc2.NewClientFromConn(clientConn)
	} else {
		client = rpc2.NewClient(addr)
	}

	r, err := recoder.NewRecorder(client, breakpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	result, _ := r.R()
	bs, _ := json.Marshal(result)
	fmt.Println(string(bs))
	return 0
}
