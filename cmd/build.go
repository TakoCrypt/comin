package cmd

import (
	"github.com/nlewo/comin/nix"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build a machine configuration",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		hosts := make([]string, 1)
		if hostname != "" {
			hosts[0] = hostname
		} else {
			hosts, _ = nix.List(flakeUrl)
		}
		for _, host := range hosts {
			logrus.Infof("Building the NixOS configuration of machine '%s'", host)
			_, err := nix.Build(flakeUrl, host)
			if err != nil {
				logrus.Errorf("Failed to build the configuration '%s': '%s'", host, err)
			}
		}
	},
}

func init() {
	buildCmd.Flags().StringVarP(&hostname, "hostname", "", "", "the name of the configuration to build")
	buildCmd.Flags().StringVarP(&flakeUrl, "flake-url", "", ".", "the URL of the flake")
	rootCmd.AddCommand(buildCmd)
}
