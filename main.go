package main

import (
	"os"

	"github.com/networkop/envoy-split-proxy/cmd"
	"github.com/sirupsen/logrus"
)

func main() {
	logrus.Info("Starting Envoy Split Proxy")

	if err := cmd.Run(); err != nil {
		logrus.Info(err)
		os.Exit(1)
	}

}
