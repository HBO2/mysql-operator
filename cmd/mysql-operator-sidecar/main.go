/*
Copyright 2018 Pressinfra SRL

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/presslabs/mysql-operator/pkg/sidecar/appclone"
	"github.com/presslabs/mysql-operator/pkg/sidecar/appconf"
	"github.com/presslabs/mysql-operator/pkg/sidecar/apphelper"
	"github.com/presslabs/mysql-operator/pkg/sidecar/apptakebackup"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("sidecar")

func main() {
	logf.SetLogger(logf.ZapLogger(true))
	stopCh := SetupSignalHandler()

	cmd := &cobra.Command{
		Use:   "mysql-operator-sidecar",
		Short: fmt.Sprintf("Helper for mysql operator."),
		Long:  `mysql-operator-sidecar: helper for config pods`,
		Run: func(cmd *cobra.Command, args []string) {
			log.Error(nil, "you run mysql-operator-sidecar, see help section")
			os.Exit(1)

		},
	}

	confCmd := &cobra.Command{
		Use:   "init-configs",
		Short: "Init subcommand, for init files.",
		Run: func(cmd *cobra.Command, args []string) {
			err := appconf.RunConfigCommand(stopCh)
			if err != nil {
				log.Error(err, "init command failed")
				os.Exit(1)
			}
		},
	}
	cmd.AddCommand(confCmd)

	cloneCmd := &cobra.Command{
		Use:   "clone",
		Short: "Clone data from a bucket or prior node.",
		Run: func(cmd *cobra.Command, args []string) {
			err := appclone.RunCloneCommand(stopCh)
			if err != nil {
				log.Error(err, "clone command failed")
				os.Exit(1)
			}
		},
	}
	cmd.AddCommand(cloneCmd)

	helperCmd := &cobra.Command{
		Use:   "run",
		Short: "Configs mysql users, replication, and serve backups.",
		Run: func(cmd *cobra.Command, args []string) {
			err := apphelper.RunRunCommand(stopCh)
			if err != nil {
				log.Error(err, "run command failed")
				os.Exit(1)
			}
		},
	}
	cmd.AddCommand(helperCmd)

	takeBackupCmd := &cobra.Command{
		Use:   "take-backup-to",
		Short: "Take a backup from node and push it to rclone path.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("require two arguments. source host and destination bucket")
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			err := apptakebackup.RunTakeBackupCommand(stopCh, args[0], args[1])
			if err != nil {
				log.Error(err, "take backup command failed")
				os.Exit(1)

			}
		},
	}
	cmd.AddCommand(takeBackupCmd)

	cmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
	if err := flag.CommandLine.Parse([]string{}); err != nil {
		log.Error(err, "failed to parse args")
		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		log.Error(err, "failed to execute command", "cmd", cmd)
		os.Exit(1)
	}
}

var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
var onlyOneSignalHandler = make(chan struct{})

// SetupSignalHandler registered for SIGTERM and SIGINT. A stop channel is returned
// which is closed on one of these signals. If a second signal is caught, the program
// is terminated with exit code 1.
func SetupSignalHandler() (stopCh <-chan struct{}) {
	close(onlyOneSignalHandler) // panics when called twice

	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		close(stop)
		fmt.Println("Press C-c again to exit.")
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return stop
}
