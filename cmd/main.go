package cmd

import (
	"flag"
	"fmt"

	"github.com/networkop/envoy-split-proxy/pkg/config"
	"github.com/networkop/envoy-split-proxy/pkg/envoy"

	"github.com/sirupsen/logrus"
)

var (
	configFlag = flag.String("conf", "", "split-proxy configuration file (YAML)")
	debugFlag  = flag.Bool("debug", false, "enable debug logging")
	envoyID    = flag.String("envoy-id", "split", "envoy Node ID")
	httpsPort  = flag.Int("https-port", 10000, "envoy https listener port")
	httpPort   = flag.Int("http-port", 10001, "envoy http listener port")
	grpcURL    = flag.String("grpc", ":18000", "GRPC URL to listen on for incoming connections from Envoy (default: ':18000')")
	cleanup    = flag.Bool("cleanup", false, "cleanup any created configuration")
)

// Run kicks off the main control loops
func Run() error {
	flag.Parse()

	if *debugFlag {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if *configFlag == "" {
		return fmt.Errorf("configuration file must be provided")
	}

	watcher, err := config.NewWatcher(*configFlag)
	if err != nil {
		return err
	}

	envoy, err := envoy.NewServer(*grpcURL, *envoyID, *httpsPort, *httpPort)
	if err != nil {
		return err
	}

	// dataChan is used to send the desired state to the envoy controller
	dataChan := make(chan *config.Data)

	go watcher.Sync(dataChan)

	go envoy.Configure(dataChan)

	select {}
}
