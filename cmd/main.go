package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/networkop/envoy-split-proxy/pkg/config"
	"github.com/networkop/envoy-split-proxy/pkg/envoy"
	"github.com/networkop/envoy-split-proxy/pkg/iptables"
	"github.com/networkop/envoy-split-proxy/pkg/route"

	"github.com/sirupsen/logrus"
)

var (
	configFlag = flag.String("conf", "", "split-proxy configuration file (YAML)")
	debugFlag  = flag.Bool("debug", false, "enable debug logging")
	envoyID    = flag.String("envoy-id", "split", "envoy Node ID")
	httpsPort  = flag.Int("https-port", 10000, "envoy https listener port")
	httpPort   = flag.Int("http-port", 10001, "envoy http listener port")
	grpcURL    = flag.String("grpc", ":18000", "GRPC URL to listen on for incoming connections from Envoy (default: ':18000')")
	bypassMark = flag.Int("bypass-mark", 0, "fwmark (SO_MARK) set on bypassed upstream sockets, e.g. 0x51821 (0 disables). Requires a host where Envoy can set SO_MARK; see README")
	manageIPT  = flag.Bool("iptables", false, "manage the nat PREROUTING REDIRECT rules for the two listeners. Requires CAP_NET_ADMIN")
	iptBin     = flag.String("iptables-bin", iptables.DefaultBinary, "iptables binary used with -iptables")
	httpsIn    = flag.Int("https-in", 443, "destination port redirected to the https listener with -iptables")
	httpIn     = flag.Int("http-in", 80, "destination port redirected to the http listener with -iptables")
	manageRule = flag.Bool("ip-rule", false, "manage the policy route and ip rule that steer bypassed traffic out the bypass interface. Requires CAP_NET_ADMIN")
	ruleTable  = flag.Int("rule-table", 200, "routing table holding the bypass default route, used with -ip-rule")
	rulePrio   = flag.Int("rule-priority", 150, "ip rule priority, used with -ip-rule. Must sit below any 'lookup main suppress_prefixlength 0' rule and above the VPN catch-all")
	verify     = flag.Bool("verify", true, "at startup, check the host actually steers bypassed traffic out the bypass interface, and warn if not")
	probeAddr  = flag.String("verify-probe", route.DefaultProbe, "destination used for the -verify route lookups. No packets are sent")
	recheck    = flag.Duration("recheck", time.Minute, "how often to re-assert the -ip-rule routing, 0 to only do it at startup")
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

	// Name the mechanism the host has to match, so a silent bypass is one log
	// line away from being diagnosed rather than a packet capture.
	if *bypassMark == 0 && !*manageRule {
		logrus.Info("Bypass steering: source address only. Host needs 'ip rule add from <bypass-ip> ...'")
	} else if *bypassMark == 0 {
		logrus.Infof("Bypass steering: source address, ip rule managed here (table %d, priority %d)", *ruleTable, *rulePrio)
	} else {
		logrus.Infof("Bypass steering: source address + fwmark %#x. Host needs 'ip rule add fwmark %#x ...'", *bypassMark, *bypassMark)
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

	// dataChan carries the desired state from the watcher; envoyChan passes it
	// on once the host routing for it is in place. Splitting them lets the
	// policy rule follow a change to the bypass interface's address instead of
	// being installed once at startup.
	dataChan := make(chan *config.Data)
	envoyChan := make(chan *config.Data)

	var router *route.Manager
	if *manageRule {
		router = route.NewManager(*ruleTable, *rulePrio, *bypassMark)
		defer router.Remove()
	}

	go watcher.Sync(dataChan)

	go envoy.Configure(envoyChan)

	go func() {
		for d := range dataChan {
			setLastState(d)
			if router != nil {
				if err := router.Ensure(d.Interface, d.IP); err != nil {
					// Non-fatal: the proxy still works, it just steers nothing.
					// The bypass failing silently is the whole reason this
					// package exists, so say so loudly.
					logrus.Warnf("Bypass routing is NOT in place, bypassed traffic will follow the host default route: %s", err)
				}
			}
			if *verify {
				route.Verify(d.Interface, d.IP, *probeAddr, *bypassMark)
			}
			envoyChan <- d
		}
	}()

	// Re-assert periodically. The rules are installed once at startup, but the
	// host can lose them afterwards: another agent flushing rules, a VPN
	// reconnect, or a boot where this ran before the default route existed.
	// Ensure is idempotent and silent when everything is already in place, so
	// this is quiet unless it actually fixes something.
	if router != nil && *recheck > 0 {
		go func() {
			for range time.Tick(*recheck) {
				last := lastState()
				if last == nil {
					continue
				}
				if err := router.Ensure(last.Interface, last.IP); err != nil {
					logrus.Warnf("Bypass routing re-check failed, bypassed traffic may be following "+
						"the host default route: %s", err)
				}
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	logrus.Infof("Received signal %s, shutting down", <-sig)

	return nil
}

// The re-check goroutine needs the latest interface and address without racing
// the config watcher, which owns them.
var (
	stateMu sync.Mutex
	state   *config.Data
)

func setLastState(d *config.Data) {
	stateMu.Lock()
	defer stateMu.Unlock()
	state = d
}

func lastState() *config.Data {
	stateMu.Lock()
	defer stateMu.Unlock()
	return state
}
