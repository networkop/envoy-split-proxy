package config

import (
	"net"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"
)

// Data stores the desire state of the app
type Data struct {
	URLs    []string
	IP      net.IP
	file    string
	Changed bool
}

// NewWatcher builds new configuration file watcher
func NewWatcher(file string) (*Data, error) {
	logrus.Infof("Starting config watcher")

	d := &Data{
		file: file,
	}

	err := d.newFromFile()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Sync sends the desired state over the channel
func (d *Data) Sync(out chan *Data) {
	logrus.Debugf("Sending the initial parsed state: %+v", d)
	out <- d

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logrus.Panicf("Failed to initialise fsnotify: %s", err)
	}
	defer watcher.Close()

	logrus.Infof("Starting a watch on a file %s", d.file)
	if err := watcher.Add(d.file); err != nil {
		logrus.Panicf("Error watching the configuration file: %s", err)
	}

	for {
		select {
		case <-watcher.Events:
			if err = d.newFromFile(); err != nil {
				logrus.Infof("Error parsing the configuration file: %s", err)
				logrus.Info("Using the previous version of config")
			} else {
				if d.Changed {
					logrus.Debugf("Sending the parsed state: %+v", d)
					out <- d
				} else {
					logrus.Debug("No change detected...")
				}
			}

		case err := <-watcher.Errors:
			logrus.Infof("Received watcher.Error: %s", err)
		}
	}
}
