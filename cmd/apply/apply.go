package apply

import (
	"fmt"
	"os"

	applyPkg "github.com/alibaba/open-simulator/pkg/apply"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var options = applyPkg.Options{}

var ApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a configuration to a resource by filename or stdin.",
	Run: func(cmd *cobra.Command, args []string) {
		applier := new(applyPkg.DefaulterApply)
		if err := applier.Run(options); err != nil {
			fmt.Printf("apply error: %s", err.Error())
			os.Exit(1)
		}
	},
}

func init() {
	ApplyCmd.Flags().StringVarP(&options.SimonConfig, "simon-config", "f", options.SimonConfig, "path to the cluster kube-config file used to connect cluster, one of both kube-config and cluster-config must exist.")
	ApplyCmd.Flags().StringVar(&options.DefaultSchedulerConfigFile, "default-scheduler-config", options.DefaultSchedulerConfigFile, "path to JSON or YAML file containing scheduler configuration.")
	ApplyCmd.Flags().BoolVar(&options.UseGreed, "use-greed", true, "use greedy algorithm when queue pods")
	ApplyCmd.Flags().BoolVarP(&options.Interactive, "interactive", "i", false, "interactive mode")

	if err := ApplyCmd.MarkFlagRequired("simon-config"); err != nil {
		log.Fatal("init ApplyCmd on simon-config flag failed")
		return
	}
}
