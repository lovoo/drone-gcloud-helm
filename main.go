package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
)

func main() {
	// Load env-file if it exists first
	if env := os.Getenv("PLUGIN_ENV_FILE"); env != "" {
		godotenv.Load(env)
	}

	var p Plugin
	if err := envconfig.Process("plugin", &p); err != nil {
		logrus.WithError(err).Fatal("failed to parse parameters")
	}
	if p.Debug {
		for _, e := range os.Environ() {
			fmt.Println(e)
		}
		logrus.SetLevel(logrus.DebugLevel)
	}
	if err := preparePlugin(&p); err != nil {
		logrus.WithError(err).Fatal("failed to prepare plugin")
	}

	if err := p.Exec(); err != nil {
		logrus.WithError(err).Fatal("failed to execute plugin")
	}
}

func preparePlugin(p *Plugin) error {
	if p.Package == "" {
		s := strings.Split(p.ChartPath, "/")
		p.Package = s[len(s)-1]
	}
	if p.Release == "" {
		p.Release = p.Package
	}
	if p.ChartRepo == "" && p.Bucket != "" {
		p.ChartRepo = fmt.Sprintf("https://%s.storage.googleapis.com/", p.Bucket)
	}
	if p.Namespace == "" {
		p.Namespace = "default"
	}

	return nil
}
