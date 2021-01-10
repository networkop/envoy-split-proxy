package config

import (
	"fmt"
	"net"
	"os"
	"reflect"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"
)

// Config stores the parsed configuration file
type Config struct {
	Interface string   `yaml:"interface"`
	URLs      []string `yaml:"urls"`
}

// NewFromFile parses the configuration file to build the desired state
func (d *Data) newFromFile() error {
	var cfg Config
	logrus.Debugf("Parsing config file: %s", d.file)

	f, err := os.Open(d.file)
	if err != nil {
		return err
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&cfg)
	if err != nil {
		return err
	}
	logrus.Debugf("Parsed configuration: %+v", cfg)

	if cfg.Interface == "" {
		return fmt.Errorf("Bypass interface must be defined")
	}

	intf, err := netlink.LinkByName(cfg.Interface)
	if err != nil {
		return fmt.Errorf("Could find link %s: %s", cfg.Interface, err)
	}
	logrus.Debugf("Found interface with index: %d", intf.Attrs().Index)

	ips, err := netlink.AddrList(intf, unix.AF_INET)
	if err != nil {
		return fmt.Errorf("Could not find IPv4 addresses assigned to %s: %s", cfg.Interface, err)
	}
	logrus.Debugf("Found interface IPs: %+v", ips)

	firstIP := ips[0].IP
	logrus.Infof("Using IP %s for bypass", firstIP.String())

	if len(cfg.URLs) < 1 {
		return fmt.Errorf("More than 1 URLs must be configured")
	}

	d.idempotentUpdate(firstIP, dedup(cfg.URLs))

	return nil
}

func (d *Data) idempotentUpdate(ip net.IP, urls []string) {
	logrus.Debugf("Idempotently applying IP %s", ip.String())
	logrus.Debugf("and URLs %+v", urls)

	if !ip.Equal(d.IP) {
		logrus.Debugf("IP is different from %s", d.IP.String())
		d.IP = ip
		d.Changed = true
	}

	if !reflect.DeepEqual(urls, d.URLs) {
		logrus.Debugf("URLs are different from %s", d.URLs)
		d.URLs = urls
		d.Changed = true
	}
}

func dedup(input []string) (result []string) {
	uniq := make(map[string]bool)
	for _, i := range input {
		if _, exists := uniq[i]; !exists {
			uniq[i] = true
			result = append(result, i)
		}
	}
	return
}
