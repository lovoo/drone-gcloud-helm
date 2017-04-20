package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
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
	// the problem is, that p.AuthKey so far is "yaml" escaped string.
	// To fix this issue, needs to yaml.Unmarshal this string simple string.
	if err := yaml.Unmarshal([]byte(p.AuthKey), &p.AuthKey); err != nil {
		return err
	}
	if p.Package == "" {
		s := strings.Split(p.ChartPath, "/")
		p.Package = s[len(s)-1]
	}
	if p.ChartRepo == "" && p.Bucket != "" {
		p.ChartRepo = fmt.Sprintf("https://%s.storage.googleapis.com/", p.Bucket)
	}
	if p.Namespace == "" {
		p.Namespace = "default"
	}

	return nil
}
