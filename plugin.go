package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

// Plugin defines the Helm plugin parameters.
type Plugin struct {
	Debug        bool     `envconfig:"DEBUG"`
	Actions      []string `envconfig:"ACTIONS" required:"true"`
	AuthKey      string   `envconfig:"AUTH_KEY"`
	Zone         string   `envconfig:"ZONE"`
	Cluster      string   `envconfig:"CLUSTER"`
	Project      string   `envconfig:"PROJECT"`
	Bucket       string   `envconfig:"BUCKET"`
	ChartPath    string   `envconfig:"CHART_PATH" required:"true"`
	ChartVersion string   `envconfig:"CHART_VERSION"`
	Package      string   `envconfig:"PACKAGE"`
	Values       []string `envconfig:"VALUES"`
}

const (
	gcloudBin  = "/opt/google-cloud-sdk/bin/gcloud"
	gsutilBin  = "/opt/google-cloud-sdk/bin/gsutil"
	kubectlBin = "/opt/google-cloud-sdk/bin/kubectl"
	helmBin    = "/opt/google-cloud-sdk/bin/helm"

	createPkg = "create"
	pushPkg   = "push"
	deployPkg = "deploy"
)

// Exec executes the plugin step.
func (p Plugin) Exec() error {
	if err := p.setupProject(); err != nil {
		return err
	}
	if err := p.helmInit(); err != nil {
		return err
	}
	for _, a := range p.Actions {
		switch a {
		case createPkg:
			if err := p.createPackage(); err != nil {
				return err
			}
		case pushPkg:
			if err := p.pushPackage(); err != nil {
				return err
			}
		case deployPkg:
			if err := p.deployPackage(); err != nil {
				return err
			}
		default:
			return errors.New("unknown action")
		}
	}

	return nil
}

// createPackage creates Helm package for Kubernetes.
// helm package --version $PLUGIN_CHART_VERSION $PLUGIN_CHART_PATH
func (p Plugin) createPackage() error {
	cmd := exec.Command(helmBin, "package",
		"--version",
		p.ChartVersion,
		p.ChartPath,
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// pushPackage pushes Helm package to the Google Storage.
// gsutil cp $PACKAGE-$PLUGIN_CHART_VERSION.tgz gs://$PLUGIN_BUCKET
func (p Plugin) pushPackage() error {
	cmd := exec.Command(gsutilBin, "cp",
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
		fmt.Sprintf("gs://%s", p.Bucket),
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// helm upgrade $PACKAGE $PACKAGE-$PLUGIN_CHART_VERSION.tgz -i
func (p Plugin) deployPackage() error {
	if p.Debug {
		if err := p.kubeConfig(); err != nil {
			return err
		}
	}
	cmd := exec.Command(helmBin, "upgrade",
		p.Package,
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
		"--set", strings.Join(p.Values, ","),
		"--install",
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// setupProject setups gcloud project.
// gcloud auth activate-service-account --key-file=$KEY_FILE_PATH
// gcloud config set project $PLUGIN_PROJECT
// gcloud container clusters get-credentials $PLUGIN_CLUSTER --zone $PLUGIN_ZONE
func (p Plugin) setupProject() error {
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

	cmds := make([]*exec.Cmd, 0, 3)

	// authorization
	cmds = append(cmds, exec.Command(gcloudBin, "auth",
		"activate-service-account",
		fmt.Sprintf("--key-file=%s", tmpfile.Name()),
	))
	// project configuration
	cmds = append(cmds, exec.Command(gcloudBin, "config",
		"set",
		"project",
		p.Project,
	))
	// cluster configuration
	cmds = append(cmds, exec.Command(gcloudBin, "container",
		"clusters",
		"get-credentials",
		p.Cluster,
		"--zone",
		p.Zone,
	))

	for _, cmd := range cmds {
		if p.Debug {
			trace(cmd)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			return err
		}
	}
	if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", tmpfile.Name()); err != nil {
		return err
	}

	return nil
}

// helmInit inits Triller on Kubernetes cluster.
// helm init
func (p Plugin) helmInit() error {
	cmd := exec.Command(helmBin, "init", "--client-only")
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func (p Plugin) kubeConfig() error {
	cmd := exec.Command(kubectlBin, "config", "view")
	trace(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// trace writes each command to stdout with the command wrapped in an xml
// tag so that it can be extracted and displayed in the logs.
func trace(cmd *exec.Cmd) {
	logrus.WithField("cmd", cmd.Args).Debug("debug")
}
