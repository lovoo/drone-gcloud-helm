package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

func main() {
	var p Plugin
	if err := envconfig.Process("plugin", &p); err != nil {
		log.Fatalf("failed to parse parameters: %v", err)
	}
	if p.ShowEnv {
		for _, e := range os.Environ() {
			pair := strings.Split(e, "=")
			fmt.Println(pair[0])
		}
	}

	if err := preparePlugin(&p); err != nil {
		log.Fatalf("failed to prepare plugin: %v", err)
	}

	if err := p.Exec(); err != nil {
		log.Fatalf("failed to execute plugin: %v", err)
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

	if p.AuthKey != "" {
		tmpfile, err := ioutil.TempFile("", "auth-key.json")
		if err != nil {
			return err
		}

		if _, err := tmpfile.Write([]byte(p.AuthKey)); err != nil {
			return err
		}
		if err := tmpfile.Close(); err != nil {
			return err
		}
		p.KeyPath = tmpfile.Name()
	}

	if p.KeyPath != "" {
		if err := setupAuth(p.KeyPath, p.Debug); err != nil {
			return fmt.Errorf("could not setup auth: %v", err)
		}
	}

	return nil
}
