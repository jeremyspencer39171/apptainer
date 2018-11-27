// Copyright (c) 2018, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	osignal "os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/kr/pty"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"github.com/spf13/cobra"
	"github.com/sylabs/singularity/internal/pkg/buildcfg"
	"github.com/sylabs/singularity/internal/pkg/instance"
	"github.com/sylabs/singularity/internal/pkg/runtime/engines/config"
	"github.com/sylabs/singularity/internal/pkg/runtime/engines/oci"
	"github.com/sylabs/singularity/internal/pkg/sylog"
	"github.com/sylabs/singularity/internal/pkg/util/exec"
	"github.com/sylabs/singularity/internal/pkg/util/signal"
	"github.com/sylabs/singularity/internal/pkg/util/unix"
	"github.com/sylabs/singularity/pkg/ociruntime"
	"golang.org/x/crypto/ssh/terminal"
)

var bundlePath string
var logPath string
var syncSocketPath string
var emptyProcess bool

func init() {
	SingularityCmd.AddCommand(OciCmd)

	OciCreateCmd.Flags().SetInterspersed(false)
	OciCreateCmd.Flags().StringVarP(&bundlePath, "bundle", "b", "", "specify the OCI bundle path")
	OciCreateCmd.Flags().SetAnnotation("bundle", "argtag", []string{"<path>"})
	OciCreateCmd.Flags().StringVarP(&syncSocketPath, "sync-socket", "s", "", "specify the path to unix socket for state synchronization (internal)")
	OciCreateCmd.Flags().SetAnnotation("sync-socket", "argtag", []string{"<path>"})
	OciCreateCmd.Flags().BoolVar(&emptyProcess, "empty-process", false, "run container without executing container process (eg: for POD container)")
	OciCreateCmd.Flags().StringVarP(&logPath, "log-path", "l", "", "specify the log file path")
	OciCreateCmd.Flags().SetAnnotation("log-path", "argtag", []string{"<path>"})

	OciStartCmd.Flags().SetInterspersed(false)
	OciDeleteCmd.Flags().SetInterspersed(false)
	OciAttachCmd.Flags().SetInterspersed(false)
	OciExecCmd.Flags().SetInterspersed(false)

	OciStateCmd.Flags().SetInterspersed(false)
	OciStateCmd.Flags().StringVarP(&syncSocketPath, "sync-socket", "s", "", "specify the path to unix socket for state synchronization (internal)")
	OciStateCmd.Flags().SetAnnotation("sync-socket", "argtag", []string{"<path>"})

	OciKillCmd.Flags().SetInterspersed(false)
	OciKillCmd.Flags().StringVarP(&stopSignal, "signal", "s", "", "signal sent to the container (default SIGTERM)")

	OciRunCmd.Flags().SetInterspersed(false)
	OciRunCmd.Flags().StringVarP(&bundlePath, "bundle", "b", "", "specify the OCI bundle path")
	OciRunCmd.Flags().SetAnnotation("bundle", "argtag", []string{"<path>"})
	OciRunCmd.Flags().StringVarP(&logPath, "log-path", "l", "", "specify the log file path")
	OciRunCmd.Flags().SetAnnotation("log-path", "argtag", []string{"<path>"})

	OciCmd.AddCommand(OciStartCmd)
	OciCmd.AddCommand(OciCreateCmd)
	OciCmd.AddCommand(OciRunCmd)
	OciCmd.AddCommand(OciDeleteCmd)
	OciCmd.AddCommand(OciKillCmd)
	OciCmd.AddCommand(OciStateCmd)
	OciCmd.AddCommand(OciAttachCmd)
	OciCmd.AddCommand(OciExecCmd)
}

// OciCreateCmd represents oci create command
var OciCreateCmd = &cobra.Command{
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociCreate(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "create",
	Short:   "oci create",
	Long:    "oci create",
	Example: "oci create",
}

// OciRunCmd allow to create/start in row
var OciRunCmd = &cobra.Command{
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociRun(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "run",
	Short:   "oci run",
	Long:    "oci run",
	Example: "oci run",
}

// OciStartCmd represents oci start command
var OciStartCmd = &cobra.Command{
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociStart(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "start",
	Short:   "oci start",
	Long:    "oci start",
	Example: "oci start",
}

// OciDeleteCmd represents oci start command
var OciDeleteCmd = &cobra.Command{
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociDelete(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "delete",
	Short:   "oci delete",
	Long:    "oci delete",
	Example: "oci delete",
}

// OciKillCmd represents oci start command
var OciKillCmd = &cobra.Command{
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 1 && args[1] != "" {
			stopSignal = args[1]
		}
		if err := ociKill(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "kill",
	Short:   "oci kill",
	Long:    "oci kill",
	Example: "oci kill",
}

// OciStateCmd represents oci start command
var OciStateCmd = &cobra.Command{
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociState(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "state",
	Short:   "oci state",
	Long:    "oci state",
	Example: "oci state",
}

// OciAttachCmd represents oci start command
var OciAttachCmd = &cobra.Command{
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociAttach(args[0]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "attach",
	Short:   "oci attach",
	Long:    "oci attach",
	Example: "oci attach",
}

// OciExecCmd represents oci exec command
var OciExecCmd = &cobra.Command{
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := ociExec(args[0], args[1:]); err != nil {
			sylog.Fatalf("%s", err)
		}
	},
	Use:     "exec",
	Short:   "oci exec",
	Long:    "oci exec",
	Example: "oci exec",
}

// OciCmd singularity oci runtime
var OciCmd = &cobra.Command{
	Run:                   nil,
	DisableFlagsInUseLine: true,

	Use:     "oci",
	Short:   "oci",
	Long:    "oci",
	Example: "oci",
}

func getCommonConfig(containerID string) (*config.Common, error) {
	commonConfig := config.Common{
		EngineConfig: &oci.EngineConfig{},
	}

	file, err := instance.Get(containerID)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(file.Config, &commonConfig); err != nil {
		return nil, err
	}

	return &commonConfig, nil
}

func getEngineConfig(containerID string) (*oci.EngineConfig, error) {
	commonConfig := config.Common{
		EngineConfig: &oci.EngineConfig{},
	}

	file, err := instance.Get(containerID)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(file.Config, &commonConfig); err != nil {
		return nil, err
	}

	return commonConfig.EngineConfig.(*oci.EngineConfig), nil
}

func getState(containerID string) (*specs.State, error) {
	engineConfig, err := getEngineConfig(containerID)
	if err != nil {
		return nil, err
	}
	return &engineConfig.State, nil
}

func resize(controlSocket string, oversized bool) {
	ctrl := &ociruntime.Control{}
	ctrl.ConsoleSize = &specs.Box{}

	c, err := unix.Dial(controlSocket)
	if err != nil {
		sylog.Errorf("failed to connect to control socket")
		return
	}
	defer c.Close()

	rows, cols, err := pty.Getsize(os.Stdin)
	if err != nil {
		sylog.Errorf("terminal resize error: %s", err)
		return
	}

	ctrl.ConsoleSize.Height = uint(rows)
	ctrl.ConsoleSize.Width = uint(cols)

	if oversized {
		ctrl.ConsoleSize.Height++
		ctrl.ConsoleSize.Width++
	}

	enc := json.NewEncoder(c)
	if err != nil {
		sylog.Errorf("%s", err)
		return
	}

	if err := enc.Encode(ctrl); err != nil {
		sylog.Errorf("%s", err)
		return
	}
}

func attach(attachSocket, controlSocket string, engineConfig *oci.EngineConfig) error {
	var ostate *terminal.State
	hasTerminal := engineConfig.OciConfig.Process.Terminal

	a, err := unix.Dial(attachSocket)
	if err != nil {
		return err
	}
	defer a.Close()

	if hasTerminal {
		ostate, _ = terminal.MakeRaw(0)
		resize(controlSocket, true)
		resize(controlSocket, false)

		go func() {
			// catch SIGWINCH signal for terminal resize
			signals := make(chan os.Signal, 1)
			osignal.Notify(signals, syscall.SIGWINCH)

			for {
				<-signals
				resize(controlSocket, false)
			}
		}()
	} else {
		go func() {
			// catch all signals and forward to container process
			signals := make(chan os.Signal, 1)
			pid := engineConfig.State.Pid
			osignal.Notify(signals)

			for {
				s := <-signals
				syscall.Kill(pid, s.(syscall.Signal))
			}
		}()
	}

	var wg sync.WaitGroup

	wg.Add(1)

	// Pipe session to bash and visa-versa
	go func() {
		io.Copy(os.Stdout, a)
		wg.Done()
	}()

	go func() {
		io.Copy(a, os.Stdout)
	}()

	wg.Wait()

	if hasTerminal {
		fmt.Printf("\r")
		return terminal.Restore(0, ostate)
	}

	return nil
}

func exitContainer(containerID string, syncSocketPath string) {
	state, err := getState(containerID)
	if err != nil {
		sylog.Errorf("%s", err)
		os.Exit(1)
	}

	if _, ok := state.Annotations[ociruntime.AnnotationExitCode]; ok {
		code := state.Annotations[ociruntime.AnnotationExitCode]
		exitCode, err := strconv.Atoi(code)
		if err != nil {
			sylog.Errorf("%s", err)
			defer os.Exit(1)
		} else {
			defer os.Exit(exitCode)
		}
	}

	if syncSocketPath != "" {
		if err := ociDelete(containerID); err != nil {
			sylog.Errorf("%s", err)
		}
	}
}

func ociRun(containerID string) error {
	dir, err := instance.GetDirPrivileged(containerID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	syncSocketPath = filepath.Join(dir, "run.sock")

	l, err := net.Listen("unix", syncSocketPath)
	if err != nil {
		os.Remove(syncSocketPath)
		return err
	}

	defer l.Close()
	defer exitContainer(containerID, syncSocketPath)
	defer os.Remove(syncSocketPath)

	if err := ociCreate(containerID); err != nil {
		return err
	}

	start := make(chan string, 1)

	go func() {
		var state specs.State

		for {
			c, err := l.Accept()
			if err != nil {
				return
			}

			dec := json.NewDecoder(c)
			if err := dec.Decode(&state); err != nil {
				return
			}

			c.Close()

			switch state.Status {
			case "created":
				if err := ociStart(containerID); err != nil {
					return
				}
			case "running":
				start <- state.Annotations[ociruntime.AnnotationAttachSocket]
			case "stopped":
				return
			}
		}
	}()

	attachSocket := <-start

	engineConfig, err := getEngineConfig(containerID)
	if err != nil {
		return err
	}

	controlSocket, ok := engineConfig.State.Annotations[ociruntime.AnnotationControlSocket]
	if !ok {
		return fmt.Errorf("control socket not available, container state: %s", engineConfig.State.Status)
	}

	return attach(attachSocket, controlSocket, engineConfig)
}

func ociAttach(containerID string) error {
	engineConfig, err := getEngineConfig(containerID)
	if err != nil {
		return err
	}

	state := engineConfig.GetState()

	attachSocket, ok := state.Annotations[ociruntime.AnnotationAttachSocket]
	if !ok {
		return fmt.Errorf("attach socket not available, container state: %s", state.Status)
	}
	controlSocket, ok := state.Annotations[ociruntime.AnnotationControlSocket]
	if !ok {
		return fmt.Errorf("control socket not available, container state: %s", state.Status)
	}

	defer exitContainer(containerID, "")

	return attach(attachSocket, controlSocket, engineConfig)
}

func ociStart(containerID string) error {
	state, err := getState(containerID)
	if err != nil {
		return err
	}

	if state.Status != "created" {
		return fmt.Errorf("container %s is not created", containerID)
	}

	// send SIGCONT signal to the instance
	if err := syscall.Kill(state.Pid, syscall.SIGCONT); err != nil {
		return err
	}
	return nil
}

func ociKill(containerID string) error {
	// send signal to the instance
	state, err := getState(containerID)
	if err != nil {
		return err
	}

	if state.Status != "created" && state.Status != "running" {
		return fmt.Errorf("container %s is nor created nor running", containerID)
	}

	sig := syscall.SIGTERM

	if stopSignal != "" {
		sig, err = signal.Convert(stopSignal)
		if err != nil {
			return err
		}
	}

	return syscall.Kill(state.Pid, sig)
}

func ociDelete(containerID string) error {
	engineConfig, err := getEngineConfig(containerID)
	if err != nil {
		return err
	}

	if engineConfig.State.Status != "stopped" {
		return fmt.Errorf("container is not stopped")
	}

	hooks := engineConfig.OciConfig.Hooks
	if hooks != nil {
		for _, h := range hooks.Poststop {
			if err := exec.Hook(&h, &engineConfig.State); err != nil {
				sylog.Warningf("%s", err)
			}
		}
	}

	// remove instance files
	file, err := instance.Get(containerID)
	if err != nil {
		return err
	}
	return file.Delete()
}

func ociState(containerID string) error {
	// query instance files and returns state
	state, err := getState(containerID)
	if err != nil {
		return err
	}
	if syncSocketPath != "" {
		data, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("failed to marshal state data: %s", err)
		} else if err := unix.WriteSocket(syncSocketPath, data); err != nil {
			return err
		}
	} else {
		c, err := json.MarshalIndent(state, "", "\t")
		if err != nil {
			return err
		}
		fmt.Println(string(c))
	}
	return nil
}

func ociCreate(containerID string) error {
	starter := buildcfg.LIBEXECDIR + "/singularity/bin/starter"

	_, err := getState(containerID)
	if err == nil {
		return fmt.Errorf("%s already exists", containerID)
	}

	os.Clearenv()

	absBundle, err := filepath.Abs(bundlePath)
	if err != nil {
		return fmt.Errorf("failed to determine bundle absolute path: %s", err)
	}

	if err := os.Chdir(absBundle); err != nil {
		return fmt.Errorf("failed to change directory to %s: %s", absBundle, err)
	}

	engineConfig := oci.NewConfig()
	generator := generate.Generator{Config: &engineConfig.OciConfig.Spec}
	engineConfig.SetBundlePath(absBundle)
	engineConfig.SetLogPath(logPath)

	// load config.json from bundle path
	configJSON := filepath.Join(bundlePath, "config.json")
	fb, err := os.Open(configJSON)
	if err != nil {
		return fmt.Errorf("failed to open %s: %s", configJSON, err)
	}

	data, err := ioutil.ReadAll(fb)
	if err != nil {
		return fmt.Errorf("failed to read %s: %s", configJSON, err)
	}

	fb.Close()

	if err := json.Unmarshal(data, generator.Config); err != nil {
		return fmt.Errorf("failed to parse %s: %s", configJSON, err)
	}

	Env := []string{sylog.GetEnvVar(), "SRUNTIME=oci"}

	engineConfig.EmptyProcess = emptyProcess
	engineConfig.SyncSocket = syncSocketPath

	commonConfig := &config.Common{
		ContainerID:  containerID,
		EngineName:   "oci",
		EngineConfig: engineConfig,
	}

	configData, err := json.Marshal(commonConfig)
	if err != nil {
		sylog.Fatalf("%s", err)
	}

	procName := fmt.Sprintf("Singularity OCI %s", containerID)
	cmd, err := exec.PipeCommand(starter, []string{procName}, Env, configData)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func ociExec(containerID string, cmdArgs []string) error {
	starter := buildcfg.LIBEXECDIR + "/singularity/bin/starter"

	commonConfig, err := getCommonConfig(containerID)
	if err != nil {
		return fmt.Errorf("%s doesn't exist", containerID)
	}

	engineConfig := commonConfig.EngineConfig.(*oci.EngineConfig)

	engineConfig.Exec = true
	engineConfig.OciConfig.SetProcessArgs(cmdArgs)

	os.Clearenv()

	configData, err := json.Marshal(commonConfig)
	if err != nil {
		sylog.Fatalf("%s", err)
	}

	Env := []string{sylog.GetEnvVar(), "SRUNTIME=oci"}

	procName := fmt.Sprintf("Singularity OCI %s", containerID)
	return exec.Pipe(starter, []string{procName}, Env, configData)
}
