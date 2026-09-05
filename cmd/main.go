package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/networkop/envoy-split-proxy/pkg/config"
	"github.com/networkop/envoy-split-proxy/pkg/envoy"
	"github.com/networkop/envoy-split-proxy/pkg/iptables"

	"github.com/sirupsen/logrus"
)

var (
	configFlag = flag.String("conf", "", "split-proxy configuration file (YAML)")
	debugFlag  = flag.Bool("debug", false, "enable debug logging")
	envoyID    = flag.String("envoy-id", "split", "envoy Node ID")
	httpsPort  = flag.Int("https-port", 10000, "envoy https listener port")
	httpPort   = flag.Int("http-port", 10001, "envoy http listener port")
	grpcURL    = flag.String("grpc", ":18000", "GRPC URL to listen on for incoming connections from Envoy (default: ':18000')")
	bypassMark = flag.Int("bypass-mark", 0x51821, "fwmark (SO_MARK) set on bypassed upstream sockets, 0 to disable. Requires CAP_NET_ADMIN")
	manageIPT  = flag.Bool("iptables", false, "manage the nat PREROUTING REDIRECT rules for the two listeners. Requires CAP_NET_ADMIN")
	iptBin     = flag.String("iptables-bin", iptables.DefaultBinary, "iptables binary used with -iptables")
	httpsIn    = flag.Int("https-in", 443, "destination port redirected to the https listener with -iptables")
	httpIn     = flag.Int("http-in", 80, "destination port redirected to the http listener with -iptables")
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

	envoy, err := envoy.NewServer(*grpcURL, *envoyID, *httpsPort, *httpPort, *bypassMark)
	if err != nil {
		return err
	}

	if *manageIPT {
		ipt := iptables.NewManager(*iptBin, []iptables.Rule{
			{DestPort: *httpsIn, ToPort: *httpsPort},
			{DestPort: *httpIn, ToPort: *httpPort},
		})
		if err := ipt.Ensure(); err != nil {
			return err
		}
		// Without this a stopped proxy leaves PREROUTING pointing at a closed
		// port, black-holing web traffic for every client behind this box.
		defer ipt.Remove()
	}

	// dataChan is used to send the desired state to the envoy controller
	dataChan := make(chan *config.Data)

	go watcher.Sync(dataChan)

	go envoy.Configure(dataChan)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	logrus.Infof("Received signal %s, shutting down", <-sig)

	return nil
}
