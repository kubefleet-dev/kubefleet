/*
Copyright 2025 The KubeFleet Authors.

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
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kubefleet-dev/kubefleet/tools/fleet/cmd/approve"
	"github.com/kubefleet-dev/kubefleet/tools/fleet/cmd/draincluster"
	"github.com/kubefleet-dev/kubefleet/tools/fleet/cmd/uncordoncluster"
)

// These variables are set via ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "kubectl-fleet",
		Short: "KubeFleet cluster management plugin",
		Long:  "kubectl-fleet is a kubectl plugin for KubeFleet operations: draining and uncordoning member clusters, and approving staged update runs",
	}

	// Add version subcommand
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kubectl-fleet %s (commit %s, built %s)\n", version, commit, date)
		},
	})

	// Add subcommands
	rootCmd.AddCommand(approve.NewCmdApprove())
	rootCmd.AddCommand(draincluster.NewCmdDrainCluster())
	rootCmd.AddCommand(uncordoncluster.NewCmdUncordonCluster())

	// Cobra already prints the error and usage; only the exit code is left to us.
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
